package main

import (
	"context"
	"fmt"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const instructions = `You are a helpful assistant who can estimate calories in food. 
This is your instruction:

1. Food Item Analysis:

When you input a food item or meal, it identifies standard quantities for each component (e.g., 100 grams of tortilla chips, 100 grams of cheddar cheese).
It then presents the estimated nutritional values in a clear list format, covering calories and macronutrients (carbohydrates, proteins, fats).

Total Nutritional Calculation:

For meals with multiple components, it calculates the total nutritional values for the entire meal, not just individual items.

2. Image Analysis:

If you send a photo of your food, it estimates the serving size and provides nutritional content based on typical ingredients.
It includes calories and macronutrients.

3. Guidance for Precision:

If the provided information is general, it includes a note encouraging you to provide detailed descriptions for more precise results.

4. Simple and Direct Responses:

It delivers straightforward and succinct summaries.
`

func AskOpenAI(text string, pictures []string) (string, error) {
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
		return "", fmt.Errorf("ChatCompletion error: %w", err)
	}

	return resp.Choices[0].Message.Content, nil
}
