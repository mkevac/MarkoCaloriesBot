package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mkevac/markocaloriesbot/stats"
)

var (
	adminUsername string
	mh            *MediaHandler
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	adminUsername = os.Getenv("ADMIN_USERNAME")
	log.Printf("Admin username: %s", adminUsername)

	mh = NewMediaHandler()

	opts := []bot.Option{
		bot.WithDefaultHandler(handler),
		//bot.WithServerURL("http://telegram-bot-api:8081"),
	}

	var b *bot.Bot
	var err error

	for range time.Tick(time.Second * 5) {
		b, err = bot.New(os.Getenv("TELEGRAM_BOT_API_TOKEN"), opts...)
		if err != nil {
			log.Printf("Error creating bot: %s", err)
			time.Sleep(time.Second * 5)
		} else {
			break
		}
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/stats", bot.MatchTypeExact, statsHandler)

	go answerMachine(ctx, b)

	b.Start(ctx)
}

func FormatChatGPTResponse(response *OpenAIResponse) string {
	// make a pretty response from OpenAIResponse struct

	// first list all the foods with their calories, protein, fat, and carbs
	var foods strings.Builder
	for _, food := range response.Foods {
		foods.WriteString(fmt.Sprintf("%s (%s):\n", food.Description, food.Portion))
		foods.WriteString(fmt.Sprintf("Calories: %.2f\n", food.Calories))
		foods.WriteString(fmt.Sprintf("Protein: %.2f\n", food.Protein))
		foods.WriteString(fmt.Sprintf("Fat: %.2f\n", food.Fat))
		foods.WriteString(fmt.Sprintf("Carbs: %.2f\n", food.Carbs))
		foods.WriteString("\n")
	}

	// then list the total calories, protein, fat, and carbs
	total := response.Total
	var totalString strings.Builder
	totalString.WriteString("Total:\n")
	totalString.WriteString(fmt.Sprintf("Calories: %.2f\n", total.Calories))
	totalString.WriteString(fmt.Sprintf("Protein: %.2f\n", total.Protein))
	totalString.WriteString(fmt.Sprintf("Fat: %.2f\n", total.Fat))
	totalString.WriteString(fmt.Sprintf("Carbs: %.2f\n", total.Carbs))

	// combine the two
	var result strings.Builder
	result.WriteString(foods.String())
	result.WriteString(totalString.String())

	return result.String()
}

func answerMachine(ctx context.Context, b *bot.Bot) {
	for mg := range mh.OutputChannel {

		log.Printf("Sending ChatGPT response to chat %d", mg.ChatID)

		var text string

		if mg.ChatGPTError != nil {
			text = fmt.Sprintf("Error processing image: %s", mg.ChatGPTError)
		} else {
			text = FormatChatGPTResponse(mg.ChatGPTResponse)
		}

		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: mg.ChatID,
			Text:   text,
			//ParseMode: models.ParseModeMarkdown,
		})
		if err != nil {
			log.Printf("Error sending message: %s", err)
		}
	}
}

func statsHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message.From.Username != adminUsername {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "You are not authorized to use this command",
		})
		return
	}

	stats := stats.GetStats()

	totalRequests := 0
	for _, count := range stats.Requests {
		totalRequests += count
	}

	// prepare stats message in Markdown format
	var statsMessage strings.Builder
	statsMessage.WriteString("*Stats*\n")
	statsMessage.WriteString("```\n")
	statsMessage.WriteString(fmt.Sprintf("Total requests: %d\n", totalRequests))
	for username, count := range stats.Requests {
		statsMessage.WriteString(fmt.Sprintf("%s: %d\n", username, count))
	}
	statsMessage.WriteString(fmt.Sprintf("Download errors: %d\n", stats.DownloadErrors))
	statsMessage.WriteString(fmt.Sprintf("Unrecognized commands: %d\n", stats.UnrecognizedCommands))
	statsMessage.WriteString("```")

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      statsMessage.String(),
		ParseMode: models.ParseModeMarkdown,
	})
}

func messageToMediaItem(ctx context.Context, b *bot.Bot, message *models.Message) (*MediaItem, error) {
	if len(message.Photo) == 0 {
		return nil, fmt.Errorf("no photo in message")
	}

	// find biggest photo
	photo := message.Photo[0]
	for _, p := range message.Photo {
		if p.FileSize > photo.FileSize {
			photo = p
		}
	}

	file, err := b.GetFile(ctx, &bot.GetFileParams{
		FileID: photo.FileID,
	})

	if err != nil {
		return nil, fmt.Errorf("error getting file: %w", err)
	}

	link := b.FileDownloadLink(file)

	return &MediaItem{
		GroupID: message.MediaGroupID,
		ChatID:  message.Chat.ID,
		Caption: message.Caption,
		URL:     link,
	}, nil
}

func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		log.Printf("Received update without message")
		return
	}

	log.Printf("[%s]: received message: '%s'", update.Message.From.Username, update.Message.Text)

	// convert update.Message to json and print it
	data, _ := json.MarshalIndent(update.Message, "", "  ")
	log.Printf("Message: %s", data)

	mi, err := messageToMediaItem(ctx, b, update.Message)
	if err != nil {
		log.Printf("Error converting message to media item: %s", err)
		return
	}
	log.Printf("Message: %v", mi)

	mh.InputChannel <- mi

	stats.AddRequest(update.Message.From.Username)
}
