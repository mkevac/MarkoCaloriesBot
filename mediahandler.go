package main

import (
	"fmt"
	"log"
	"time"
)

type MediaItem struct {
	GroupID          string
	ChatID           int64
	Caption          string
	URL              string
	ReplyToMessageID int
}

type MediaGroup struct {
	GroupID          string
	ChatID           int64
	Caption          string
	URLs             []string
	LastUpdate       time.Time
	ChatGPTResponse  *OpenAIResponse
	ChatGPTError     error
	ReplyToMessageID int
}

type MediaHandler struct {
	InputChannel    chan *MediaItem
	internalChannel chan *MediaGroup
	OutputChannel   chan *MediaGroup
}

func NewMediaHandler() *MediaHandler {
	mh := &MediaHandler{
		InputChannel:    make(chan *MediaItem),
		internalChannel: make(chan *MediaGroup),
		OutputChannel:   make(chan *MediaGroup),
	}

	go mh.mediaConsolidator()
	go mh.mediaDownloader()

	return mh
}

func (m *MediaHandler) mediaDownloader() {
	for mg := range m.internalChannel {
		log.Printf("asking ChatGPT about group '%s' with %d images", mg.GroupID, len(mg.URLs))
		response, err := AskOpenAI(mg.Caption, mg.URLs)
		if err != nil {
			mg.ChatGPTError = err
		} else {
			mg.ChatGPTResponse = response
		}

		log.Printf("ChatGPT response: %v", mg.ChatGPTResponse)
		m.OutputChannel <- mg
	}
}

func (m *MediaHandler) mediaConsolidator() {
	incoming := make(map[string]*MediaGroup)
	timer := time.NewTicker(time.Second)
	threshold := time.Second

	for {
		select {
		case message := <-m.InputChannel:
			log.Printf("received image")
			if message.GroupID == "" {
				fmt.Println("consolidating single image")
				m.internalChannel <- &MediaGroup{
					GroupID:          "",
					ChatID:           message.ChatID,
					Caption:          message.Caption,
					URLs:             []string{message.URL},
					LastUpdate:       time.Now(),
					ReplyToMessageID: message.ReplyToMessageID,
				}
				continue
			} else {
				if group, ok := incoming[message.GroupID]; ok {
					group.URLs = append(group.URLs, message.URL)
					group.LastUpdate = time.Now()
					// Use the first message's ID for reply
					if group.ReplyToMessageID == 0 {
						group.ReplyToMessageID = message.ReplyToMessageID
					}
				} else {
					incoming[message.GroupID] = &MediaGroup{
						GroupID:          message.GroupID,
						ChatID:           message.ChatID,
						Caption:          message.Caption,
						URLs:             []string{message.URL},
						LastUpdate:       time.Now(),
						ReplyToMessageID: message.ReplyToMessageID,
					}
				}
			}
		case <-timer.C:
			for groupID, group := range incoming {
				if time.Since(group.LastUpdate) >= threshold {
					log.Printf("consolidating group %s", groupID)
					m.internalChannel <- group
					delete(incoming, groupID)
				}
			}
		}
	}
}
