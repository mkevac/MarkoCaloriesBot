package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
	"github.com/mkevac/markocaloriesbot/history"
	"github.com/mkevac/markocaloriesbot/stats"
)

var (
	adminUsername string
	mh            *MediaHandler
	usageStats    *stats.Store
	mealHistory   *history.Store
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

	mealHistory, err = history.Open(dbPath)
	if err != nil {
		log.Fatalf("Error opening meal history: %v", err)
	}
	defer mealHistory.Close()

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
	mh = NewMediaHandler(ctx, mealHistory, func(ctx context.Context, fileID string) (string, error) {
		file, err := b.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
		if err != nil {
			return "", err
		}
		return b.FileDownloadLink(file), nil
	})

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
			text = "I couldn’t analyze these photos. Please try again by replying with your clarification, or resend the photos."
		} else {
			text = FormatChatGPTResponse(mg.ChatGPTResponse)
		}

		sent, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: mg.ChatID,
			Text:   text,
			ReplyParameters: &models.ReplyParameters{
				MessageID: mg.ReplyToMessageID,
			},
		})
		if err != nil {
			log.Printf("Error sending message: %s", err)
		} else if err := mealHistory.LinkReply(mg.ChatID, sent.ID, mg.MealKey); err != nil {
			log.Printf("Error linking meal reply: %v", err)
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
	text := fmt.Sprintf("Usage stats\nTracking since: %s UTC\n\nAll time: %d users, %d requests\nLast 24 hours: %d users, %d requests\nLast 7 days: %d users, %d requests\nLast 30 days: %d users, %d requests\n\nAverage requests/day since tracking began: %.1f\nAverage requests/user: %.1f\n\nA request is a photo, album (counted once), or clarification, including failed analyses. Commands are excluded.",
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

// prepareMediaItem records original photos or resolves a clarification without
// making network calls. The history lookup enforces chat and owner boundaries.
func prepareMediaItem(store *history.Store, message *models.Message) (*MediaItem, error) {
	if message == nil || message.From == nil {
		return nil, nil
	}
	item := &MediaItem{ChatID: message.Chat.ID, UserID: message.From.ID, ReplyToMessageID: message.ID}
	if len(message.Photo) > 0 {
		key, err := savePhoto(store, message)
		item.MealKey = key
		item.GroupID = message.MediaGroupID
		return item, err
	}
	text := strings.TrimSpace(message.Text)
	if text == "" || strings.HasPrefix(text, "/") || message.ReplyToMessage == nil {
		return nil, nil
	}
	target := message.ReplyToMessage
	key, added, err := store.Correct(item.ChatID, item.UserID, target.ID, message.ID, text)
	if errors.Is(err, sql.ErrNoRows) && len(target.Photo) > 0 && target.From != nil && target.From.ID == item.UserID && target.Chat.ID == item.ChatID {
		// Telegram includes a directly replied-to photo even if it predates history.
		// An old album cannot be reconstructed in full from a single embedded photo.
		if target.MediaGroupID != "" {
			return nil, sql.ErrNoRows
		}
		if _, err = savePhoto(store, target); err != nil {
			return nil, err
		}
		key, added, err = store.Correct(item.ChatID, item.UserID, target.ID, message.ID, text)
	}
	if err != nil {
		return nil, err
	}
	if !added {
		return nil, nil
	}
	item.MealKey = key
	return item, nil
}

func savePhoto(store *history.Store, message *models.Message) (string, error) {
	photo := message.Photo[0]
	for _, p := range message.Photo {
		if p.Width*p.Height > photo.Width*photo.Height {
			photo = p
		}
	}
	return store.AddPhoto(message.Chat.ID, message.From.ID, message.ID, message.MediaGroupID, photo.FileID, message.Caption)
}

func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	message := update.Message
	if message == nil || message.From == nil {
		return
	}
	item, err := prepareMediaItem(mealHistory, message)
	if err != nil {
		text := "I couldn’t save this meal context. Please try again."
		if errors.Is(err, sql.ErrNoRows) {
			text = "I can’t find your original photos for that reply. Please resend the photo or album with your clarification."
		} else {
			log.Printf("Error preparing meal request: %v", err)
		}
		if _, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: message.Chat.ID, Text: text, ReplyParameters: &models.ReplyParameters{MessageID: message.ID}}); sendErr != nil {
			log.Printf("Error sending clarification help: %v", sendErr)
		}
		return
	}
	if item == nil {
		return
	}
	if err := usageStats.Record(item.ChatID, item.UserID, message.ID, item.GroupID, message.From.Username, time.Now().UTC()); err != nil {
		log.Printf("Error recording usage: %v", err)
	}
	select {
	case mh.InputChannel <- item:
	case <-ctx.Done():
	}
}
