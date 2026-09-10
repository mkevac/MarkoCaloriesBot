package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mkevac/markocaloriesbot/history"
)

type MediaItem struct {
	GroupID          string
	ChatID, UserID   int64
	MealKey          string
	ReplyToMessageID int
}
type MediaGroup struct {
	MediaItem
	LastUpdate      time.Time
	ChatGPTResponse *OpenAIResponse
	ChatGPTError    error
}

type MediaHandler struct {
	InputChannel    chan *MediaItem
	internalChannel chan *MediaGroup
	OutputChannel   chan *MediaGroup
	history         *history.Store
	resolveFile     func(context.Context, string) (string, error)
	ask             func(string, []string) (*OpenAIResponse, error)
}

func NewMediaHandler(ctx context.Context, store *history.Store, resolveFile func(context.Context, string) (string, error)) *MediaHandler {
	m := &MediaHandler{InputChannel: make(chan *MediaItem), internalChannel: make(chan *MediaGroup), OutputChannel: make(chan *MediaGroup), history: store, resolveFile: resolveFile, ask: AskOpenAI}
	go m.mediaConsolidator(ctx)
	go m.mediaDownloader(ctx)
	return m
}

func (m *MediaHandler) analyze(ctx context.Context, mg *MediaGroup) (*OpenAIResponse, error) {
	meal, err := m.history.Load(mg.ChatID, mg.UserID, mg.MealKey, mg.ReplyToMessageID)
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(meal.FileIDs))
	for _, id := range meal.FileIDs {
		// Telegram download links expire. Resolve the persistent file ID on every call.
		url, err := m.resolveFile(ctx, id)
		if err != nil {
			return nil, err
		}
		urls = append(urls, url)
	}
	return m.ask(meal.Prompt, urls)
}

func (m *MediaHandler) mediaDownloader(ctx context.Context) {
	defer close(m.OutputChannel)
	for {
		select {
		case <-ctx.Done():
			return
		case mg, ok := <-m.internalChannel:
			if !ok {
				return
			}
			mg.ChatGPTResponse, mg.ChatGPTError = m.analyze(ctx, mg)
			if mg.ChatGPTError != nil {
				log.Printf("Meal analysis failed in chat %d", mg.ChatID)
			}
			select {
			case m.OutputChannel <- mg:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (m *MediaHandler) mediaConsolidator(ctx context.Context) {
	defer close(m.internalChannel)
	incoming := make(map[string]*MediaGroup)
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	send := func(group *MediaGroup) bool {
		select {
		case m.internalChannel <- group:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-m.InputChannel:
			if message.GroupID == "" {
				if !send(&MediaGroup{MediaItem: *message, LastUpdate: time.Now()}) {
					return
				}
			} else {
				// Albums from different chats or users must never share photos.
				key := fmt.Sprintf("%d:%d:%s", message.ChatID, message.UserID, message.GroupID)
				if group, ok := incoming[key]; ok {
					group.LastUpdate = time.Now()
				} else {
					incoming[key] = &MediaGroup{MediaItem: *message, LastUpdate: time.Now()}
				}
			}
		case <-timer.C:
			for key, group := range incoming {
				if time.Since(group.LastUpdate) >= time.Second {
					if !send(group) {
						return
					}
					delete(incoming, key)
				}
			}
		}
	}
}
