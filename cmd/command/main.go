package main

import (
	"chatgpt/pkg/openai"
	"fmt"
	"strings"
)

func main() {
	apiKey := "MY_API_KEY"

	messages := []openai.Message{
		{
			Role:    "system",
			Content: "You are a helpful assistant.",
		},
		{
			Role:    "user",
			Content: "Who won the world series in 2020?",
		},
	}

	request, err := openai.NewOpenAIRequest(apiKey, messages)
	if err != nil {
		fmt.Errorf("Error: %w", err)
		return
	}

	response, err := openai.GetOpenAIResponse(request)
	if err != nil {
		fmt.Errorf("Error: %w", err)
		return
	}
	fmt.Println(strings.ReplaceAll(fmt.Sprintf("%+v", response), "\n", " "))
}
