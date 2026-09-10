package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mkevac/markocaloriesbot/history"
)

//go:embed timezones.txt
var timezoneNames string

func cityZones(query string) []string {
	normalize := func(s string) string { return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", " ")) }
	q := normalize(query)
	if len(q) < 2 {
		return nil
	}
	var exact, partial []string
	for _, zone := range strings.Split(timezoneNames, "\n") {
		if zone == "" || strings.HasPrefix(zone, "#") {
			continue
		}
		city := zone[strings.LastIndex(zone, "/")+1:]
		if normalize(city) == q || normalize(zone) == q {
			exact = append(exact, zone)
		} else if strings.Contains(normalize(zone), q) {
			partial = append(partial, zone)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	// A bounded result list invites a more specific search rather than flooding chat.
	if len(partial) > 8 {
		return nil
	}
	return partial
}

type preferenceSession struct {
	Token          string
	ChatID, UserID int64
	PromptID       int
	Kind           string
	Expires        time.Time
}
type preferenceUI struct {
	mu       sync.Mutex
	sessions map[string]preferenceSession
}

var preferences = &preferenceUI{sessions: make(map[string]preferenceSession)}

func (p *preferenceUI) newSession(chat, user int64) (preferenceSession, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return preferenceSession{}, err
	}
	s := preferenceSession{Token: hex.EncodeToString(bytes), ChatID: chat, UserID: user, Expires: time.Now().Add(15 * time.Minute)}
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, old := range p.sessions {
		if time.Now().After(old.Expires) || (old.ChatID == chat && old.UserID == user) {
			delete(p.sessions, key)
		}
	}
	p.sessions[s.Token] = s
	return s, nil
}
func (p *preferenceUI) get(token string, chat, user int64) (preferenceSession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[token]
	return s, ok && s.ChatID == chat && s.UserID == user && time.Now().Before(s.Expires)
}
func (p *preferenceUI) pending(m *models.Message) (preferenceSession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.sessions {
		if s.ChatID != m.Chat.ID || s.UserID != m.From.ID || time.Now().After(s.Expires) || (s.Kind != "target" && s.Kind != "city") {
			continue
		}
		if m.ReplyToMessage != nil {
			if m.ReplyToMessage.ID == s.PromptID {
				return s, true
			}
			continue
		}
		if m.Chat.Type == "private" {
			return s, true
		}
	}
	return preferenceSession{}, false
}
func (p *preferenceUI) update(s preferenceSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.sessions[s.Token]; ok {
		p.sessions[s.Token] = s
	}
}
func (p *preferenceUI) clear(chat, user int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for token, s := range p.sessions {
		if s.ChatID == chat && s.UserID == user {
			delete(p.sessions, token)
		}
	}
}
func preferenceButton(s preferenceSession, label, action, value string) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: label, CallbackData: "prefs:" + s.Token + ":" + action + ":" + value}
}
func preferenceMenu(s preferenceSession, kind string) *models.InlineKeyboardMarkup {
	button := func(label, action, value string) models.InlineKeyboardButton {
		return preferenceButton(s, label, action, value)
	}
	rows := [][]models.InlineKeyboardButton{}
	if kind == "calories" {
		rows = append(rows, []models.InlineKeyboardButton{button("1,500 kcal", "target", "1500"), button("1,800 kcal", "target", "1800")}, []models.InlineKeyboardButton{button("2,000 kcal", "target", "2000"), button("2,500 kcal", "target", "2500")}, []models.InlineKeyboardButton{button("Custom…", "custom", "target"), button("Clear target", "target", "0")})
	} else {
		rows = append(rows, []models.InlineKeyboardButton{button("Dubai", "zone", "Asia/Dubai"), button("Belgrade", "zone", "Europe/Belgrade")}, []models.InlineKeyboardButton{button("London", "zone", "Europe/London"), button("UTC", "zone", "UTC")}, []models.InlineKeyboardButton{button("Other city…", "custom", "city")})
	}
	rows = append(rows, []models.InlineKeyboardButton{button("Cancel", "cancel", "")})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}
