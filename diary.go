package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mkevac/markocaloriesbot/history"
)

func formatDaily(d history.Daily) string {
	text := fmt.Sprintf("%s (%s)\nLogged: %.0f kcal across %d meals", d.Day, d.Timezone, d.Calories, d.Meals)
	if d.Target > 0 {
		text += fmt.Sprintf("\nDaily target: %d kcal", d.Target)
		remaining := float64(d.Target) - d.Calories
		if remaining >= 0 {
			text += fmt.Sprintf("\nRemaining: %.0f kcal", remaining)
		} else {
			text += fmt.Sprintf("\nOver target: %.0f kcal", -remaining)
		}
	} else {
		text += "\nSet a daily target with /calories 2000."
	}
	return text
}

// diaryText runs before clarification handling so "save" never calls the LLM.
func diaryText(store *history.Store, message *models.Message, now time.Time) (string, bool, error) {
	if message == nil || message.From == nil {
		return "", false, nil
	}
	text := strings.TrimSpace(message.Text)
	if strings.EqualFold(text, "save") {
		if message.ReplyToMessage == nil {
			return "Reply save to your meal photo or my calorie estimate to log it.", true, nil
		}
		saved, err := store.Save(message.Chat.ID, message.From.ID, message.ReplyToMessage.ID, now)
		if errors.Is(err, sql.ErrNoRows) {
			return "I can’t find your meal for that reply. Please send a photo and wait for its estimate first.", true, nil
		}
		if errors.Is(err, history.ErrNotAnalyzed) {
			return "I don’t have a completed estimate to save yet. Wait for the analysis, or resend the photo, then reply save to the answer.", true, nil
		}
		if err != nil {
			return "", true, err
		}
		prefix := fmt.Sprintf("Saved: %.0f kcal.", saved.Calories)
		if !saved.Changed {
			prefix = "This meal is already saved; it wasn’t counted twice."
		} else if !saved.Added {
			prefix = fmt.Sprintf("Updated saved meal: %.0f kcal. Kept its original day.", saved.Calories)
		}
		return prefix + "\n\n" + formatDaily(saved.Daily), true, nil
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", false, nil
	}
	command := strings.SplitN(fields[0], "@", 2)[0]
	switch command {
	case "/calories":
		if len(fields) > 2 {
			return "Use /calories 2000 to set your daily target, or /calories off to clear it.", true, nil
		}
		if len(fields) == 2 {
			target := 0
			if !strings.EqualFold(fields[1], "off") {
				var err error
				target, err = strconv.Atoi(fields[1])
				if err != nil || target <= 0 || target > 100000 {
					return "Enter a positive whole number of calories, for example /calories 2000.", true, nil
				}
			}
			if err := store.SetTarget(message.From.ID, target); err != nil {
				return "", true, err
			}
		}
		summary, err := store.Today(message.From.ID, now)
		return formatDaily(summary) + "\n\nUse /timezone Asia/Dubai to set your local day.", true, err
	case "/timezone":
		if len(fields) > 2 {
			return "Use /timezone followed by a timezone, for example /timezone Asia/Dubai.", true, nil
		}
		if len(fields) == 2 {
			if _, err := time.LoadLocation(fields[1]); err != nil || fields[1] == "Local" {
				return "Unknown timezone. Try /timezone Asia/Dubai or /timezone Europe/Belgrade.", true, nil
			}
			if err := store.SetTimezone(message.From.ID, fields[1]); err != nil {
				return "", true, err
			}
		}
		summary, err := store.Today(message.From.ID, now)
		return formatDaily(summary) + "\n\nNew saves use this timezone. Previously saved meals keep their recorded dates.", true, err
	case "/today":
		if len(fields) > 1 {
			return "Use /today to see your saved meals’ calorie total for today.", true, nil
		}
		summary, err := store.Today(message.From.ID, now)
		return formatDaily(summary), true, err
	default:
		return "", false, nil
	}
}

func handleDiary(ctx context.Context, b *bot.Bot, message *models.Message) bool {
	// Use Telegram's send time so delayed processing across midnight keeps the
	// day on which the user actually sent "save".
	at := time.Now()
	if message.Date > 0 {
		at = time.Unix(int64(message.Date), 0)
	}
	text, handled, err := diaryText(mealHistory, message, at)
	if !handled {
		return false
	}
	if err != nil {
		log.Printf("Calorie diary error: %v", err)
		text = "I couldn’t update your calorie diary. Please try again."
	}
	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: message.Chat.ID, Text: text, ReplyParameters: &models.ReplyParameters{MessageID: message.ID}}); err != nil {
		log.Printf("Error sending calorie diary response: %v", err)
	}
	return true
}
