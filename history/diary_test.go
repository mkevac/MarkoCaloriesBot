package history

import (
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func diaryStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "diary.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func estimatedMeal(t *testing.T, s *Store, chat, user int64, id int, album string, cal float64) string {
	t.Helper()
	key, err := s.AddPhoto(chat, user, id, album, "file", "meal")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutEstimate(chat, user, key, id, cal, `{"total":{"calories":500}}`); err != nil {
		t.Fatal(err)
	}
	if err = s.LinkEstimateReply(chat, id+1000, key, id); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestSaveDayTargetDuplicatesAndRevisions(t *testing.T) {
	s := diaryStore(t)
	at := time.Date(2026, 9, 10, 21, 0, 0, 0, time.UTC) // Sept 11 in Dubai.
	if err := s.SetTimezone(1, "Asia/Dubai"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTarget(1, 2000); err != nil {
		t.Fatal(err)
	}
	key := estimatedMeal(t, s, 1, 1, 10, "album", 500)
	if _, err := s.AddPhoto(1, 1, 11, "album", "second", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Save(1, 1, 11, at)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Added || got.Daily.Day != "2026-09-11" || got.Daily.Calories != 500 || got.Daily.Target != 2000 || got.Daily.Meals != 1 {
		t.Fatalf("bad save: %+v", got)
	}
	got, err = s.Save(1, 1, 1010, at.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Added || got.Changed || got.Daily.Day != "2026-09-11" || got.Daily.Meals != 1 {
		t.Fatalf("duplicate moved or doubled: %+v", got)
	}
	// A photo cannot silently save an obsolete estimate while a correction is pending.
	if _, _, err = s.Correct(1, 1, 1010, 20, "cabbage"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(1, 1, 10, at); !errors.Is(err, ErrNotAnalyzed) {
		t.Fatalf("pending save: %v", err)
	}
	if err = s.PutEstimate(1, 1, key, 20, 300, `{"total":{"calories":300}}`); err != nil {
		t.Fatal(err)
	}
	if err = s.LinkEstimateReply(1, 1020, key, 20); err != nil {
		t.Fatal(err)
	}
	got, err = s.Save(1, 1, 1020, at.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Added || !got.Changed || got.Daily.Calories != 300 || got.Daily.Day != "2026-09-11" {
		t.Fatalf("bad revision: %+v", got)
	}
	// Explicitly saving an older answer uses exactly that answer's estimate.
	got, err = s.Save(1, 1, 1010, at)
	if err != nil || got.Calories != 500 {
		t.Fatalf("wrong answer version: %+v %v", got, err)
	}
	got, err = s.Save(1, 1, 10, at)
	if err != nil || got.Calories != 300 {
		t.Fatalf("photo should use latest: %+v %v", got, err)
	}
	today, err := s.Today(1, at.Add(24*time.Hour))
	if err != nil || today.Calories != 0 {
		t.Fatalf("tomorrow: %+v %v", today, err)
	}
	// Other meals and chats count toward the same user's day.
	estimatedMeal(t, s, 2, 1, 10, "", 800)
	got, err = s.Save(2, 1, 10, at)
	if err != nil || got.Daily.Calories != 1100 || got.Daily.Meals != 2 {
		t.Fatalf("cross-chat sum: %+v %v", got, err)
	}
	if _, err = s.Save(1, 2, 10, at); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("wrong user accepted: %v", err)
	}
	if _, err = s.Save(3, 1, 10, at); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("wrong chat accepted: %v", err)
	}
}

func TestDiaryPersistenceAndTimezone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 11, 1, 0, 0, 0, time.UTC)
	if err = s.SetTimezone(1, "America/New_York"); err != nil {
		t.Fatal(err)
	}
	estimatedMeal(t, s, 1, 1, 1, "", 250)
	if _, err = s.Save(1, 1, 1, at); err != nil {
		t.Fatal(err)
	}
	if err = s.SetTarget(1, 1800); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Today(1, at)
	if err != nil {
		t.Fatal(err)
	}
	if got.Day != "2026-09-10" || got.Target != 1800 || got.Calories != 250 || got.Timezone != "America/New_York" {
		t.Fatalf("lost diary: %+v", got)
	}
	if err = s.SetTarget(1, 0); err != nil {
		t.Fatal(err)
	}
	got, err = s.Today(1, at)
	if err != nil || got.Target != 0 || got.Calories != 250 {
		t.Fatal("clearing target removed meals")
	}
	for _, zone := range []string{"Not/AZone", "Local", ""} {
		if s.SetTimezone(1, zone) == nil {
			t.Fatalf("accepted timezone %q", zone)
		}
	}
	for _, n := range []int{-1, 100001} {
		if s.SetTarget(1, n) == nil {
			t.Fatal("accepted invalid goal")
		}
	}
	// Default users use UTC, with no target and an empty total.
	empty, err := s.Today(2, at)
	if err != nil || empty.Day != "2026-09-11" || empty.Timezone != "UTC" || empty.Target != 0 || empty.Meals != 0 {
		t.Fatalf("bad default: %+v %v", empty, err)
	}
	// Day bucketing follows timezone rules across a DST transition.
	spring := time.Date(2026, 3, 8, 4, 30, 0, 0, time.UTC)
	before, _ := s.Today(1, spring)
	after, _ := s.Today(1, spring.Add(time.Hour))
	if before.Day != "2026-03-07" || after.Day != "2026-03-08" {
		t.Fatalf("DST days: %s %s", before.Day, after.Day)
	}
}

func TestConcurrentSavesAndUnavailableEstimates(t *testing.T) {
	s := diaryStore(t)
	at := time.Now()
	key, err := s.AddPhoto(1, 1, 1, "", "file", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(1, 1, 1, at); !errors.Is(err, ErrNotAnalyzed) {
		t.Fatal(err)
	}
	for _, n := range []float64{-1, math.NaN(), math.Inf(1)} {
		if s.PutEstimate(1, 1, key, 1, n, `{}`) == nil {
			t.Fatal("accepted invalid calories")
		}
	}
	if err = s.PutEstimate(1, 2, key, 1, 100, `{}`); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("accepted wrong owner")
	}
	if err = s.PutEstimate(1, 1, key, 1, 100, `{}`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Save(1, 1, 1, at); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := s.Today(1, at)
	if err != nil || got.Meals != 1 || got.Calories != 100 {
		t.Fatalf("double counted: %+v %v", got, err)
	}
}
