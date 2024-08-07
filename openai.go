package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const instructions = `You are a helpful assistant who can estimate calories and macronutrients in food based on description or photos.
Answer in JSON with a following JSON schema:
----
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "foods": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "description": { "type": "string" },
          "portion": { "type": "string" },
          "calories": { "type": "number" },
          "protein": { "type": "number" },
          "fat": { "type": "number" },
          "carbs": { "type": "number" }
        },
        "required": ["description", "portion", "calories", "protein", "fat", "carbs"]
      }
    },
    "total": {
      "type": "object",
      "properties": {
        "description": { "type": "string" },
        "portion": { "type": "string" },
        "calories": { "type": "number" },
        "protein": { "type": "number" },
        "fat": { "type": "number" },
        "carbs": { "type": "number" }
      },
      "required": ["description", "portion", "calories", "protein", "fat", "carbs"]
    }
  },
  "required": ["foods", "total"]
}
----
Answer only with JSON. Do not include any other information in your response.
`

type OpenAIResponse struct {
	Foods []struct {
		Description string  `json:"description"`
		Portion     string  `json:"portion"`
		Calories    float64 `json:"calories"`
		Protein     float64 `json:"protein"`
		Fat         float64 `json:"fat"`
		Carbs       float64 `json:"carbs"`
	} `json:"foods"`
	Total struct {
		Description string  `json:"description"`
		Portion     string  `json:"portion"`
		Calories    float64 `json:"calories"`
		Protein     float64 `json:"protein"`
		Fat         float64 `json:"fat"`
		Carbs       float64 `json:"carbs"`
	} `json:"total"`
}

func AskOpenAI(text string, pictures []string) (*OpenAIResponse, error) {
	token := os.Getenv("OPENAI_API_KEY")

	images := make([]openai.ChatMessagePart, 0)
	for _, picture := range pictures {
		images = append(images, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL: picture,
			},
		})
	}

	client := openai.NewClient(token)

	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4oMini,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: instructions,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: text,
				},
				{
					Role:         openai.ChatMessageRoleUser,
					MultiContent: images,
				},
			},
		},
	)

	if err != nil {
		return nil, fmt.Errorf("ChatCompletion error: %w", err)
	}

	response := resp.Choices[0].Message.Content

	// remove ```json from the beginning if it exists
	if response[:7] == "```json" {
		response = response[7:]
	}

	// remove ``` from the end if it exists
	if response[len(response)-3:] == "```" {
		response = response[:len(response)-3]
	}

	// parse json
	var openAIResponse OpenAIResponse

	if err := json.Unmarshal([]byte(response), &openAIResponse); err != nil {
		return nil, fmt.Errorf("unmarshal JSON error: %w", err)
	}

	return &openAIResponse, nil
}