func sendPreference(ctx context.Context, b *bot.Bot, chat int64, text string, markup any, replyID int) (int, error) {
	params := &bot.SendMessageParams{ChatID: chat, Text: text, ReplyMarkup: markup}
	if replyID > 0 {
		params.ReplyParameters = &models.ReplyParameters{MessageID: replyID}
	}
	sent, err := b.SendMessage(ctx, params)
	if err != nil {
		return 0, err
	}
	return sent.ID, nil
}
func (p *preferenceUI) prompt(ctx context.Context, b *bot.Bot, s preferenceSession, kind string, replyID int, warning string) error {
	text, placeholder := "How many calories would you like per day? Send a whole number, such as 2100.", "e.g. 2100"
	if kind == "city" {
		text = "Which city are you in? Try Paris, New York, or another major city in your timezone."
		placeholder = "e.g. Paris"
	}
	if warning != "" {
		text = warning + "\n\n" + text
	}
	text += "\n/cancel to stop."
	id, err := sendPreference(ctx, b, s.ChatID, text, &models.ForceReply{ForceReply: true, Selective: true, InputFieldPlaceholder: placeholder}, replyID)
	if err == nil {
		s.Kind = kind
		s.PromptID = id
		p.update(s)
	}
	return err
}

func (p *preferenceUI) message(ctx context.Context, b *bot.Bot, store *history.Store, m *models.Message) bool {
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return false
	}
	cmd := strings.SplitN(fields[0], "@", 2)[0]
	if cmd == "/cancel" {
		p.clear(m.Chat.ID, m.From.ID)
		_, err := sendPreference(ctx, b, m.Chat.ID, "Settings entry cancelled.", nil, m.ID)
		if err != nil {
			log.Printf("Preference response error: %v", err)
		}
		return true
	}
	if len(fields) == 1 && (cmd == "/calories" || cmd == "/timezone") {
		p.clear(m.Chat.ID, m.From.ID)
		summary, err := store.Today(m.From.ID, time.Now())
		if err == nil {
			var s preferenceSession
			s, err = p.newSession(m.Chat.ID, m.From.ID)
			if err == nil {
				text := fmt.Sprintf("Daily calorie target: %d kcal\nChoose a target:", summary.Target)
				if summary.Target == 0 {
					text = "No daily calorie target set.\nChoose a target:"
				}
				if cmd == "/timezone" {
					text = "Current timezone: " + summary.Timezone + "\nChoose your city or search for another:"
				}
				_, err = sendPreference(ctx, b, m.Chat.ID, text, preferenceMenu(s, strings.TrimPrefix(cmd, "/")), m.ID)
			}
		}
		if err != nil {
			log.Printf("Preference menu error: %v", err)
			sendPreference(ctx, b, m.Chat.ID, "I couldn’t open settings. Please try again.", nil, m.ID)
		}
		return true
	}
	if strings.HasPrefix(cmd, "/") {
		p.clear(m.Chat.ID, m.From.ID)
		return false
	}
	s, ok := p.pending(m)
	if !ok {
		return false
	}
	var err error
	if s.Kind == "target" {
		target, parseErr := strconv.Atoi(strings.TrimSpace(m.Text))
		if parseErr != nil || target < 1 || target > 100000 {
			err = p.prompt(ctx, b, s, "target", m.ID, "Please enter a positive whole number.")
		} else {
			err = store.SetTarget(m.From.ID, target)
			if err == nil {
				p.clear(s.ChatID, s.UserID)
				_, err = sendPreference(ctx, b, s.ChatID, fmt.Sprintf("Daily target saved: %d kcal.\nUse /today to check your progress.", target), nil, m.ID)
			}
		}
	} else {
		zones := cityZones(m.Text)
		if len(zones) == 0 {
			err = p.prompt(ctx, b, s, "city", m.ID, "I couldn’t narrow that down. Try a nearby major city or a more specific name.")
		} else {
			rows := [][]models.InlineKeyboardButton{}
			for _, zone := range zones {
				label := strings.ReplaceAll(zone, "_", " ")
				rows = append(rows, []models.InlineKeyboardButton{preferenceButton(s, label, "zone", zone)})
			}
			rows = append(rows, []models.InlineKeyboardButton{preferenceButton(s, "Search again", "custom", "city"), preferenceButton(s, "Cancel", "cancel", "")})
			_, err = sendPreference(ctx, b, s.ChatID, "Choose your timezone:", &models.InlineKeyboardMarkup{InlineKeyboard: rows}, m.ID)
			if err == nil {
				s.Kind = "choices"
				p.update(s)
			}
		}
	}
	if err != nil {
		log.Printf("Preference input error: %v", err)
		sendPreference(ctx, b, m.Chat.ID, "I couldn’t update settings. Please try again.", nil, m.ID)
	}
	return true
}

func preferenceCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	q := update.CallbackQuery
	if q == nil {
		return
	}
	acknowledge := func(text string) {
		if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: text}); err != nil {
			log.Printf("Preference callback error: %v", err)
		}
	}
	if q.Message.Message == nil {
		acknowledge("Open /calories or /timezone again.")
		return
	}
	message := q.Message.Message
	parts := strings.SplitN(q.Data, ":", 4)
	if len(parts) != 4 {
		acknowledge("Invalid selection.")
		return
	}
	s, ok := preferences.get(parts[1], message.Chat.ID, q.From.ID)
	if !ok {
		acknowledge("Open your own /calories or /timezone menu to continue.")
		return
	}
	acknowledge("")
	var err error
	switch parts[2] {
	case "custom":
		if parts[3] != "target" && parts[3] != "city" {
			return
		}
		// Reply to the command that opened this menu to target ForceReply in groups.
		replyID := 0
		if message.ReplyToMessage != nil && message.ReplyToMessage.From != nil && message.ReplyToMessage.From.ID == q.From.ID {
			replyID = message.ReplyToMessage.ID
		}
		err = preferences.prompt(ctx, b, s, parts[3], replyID, "")
	case "target":
		target, parseErr := strconv.Atoi(parts[3])
		if parseErr != nil || target < 0 || target > 100000 {
			return
		}
		err = mealHistory.SetTarget(q.From.ID, target)
		if err == nil {
			preferences.clear(s.ChatID, s.UserID)
			text := fmt.Sprintf("Daily target saved: %d kcal.\nUse /today to check your progress.", target)
			if target == 0 {
				text = "Daily target cleared. Your saved meals are kept."
			}
			_, err = sendPreference(ctx, b, s.ChatID, text, nil, 0)
		}
	case "zone":
		err = mealHistory.SetTimezone(q.From.ID, parts[3])
		if err == nil {
			preferences.clear(s.ChatID, s.UserID)
			_, err = sendPreference(ctx, b, s.ChatID, "Timezone saved: "+parts[3]+".\nNew meals use your local day. Previously saved dates stay the same.", nil, 0)
		}
	case "cancel":
		preferences.clear(s.ChatID, s.UserID)
		_, err = sendPreference(ctx, b, s.ChatID, "Settings entry cancelled.", nil, 0)
	default:
		return
	}
	if err != nil {
		log.Printf("Preference update error: %v", err)
		sendPreference(ctx, b, s.ChatID, "I couldn’t update settings. Please try again.", nil, 0)
		return
	}
	// Remove used buttons; a fresh command always opens a new menu.
	if _, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{ChatID: s.ChatID, MessageID: message.ID, ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}}); err != nil {
		log.Printf("Preference keyboard update error: %v", err)
	}
}
