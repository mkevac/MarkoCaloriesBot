package main

import (
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/mkevac/markocaloriesbot/history"
)

func TestDiaryCommandsAndSaveRouting(t *testing.T) {
	s := testHistory(t)
	now := time.Now()
	msg := &models.Message{ID: 10, Chat: models.Chat{ID: 1}, From: &models.User{ID: 1}}
	for _, text := range []string{"/calories 2000", "/today", "/timezone Asia/Dubai", "/calories off", "save", "SAVE", "/calories nope", "/calories -5", "/calories 2.5", "/timezone Invalid/Zone"} {
		msg.Text = text
		reply, handled, err := diaryText(s, msg, now)
		if err != nil || !handled || reply == "" {
			t.Fatalf("%s: %s %v %v", text, reply, handled, err)
		}
	}
	item, err := prepareMediaItem(s, testPhoto(1, 1, 1, "", "file", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutEstimate(1, 1, item.MealKey, 1, 400, `{}`); err != nil {
		t.Fatal(err)
	}
	if err = s.LinkEstimateReply(1, 2, item.MealKey, 1); err != nil {
		t.Fatal(err)
	}
	msg = testReply(1, 1, 3, " SAVE ", &models.Message{ID: 2})
	reply, handled, err := diaryText(s, msg, now)
	if err != nil || !handled || !strings.Contains(reply, "Saved: 400") {
		t.Fatalf("save: %s %v", reply, err)
	}
	// Save handling has not added the word save to the LLM clarification history.
	meal, err := s.Load(1, 1, item.MealKey, 3)
	if err != nil || strings.Contains(meal.Prompt, "SAVE") {
		t.Fatalf("save became correction: %+v %v", meal, err)
	}
	msg.Text = "There is no potato"
	if _, handled, err := diaryText(s, msg, now); err != nil || handled {
		t.Fatal("correction intercepted")
	}
}
func TestFormatDailyRemaining(t *testing.T) {
	for _, tc := range []struct {
		cal  float64
		want string
	}{{500, "Remaining: 1500"}, {2000, "Remaining: 0"}, {2100, "Over target: 100"}} {
		text := formatDaily(history.Daily{Day: "2026-09-11", Timezone: "UTC", Target: 2000, Calories: tc.cal})
		if !strings.Contains(text, tc.want) {
			t.Fatal(text)
		}
	}
}
