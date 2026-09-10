package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type preferenceClient struct {
	calls  []map[string]any
	paths  []string
	nextID int
}

func (c *preferenceClient) Do(req *http.Request) (*http.Response, error) {
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		return nil, err
	}
	data := make(map[string]any)
	for key, values := range req.Form {
		if len(values) > 0 {
			data[key] = values[0]
		}
	}
	c.calls = append(c.calls, data)
	c.paths = append(c.paths, req.URL.Path)
	c.nextID++
	result := `true`
	if strings.HasSuffix(req.URL.Path, "/sendMessage") {
		result = fmt.Sprintf(`{"message_id":%d,"chat":{"id":1,"type":"private"}}`, c.nextID)
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":` + result + `}`))}, nil
}
func preferenceTestBot(t *testing.T) (*bot.Bot, *preferenceClient) {
	t.Helper()
	client := &preferenceClient{nextID: 100}
	b, err := bot.New("123:test", bot.WithSkipGetMe(), bot.WithHTTPClient(time.Second, client))
	if err != nil {
		t.Fatal(err)
	}
	return b, client
}
func TestPreferenceMenusAndCustomTarget(t *testing.T) {
	s := testHistory(t)
	p := &preferenceUI{sessions: make(map[string]preferenceSession)}
	b, client := preferenceTestBot(t)
	msg := &models.Message{ID: 1, Chat: models.Chat{ID: 1, Type: "private"}, From: &models.User{ID: 1}, Text: "/calories"}
	if !p.message(context.Background(), b, s, msg) {
		t.Fatal("menu not handled")
	}
	raw, _ := json.Marshal(client.calls[len(client.calls)-1])
	for _, want := range []string{"1,500 kcal", "1,800 kcal", "2,000 kcal", "2,500 kcal", "Custom", "Clear target"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s in %s", want, raw)
		}
	}
	var session preferenceSession
	for _, v := range p.sessions {
		session = v
	}
	if err := p.prompt(context.Background(), b, session, "target", 1, ""); err != nil {
		t.Fatal(err)
	}
	msg.Text = "not a number"
	p.message(context.Background(), b, s, msg)
	msg.Text = "2150"
	if !p.message(context.Background(), b, s, msg) {
		t.Fatal("custom number not handled")
	}
	day, err := s.Today(1, time.Now())
	if err != nil || day.Target != 2150 {
		t.Fatalf("target: %+v %v", day, err)
	}
	if _, ok := p.pending(msg); ok {
		t.Fatal("prompt not cleared")
	}
}
func TestCitySearchAndCallbackBounds(t *testing.T) {
	for query, want := range map[string]string{"dubai": "Asia/Dubai", "Belgrade": "Europe/Belgrade", "New York": "America/New_York", "PARIS": "Europe/Paris", "UTC": "UTC"} {
		zones := cityZones(query)
		if len(zones) != 1 || zones[0] != want {
			t.Fatalf("%s: %v", query, zones)
		}
	}
	if len(cityZones("not a real city")) != 0 || len(cityZones("a")) != 0 {
		t.Fatal("unknown or broad city guessed")
	}
	session := preferenceSession{Token: strings.Repeat("a", 16)}
	for _, zone := range strings.Split(timezoneNames, "\n") {
		if zone == "" || strings.HasPrefix(zone, "#") {
			continue
		}
		if _, err := time.LoadLocation(zone); err != nil {
			t.Fatalf("unsupported bundled timezone %s: %v", zone, err)
		}
		if n := len(preferenceButton(session, zone, "zone", zone).CallbackData); n > 64 {
			t.Fatalf("callback too long: %s (%d)", zone, n)
		}
	}
}
func TestPreferenceOwnershipExpiryAndMealReplies(t *testing.T) {
	p := &preferenceUI{sessions: make(map[string]preferenceSession)}
	s, err := p.newSession(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	s.Kind = "city"
	s.PromptID = 10
	p.update(s)
	for _, ids := range [][2]int64{{1, 3}, {2, 2}} {
		if _, ok := p.get(s.Token, ids[0], ids[1]); ok {
			t.Fatal("cross-user or cross-chat access")
		}
	}
	msg := testReply(1, 2, 20, "cabbage", &models.Message{ID: 11})
	msg.Chat.Type = "private"
	if _, ok := p.pending(msg); ok {
		t.Fatal("meal correction intercepted")
	}
	msg.ReplyToMessage = &models.Message{ID: 10}
	if _, ok := p.pending(msg); !ok {
		t.Fatal("prompt reply not recognized")
	}
	msg.Chat.Type = "group"
	msg.ReplyToMessage = nil
	if _, ok := p.pending(msg); ok {
		t.Fatal("unrelated group text intercepted")
	}
	newer, err := p.newSession(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.get(s.Token, 1, 2); ok {
		t.Fatal("old menu remains active")
	}
	newer.Expires = time.Now().Add(-time.Minute)
	p.update(newer)
	if _, ok := p.get(newer.Token, 1, 2); ok {
		t.Fatal("expired menu accepted")
	}
}
func TestPreferenceCallbackSavesAndRejectsOtherUsers(t *testing.T) {
	oldPrefs, oldHistory := preferences, mealHistory
	defer func() { preferences, mealHistory = oldPrefs, oldHistory }()
	preferences = &preferenceUI{sessions: make(map[string]preferenceSession)}
	mealHistory = testHistory(t)
	b, _ := preferenceTestBot(t)
	s, err := preferences.newSession(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	q := &models.CallbackQuery{ID: "callback", From: models.User{ID: 3}, Data: preferenceButton(s, "Dubai", "zone", "Asia/Dubai").CallbackData, Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: 10, Chat: models.Chat{ID: 1}}}}
	preferenceCallback(context.Background(), b, &models.Update{CallbackQuery: q})
	day, _ := mealHistory.Today(3, time.Now())
	if day.Timezone != "UTC" {
		t.Fatal("another user changed setting")
	}
	q.From.ID = 2
	preferenceCallback(context.Background(), b, &models.Update{CallbackQuery: q})
	day, _ = mealHistory.Today(2, time.Now())
	if day.Timezone != "Asia/Dubai" {
		t.Fatal("timezone not saved")
	}
	s, _ = preferences.newSession(1, 2)
	q.Data = preferenceButton(s, "2000", "target", "2000").CallbackData
	preferenceCallback(context.Background(), b, &models.Update{CallbackQuery: q})
	day, _ = mealHistory.Today(2, time.Now())
	if day.Target != 2000 {
		t.Fatal("preset not saved")
	}
}
func TestOtherCityAndCancel(t *testing.T) {
	s := testHistory(t)
	p := &preferenceUI{sessions: make(map[string]preferenceSession)}
	b, client := preferenceTestBot(t)
	session, _ := p.newSession(1, 1)
	if err := p.prompt(context.Background(), b, session, "city", 1, ""); err != nil {
		t.Fatal(err)
	}
	msg := &models.Message{ID: 2, Chat: models.Chat{ID: 1, Type: "private"}, From: &models.User{ID: 1}, Text: "New York"}
	if !p.message(context.Background(), b, s, msg) {
		t.Fatal("city ignored")
	}
	raw, _ := json.Marshal(client.calls[len(client.calls)-1])
	if !strings.Contains(string(raw), "America/New_York") {
		t.Fatalf("missing zone button: %s", raw)
	}
	day, _ := s.Today(1, time.Now())
	if day.Timezone != "UTC" {
		t.Fatal("city search saved without selection")
	}
	msg.Text = "/cancel"
	p.message(context.Background(), b, s, msg)
	if _, ok := p.get(session.Token, 1, 1); ok {
		t.Fatal("cancel did not clear state")
	}
}
