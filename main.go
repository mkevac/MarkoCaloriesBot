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
	"github.com/joho/godotenv"
	"github.com/mkevac/markocaloriesbot/stats"
)

var (
	adminUsername string
	mh            *MediaHandler
	usageStats    *stats.Store
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("Error loading .env file: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	adminUsername = os.Getenv("ADMIN_USERNAME")
	log.Printf("Admin username: %s", adminUsername)

	botToken := os.Getenv("TELEGRAM_BOT_API_TOKEN")
	if botToken == "" {
		log.Fatal("TELEGRAM_BOT_API_TOKEN environment variable is not set")
	}

	dbPath := os.Getenv("STATS_DB_PATH")
	if dbPath == "" {
		dbPath = "./data/stats.db"
	}
	var err error
	usageStats, err = stats.Open(dbPath)
	if err != nil {
		log.Fatalf("Error opening stats database: %v", err)
	}
	defer usageStats.Close()

	mh = NewMediaHandler()

	opts := []bot.Option{
		bot.WithDefaultHandler(handler),
		bot.WithCheckInitTimeout(time.Minute),
		//bot.WithServerURL("http://telegram-bot-api:8081"),
	}

	var b *bot.Bot

	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Context cancelled, exiting")
			return
		case <-ticker.C:
			b, err = bot.New(botToken, opts...)
			if err != nil {
				log.Printf("Error creating bot: %s", err)
			} else {
				goto botCreated
			}
		}
	}

botCreated:

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
			ReplyParameters: &models.ReplyParameters{
				MessageID: mg.ReplyToMessageID,
			},
		})
		if err != nil {
			log.Printf("Error sending message: %s", err)
		}
	}
}

func statsHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.From == nil {
		return
	}
	if update.Message.From.Username == "" || adminUsername == "" || !strings.EqualFold(update.Message.From.Username, strings.TrimPrefix(adminUsername, "@")) {
		b.SendMessage(ctx, &bot.SendMessageParams{ChatID: update.Message.Chat.ID, Text: "You are not authorized to use this command"})
		return
	}
	now := time.Now().UTC()
	snapshot, err := usageStats.Get(now)
	text := "Unable to load usage stats. Please try again."
	if err != nil {
		log.Printf("Error loading stats: %v", err)
	} else {
		text = formatStats(snapshot, now)
	}
	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: update.Message.Chat.ID, Text: text}); err != nil {
		log.Printf("Error sending stats: %v", err)
	}
}

func formatStats(s stats.Stats, now time.Time) string {
	days := now.Sub(s.Since).Hours() / 24
	// Count the first partial day as one day so startup doesn't inflate the rate.
	if days < 1 {
		days = 1
	}
	average := 0.0
	if s.All.Users > 0 {
		average = float64(s.All.Requests) / float64(s.All.Users)
	}
	text := fmt.Sprintf("Usage stats\nTracking since: %s UTC\n\nAll time: %d users, %d requests\nLast 24 hours: %d users, %d requests\nLast 7 days: %d users, %d requests\nLast 30 days: %d users, %d requests\n\nAverage requests/day since tracking began: %.1f\nAverage requests/user: %.1f\n\nA request is a submitted photo or album (one per album), including failed analyses. Commands are excluded.",
		s.Since.Format("2006-01-02 15:04"), s.All.Users, s.All.Requests,
		s.Day.Users, s.Day.Requests, s.Week.Users, s.Week.Requests, s.Month.Users, s.Month.Requests,
		float64(s.All.Requests)/days, average)
	if len(s.Top) > 0 {
		text += "\n\nTop users (all time):"
		for _, user := range s.Top {
			name := fmt.Sprintf("User %d", user.ID)
			if user.Username != "" {
				name = "@" + user.Username
			}
			text += fmt.Sprintf("\n%s: %d requests", name, user.Requests)
		}
	}
	return text
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
		GroupID:          message.MediaGroupID,
		ChatID:           message.Chat.ID,
		Caption:          message.Caption,
		URL:              link,
		ReplyToMessageID: message.ID,
	}, nil
}

func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.From == nil {
		log.Printf("Received update without message")
		return
	}

	log.Printf("[%s]: received message: '%s'", update.Message.From.Username, update.Message.Text)

	// convert update.Message to json and print it
	data, _ := json.MarshalIndent(update.Message, "", "  ")
	log.Printf("Message: %s", data)

	if len(update.Message.Photo) > 0 {
		if err := usageStats.Record(update.Message.Chat.ID, update.Message.From.ID, update.Message.ID, update.Message.MediaGroupID, update.Message.From.Username, time.Now().UTC()); err != nil {
			log.Printf("Error recording usage: %v", err)
		}
	}

	mi, err := messageToMediaItem(ctx, b, update.Message)
	if err != nil {
		log.Printf("Error converting message to media item: %s", err)
		return
	}
	log.Printf("Message: %v", mi)

	mh.InputChannel <- mi

}
