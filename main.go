package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/awslabs/aws-lambda-go-api-proxy/core"
	"github.com/line/line-bot-sdk-go/linebot"
)

const openaiURL = "https://api.openai.com/v1/chat/completions"

var baseMessages []Message
var bot *linebot.Client
var apiKey string

func init() {
	var err error
	bot, err = linebot.New(
		os.Getenv("CHANNEL_SECRET"),
		os.Getenv("CHANNEL_TOKEN"),
	)
	if err != nil {
		log.Fatal(err)
	}

	baseMessages = append(baseMessages, Message{
		Role:    "system",
		Content: os.Getenv("ROLE_PROMPT"),
	})

	apiKey = os.Getenv("OPENAI_API_KEY")
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
			fmt.Printf("RequestId: %s, Method: %s, Path: %s, Body: %s\n", requestId, method, path, body)
			if err == linebot.ErrInvalidSignature {
				return newResponse(http.StatusBadRequest), err
			} else {
				return newResponse(http.StatusInternalServerError), err
			}
		}

		for _, event := range events {
			switch event.Type {
			case linebot.EventTypeMessage:
				switch message := event.Message.(type) {
				case *linebot.TextMessage:
					messages := append(baseMessages, Message{
						Role:    "user",
						Content: message.Text,
					})

					response := getOpenAIResponse(apiKey, messages)
					if _, err = bot.ReplyMessage(event.ReplyToken, linebot.NewTextMessage(response.Choices[0].Messages.Content)).Do(); err != nil {
						fmt.Printf("RequestId: %s, Method: %s, Path: %s, Body: %s\n", requestId, method, path, body)
						return newResponse(http.StatusInternalServerError), err
					}
				}
			}
		}
		return newResponse(http.StatusOK), nil
	default:
		return newResponse(http.StatusBadRequest), nil
	}
}

func newAPIGatewayProxyResponse() events.APIGatewayProxyResponse {
	var headers = make(map[string]string)
	var mHeaders = make(map[string][]string)
	return events.APIGatewayProxyResponse{Headers: headers, MultiValueHeaders: mHeaders}
}

func newResponse(statusCode int) events.APIGatewayProxyResponse {
	res := newAPIGatewayProxyResponse()
	res.StatusCode = statusCode
	return res
}

func getOpenAIResponse(apiKey string, messages []Message) OpenaiResponse {
	requestBody := OpenaiRequest{
		Model:    "gpt-3.5-turbo",
		Messages: messages,
	}

	requestJSON, _ := json.Marshal(requestBody)

	req, err := http.NewRequest("POST", openaiURL, bytes.NewBuffer(requestJSON))
	if err != nil {
		panic(err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			panic(err)
		}
	}(resp.Body)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	var response OpenaiResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		println("Error: ", err.Error())
		return OpenaiResponse{}
	}

	// messages = append(messages, Message{
	// 	Role:    "assistant",
	// 	Content: response.Choices[0].Messages.Content,
	// })

	return response
}

type OpenaiRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type OpenaiResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int      `json:"created"`
	Choices []Choice `json:"choices"`
	Usages  Usage    `json:"usage"`
}

type Choice struct {
	Index        int     `json:"index"`
	Messages     Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
