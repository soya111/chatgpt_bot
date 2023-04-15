package main

import (
	"chatgpt/pkg/openai"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/awslabs/aws-lambda-go-api-proxy/core"
	"github.com/guregu/dynamo"
	"github.com/line/line-bot-sdk-go/linebot"
)

var baseMessages []openai.Message
var bot *linebot.Client
var apiKey string
var sess *session.Session
var recentChatsLimit int

func init() {
	var err error
	bot, err = linebot.New(
		os.Getenv("CHANNEL_SECRET"),
		os.Getenv("CHANNEL_TOKEN"),
	)
	if err != nil {
		log.Fatal(err)
	}

	baseMessages = append(baseMessages, openai.Message{
		Role:    "system",
		Content: os.Getenv("ROLE_PROMPT"),
	})

	apiKey = os.Getenv("OPENAI_API_KEY")

	sess = session.Must(session.NewSession())

	recentChatsLimitStr := os.Getenv("RECENT_CHATS_LIMIT")
	recentChatsLimit, err = strconv.Atoi(recentChatsLimitStr)
	if err != nil {
		fmt.Println("Error parsing RECENT_CHATS_LIMIT:", err)
		return
	}
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	path := request.Path
	body := request.Body
	method := request.HTTPMethod

	lambdaCtx, _ := lambdacontext.FromContext(ctx)
	requestId := lambdaCtx.AwsRequestID

	fmt.Printf("RequestId: %s, Method: %s, Path: %s, Body: %s\n", requestId, method, path, body)

	switch path {
	case "/ChatGPT":
		// LINEのsdkがHTTPを前提にParseしているのでHttpRequestに戻す
		r := &core.RequestAccessor{}
		httpRequest, err := r.EventToRequest(request)
		if err != nil {
			return newResponse(http.StatusInternalServerError), err
		}

		events, err := bot.ParseRequest(httpRequest)
		if err != nil {
			if err == linebot.ErrInvalidSignature {
				return newResponse(http.StatusBadRequest), err
			} else {
				return newResponse(http.StatusInternalServerError), err
			}
		}

		for _, event := range events {
			switch event.Type {
			case linebot.EventTypeMessage:
				switch event.Message.(type) {
				case *linebot.TextMessage:

					// DynamoDBに会話を保存する
					putMessageToDynamoDB(event, "user", event.Message.(*linebot.TextMessage).Text)

					// baseMessagesにdynamoDBから取得したメッセージを追加
					messages := append(baseMessages, getMessagesFromDynamoDB(event)...)

					request, err := openai.NewOpenAIRequest(apiKey, messages)
					if err != nil {
						return newResponse(http.StatusInternalServerError), err
					}
					response, err := openai.GetOpenAIResponse(request)
					if err != nil {
						return newResponse(http.StatusInternalServerError), err
					}
					// responseの中身をログに出力
					fmt.Println(strings.ReplaceAll(fmt.Sprintf("%+v", response), "\n", " "))
					if _, err = bot.ReplyMessage(event.ReplyToken, linebot.NewTextMessage(response.Choices[0].Messages.Content)).Do(); err != nil {
						return newResponse(http.StatusInternalServerError), err
					}

					// dynamoDBにアシスタントのメッセージを保存
					putMessageToDynamoDB(event, response.Choices[0].Messages.Role, response.Choices[0].Messages.Content)
				}
			}
		}
		return newResponse(http.StatusOK), nil
	default:
		return newResponse(http.StatusBadRequest), nil
	}
}

func newResponse(statusCode int) events.APIGatewayProxyResponse {
	var headers = make(map[string]string)
	var mHeaders = make(map[string][]string)
	return events.APIGatewayProxyResponse{StatusCode: statusCode, Headers: headers, MultiValueHeaders: mHeaders}
}

type ChatHistory struct {
	ChatIdentifier string `dynamo:"chatId"`      // 会話の識別子
	Timestamp      int64  `dynamo:"timestamp,N"` // メッセージのタイムスタンプ
	Message        string `dynamo:"message"`     // メッセージの内容
	Role           string `dynamo:"role"`        // メッセージの役割（ユーザーまたはアシスタント）
}

// DynamoDBから過去の会話を取得するメソッド
func getMessagesFromDynamoDB(event *linebot.Event) []openai.Message {
	db := dynamo.New(sess, &aws.Config{Region: aws.String("ap-northeast-1")})
	table := db.Table("line_log")
	chatId := getChatIdentifier(event)

	// 会話の識別子をキーにして、会話の履歴を取得する
	var chatHistories []ChatHistory
	err := table.Get("chatId", chatId).Limit(int64(recentChatsLimit)).Order(false).All(&chatHistories)
	if err != nil {
		fmt.Println(err)
		return []openai.Message{}
	}

	// 会話の履歴をMessage型に変換する
	var messages []openai.Message
	for _, chatHistory := range reverseChatHistories(chatHistories) {
		messages = append(messages, openai.Message{
			Role:    chatHistory.Role,
			Content: chatHistory.Message,
		})
	}

	return messages
}

// DynamoDBに会話の履歴を書き込むメソッド
func putMessageToDynamoDB(event *linebot.Event, role string, message string) {
	db := dynamo.New(sess, &aws.Config{Region: aws.String("ap-northeast-1")})
	table := db.Table("line_log")
	chatId := getChatIdentifier(event)

	// 会話の履歴をChatHistory型に変換する
	chatHistory := ChatHistory{
		ChatIdentifier: chatId,
		Timestamp:      time.Now().Unix(),
		Message:        message,
		Role:           role,
	}

	// 会話の履歴をDynamoDBに書き込む
	err := table.Put(chatHistory).Run()
	if err != nil {
		fmt.Println(err)
	}
}

// *linebot.Eventから会話の識別子を取得するメソッド
func getChatIdentifier(event *linebot.Event) string {
	// グループまたはトークルームの場合は、グループIDまたはトークルームIDを返す
	if event.Source.Type == linebot.EventSourceTypeGroup {
		return event.Source.GroupID
	} else if event.Source.Type == linebot.EventSourceTypeRoom {
		return event.Source.RoomID
	} else {
		return event.Source.UserID
	}
}

// ChatHistory型のスライスをリバースするメソッド
func reverseChatHistories(chatHistories []ChatHistory) []ChatHistory {
	for i := len(chatHistories)/2 - 1; i >= 0; i-- {
		opp := len(chatHistories) - 1 - i
		chatHistories[i], chatHistories[opp] = chatHistories[opp], chatHistories[i]
	}
	return chatHistories
}
