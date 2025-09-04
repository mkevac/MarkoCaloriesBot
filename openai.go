package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/responses"
)

const instructions = `You are a helpful assistant who can estimate calories and macronutrients in food based on description or photos. You use metric measurements.
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

	client := openai.NewClient(option.WithAPIKey(token))

	// Build the message content for the responses API
	var content responses.ResponseInputMessageContentListParam

	// Add text part
	content = append(content, responses.ResponseInputContentParamOfInputText(text))

	// Add image parts
	for _, picture := range pictures {
		imageParam := responses.ResponseInputImageParam{
			Detail: responses.ResponseInputImageDetailHigh,
			ImageURL: openai.String(picture),
		}
		content = append(content, responses.ResponseInputContentUnionParam{
			OfInputImage: &imageParam,
		})
	}

	// Create input as a single message with mixed content
	input := responses.ResponseNewParamsInputUnion{
		OfInputItemList: responses.ResponseInputParam{
			responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleUser),
		},
	}

	resp, err := client.Responses.New(context.Background(), responses.ResponseNewParams{
		Model: responses.ChatModelGPT5Mini,
		Instructions: openai.String(instructions),
		Input: input,
		Text: responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name: "calorie_response",
					Schema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"foods": map[string]interface{}{
								"type": "array",
								"items": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"description": map[string]interface{}{"type": "string"},
										"portion":     map[string]interface{}{"type": "string"},
										"calories":    map[string]interface{}{"type": "number"},
										"protein":     map[string]interface{}{"type": "number"},
										"fat":         map[string]interface{}{"type": "number"},
										"carbs":       map[string]interface{}{"type": "number"},
									},
									"required": []string{"description", "portion", "calories", "protein", "fat", "carbs"},
									"additionalProperties": false,
								},
							},
							"total": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"description": map[string]interface{}{"type": "string"},
									"portion":     map[string]interface{}{"type": "string"},
									"calories":    map[string]interface{}{"type": "number"},
									"protein":     map[string]interface{}{"type": "number"},
									"fat":         map[string]interface{}{"type": "number"},
									"carbs":       map[string]interface{}{"type": "number"},
								},
								"required": []string{"description", "portion", "calories", "protein", "fat", "carbs"},
								"additionalProperties": false,
							},
						},
						"required": []string{"foods", "total"},
						"additionalProperties": false,
					},
				},
			},
		},
	})

	if err != nil {
		return nil, fmt.Errorf("Response API error: %w", err)
	}

	// Extract JSON response from output items
	if len(resp.Output) == 0 {
		return nil, fmt.Errorf("no output in response")
	}

	// For reasoning models like GPT-5 Mini, we need to look through all output items
	// to find the final response (usually comes after reasoning)
	for _, outputItem := range resp.Output {
		// Try to get as message
		outputMessage := outputItem.AsMessage()
		if outputMessage.Type != "" && len(outputMessage.Content) > 0 {
			// Get the first content item as text
			contentItem := outputMessage.Content[0]
			outputText := contentItem.AsOutputText()
			if outputText.Type != "" {
				// parse json directly since responses API returns structured data
				var openAIResponse OpenAIResponse
				if err := json.Unmarshal([]byte(outputText.Text), &openAIResponse); err != nil {
					return nil, fmt.Errorf("unmarshal JSON error: %w", err)
				}
				return &openAIResponse, nil
			}
		}

		// For reasoning models, check if there's a reasoning item with summary
		if reasoning := outputItem.AsReasoning(); reasoning.Type != "" {
			// Check if reasoning has summary with the final answer
			if len(reasoning.Summary) > 0 {
				// Look for text content in the summary
				for _, summaryItem := range reasoning.Summary {
					if summaryItem.Text != "" {
						// Try to parse the summary text as JSON
						var openAIResponse OpenAIResponse
						if err := json.Unmarshal([]byte(summaryItem.Text), &openAIResponse); err == nil {
							return &openAIResponse, nil
						}
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("no valid JSON response found in output items")
}
