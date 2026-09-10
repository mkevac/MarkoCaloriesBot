package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mkevac/markocaloriesbot/history"
)

func testPhoto(chat, user int64, id int, album, file, caption string) *models.Message {
	return &models.Message{ID: id, Chat: models.Chat{ID: chat}, From: &models.User{ID: user}, MediaGroupID: album, Caption: caption, Photo: []models.PhotoSize{{FileID: "small", Width: 10, Height: 10}, {FileID: file, Width: 100, Height: 100}}}
}
func testReply(chat, user int64, id int, text string, target *models.Message) *models.Message {
	return &models.Message{ID: id, Chat: models.Chat{ID: chat}, From: &models.User{ID: user}, Text: text, ReplyToMessage: target}
}
func testHistory(t *testing.T) *history.Store {
	t.Helper()
	s, err := history.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCorrectionResendsWholeAlbumAndAccumulates(t *testing.T) {
	s := testHistory(t)
	first := testPhoto(10, 1, 1, "album", "first", "Lunch")
	second := testPhoto(10, 1, 2, "album", "second", "About 300 g")
	for _, photo := range []*models.Message{first, second} {
		if _, err := prepareMediaItem(s, photo); err != nil {
			t.Fatal(err)
		}
	}
	correction := testReply(10, 1, 4, "No potato, those are cabbages", second)
	item, err := prepareMediaItem(s, correction)
	if err != nil {
		t.Fatal(err)
	}
	if item.GroupID != "" || item.ReplyToMessageID != 4 {
		t.Fatalf("bad correction routing: %+v", item)
	}
	if err := s.LinkReply(10, 5, item.MealKey); err != nil {
		t.Fatal(err)
	}
	again, err := prepareMediaItem(s, testReply(10, 1, 6, "Actually 200 g", &models.Message{ID: 5, Chat: models.Chat{ID: 10}, From: &models.User{ID: 999, IsBot: true}}))
	if err != nil {
		t.Fatal(err)
	}
	var prompts []string
	var resolutions []string
	m := &MediaHandler{history: s, resolveFile: func(_ context.Context, id string) (string, error) {
		resolutions = append(resolutions, id)
		return "fresh://" + id, nil
	}, ask: func(prompt string, urls []string) (*OpenAIResponse, error) {
		if !reflect.DeepEqual(urls, []string{"fresh://first", "fresh://second"}) {
			t.Fatalf("wrong images: %v", urls)
		}
		prompts = append(prompts, prompt)
		return &OpenAIResponse{}, nil
	}}
	for _, job := range []*MediaItem{item, again} {
		if _, err := m.analyze(context.Background(), &MediaGroup{MediaItem: *job}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(prompts[0], "Actually 200 g") {
		t.Fatal("later correction leaked into earlier request")
	}
	for _, want := range []string{"Lunch", "About 300 g", "No potato, those are cabbages", "Actually 200 g"} {
		if !strings.Contains(prompts[1], want) {
			t.Fatalf("missing %q in %s", want, prompts[1])
		}
	}
	if len(resolutions) != 4 {
		t.Fatal("did not refresh every file for each call")
	}
	// Repeated Telegram delivery must not enqueue the same clarification twice.
	duplicate, err := prepareMediaItem(s, correction)
	if err != nil || duplicate != nil {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	// Replying to the clarification itself also works.
	if _, err := prepareMediaItem(s, testReply(10, 1, 7, "No added oil", correction)); err != nil {
		t.Fatal(err)
	}
}

func TestCorrectionOwnershipAndMissingContext(t *testing.T) {
	s := testHistory(t)
	photo := testPhoto(10, 1, 1, "", "original", "")
	item, err := prepareMediaItem(s, photo)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LinkReply(10, 2, item.MealKey); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []*models.Message{
		testReply(10, 2, 3, "change", photo),
		testReply(20, 1, 3, "change", photo),
		testReply(10, 2, 3, "change", &models.Message{ID: 2}),
		testReply(10, 1, 3, "change", &models.Message{ID: 99}),
	} {
		if _, err := prepareMediaItem(s, msg); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("unexpected access: %v", err)
		}
	}
	for _, msg := range []*models.Message{nil, {Text: "hello", From: &models.User{ID: 1}}, testReply(10, 1, 4, "  ", photo), testReply(10, 1, 4, "/stats", photo)} {
		if item, err := prepareMediaItem(s, msg); err != nil || item != nil {
			t.Fatalf("unexpected request: %+v %v", item, err)
		}
	}
	// A directly replied-to old single photo can be recovered from Telegram.
	old := testPhoto(10, 1, 50, "", "old", "Original caption")
	if _, err := prepareMediaItem(s, testReply(10, 1, 51, "cabbage", old)); err != nil {
		t.Fatal(err)
	}
	// Don't silently analyze just one photo from an unknown historical album.
	oldAlbum := testPhoto(10, 1, 60, "old-album", "old", "")
	if _, err := prepareMediaItem(s, testReply(10, 1, 61, "cabbage", oldAlbum)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestReplyContextSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := history.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	item, err := prepareMediaItem(s, testPhoto(1, 1, 1, "", "file", "caption"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.LinkReply(1, 2, item.MealKey); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareMediaItem(s, testReply(1, 1, 3, "cabbage", &models.Message{ID: 2})); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = history.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item, err = prepareMediaItem(s, testReply(1, 1, 4, "200 grams", &models.Message{ID: 2}))
	if err != nil {
		t.Fatal(err)
	}
	meal, err := s.Load(1, 1, item.MealKey, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(meal.FileIDs, []string{"file"}) || !strings.Contains(meal.Prompt, "cabbage") || !strings.Contains(meal.Prompt, "200 grams") {
		t.Fatalf("lost context: %+v", meal)
	}
}

func TestAlbumsStaySeparateAcrossChats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &MediaHandler{InputChannel: make(chan *MediaItem), internalChannel: make(chan *MediaGroup)}
	go m.mediaConsolidator(ctx)
	for _, chat := range []int64{1, 2} {
		m.InputChannel <- &MediaItem{ChatID: chat, UserID: 3, GroupID: "same", MealKey: "key", ReplyToMessageID: 1}
	}
	seen := map[int64]bool{}
	for i := 0; i < 2; i++ {
		select {
		case group := <-m.internalChannel:
			seen[group.ChatID] = true
		case <-time.After(5 * time.Second):
			t.Fatal("album dispatch timed out")
		}
	}
	if len(seen) != 2 {
		t.Fatal("albums merged across chats")
	}
}

func TestFileResolutionFailureDoesNotCallLLM(t *testing.T) {
	s := testHistory(t)
	item, err := prepareMediaItem(s, testPhoto(1, 1, 1, "", "file", ""))
	if err != nil {
		t.Fatal(err)
	}
	m := &MediaHandler{history: s, resolveFile: func(context.Context, string) (string, error) { return "", errors.New("download unavailable") }, ask: func(string, []string) (*OpenAIResponse, error) { t.Fatal("LLM called without images"); return nil, nil }}
	if _, err := m.analyze(context.Background(), &MediaGroup{MediaItem: *item}); err == nil {
		t.Fatal("expected error")
	}
}

// Fake Telegram transport verifies that the ID returned by SendMessage is saved.
type telegramReplyClient struct{}

func (telegramReplyClient) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":100,"chat":{"id":1,"type":"private"}}}`)), Header: make(http.Header)}, nil
}
func TestBotAnswerBecomesCorrectionTarget(t *testing.T) {
	s := testHistory(t)
	item, err := prepareMediaItem(s, testPhoto(1, 1, 1, "", "file", "meal"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := bot.New("123:test", bot.WithSkipGetMe(), bot.WithHTTPClient(time.Second, telegramReplyClient{}))
	if err != nil {
		t.Fatal(err)
	}
	previousHandler, previousHistory := mh, mealHistory
	defer func() { mh, mealHistory = previousHandler, previousHistory }()
	mealHistory = s
	mh = &MediaHandler{OutputChannel: make(chan *MediaGroup, 1)}
	mh.OutputChannel <- &MediaGroup{MediaItem: *item, ChatGPTResponse: &OpenAIResponse{}}
	close(mh.OutputChannel)
	answerMachine(context.Background(), b)
	correction, err := prepareMediaItem(s, testReply(1, 1, 101, "cabbage", &models.Message{ID: 100}))
	if err != nil || correction == nil || correction.MealKey != item.MealKey {
		t.Fatalf("reply was not linked: %+v %v", correction, err)
	}
}
