package stats

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPersistencePeriodsAndIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "stats.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	empty, err := s.Get(now)
	if err != nil || empty.All != (Period{}) {
		t.Fatalf("empty stats: %+v, %v", empty, err)
	}
	for i, age := range []time.Duration{0, 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour, 31 * 24 * time.Hour} {
		if err := s.Record(1, int64(i+1), i, "", "", now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	// An album has several photos but is one request, including redelivery.
	for i := 10; i < 13; i++ {
		if err := s.Record(2, 1, i, "album", "renamed", now); err != nil {
			t.Fatal(err)
		}
	}
	// Same Telegram message ID in a different chat must still count.
	if err := s.Record(3, 1, 0, "", "renamed", now); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(now)
	if err != nil {
		t.Fatal(err)
	}
	if got.All != (Period{7, 5}) || got.Day != (Period{4, 2}) || got.Week != (Period{5, 3}) || got.Month != (Period{6, 4}) {
		t.Fatalf("unexpected stats: %+v", got)
	}
	if !got.Since.Equal(empty.Since) {
		t.Fatal("tracking start changed after restart")
	}
	if len(got.Top) != 5 || got.Top[0] != (User{1, "renamed", 3}) {
		t.Fatalf("top users: %+v", got.Top)
	}
}

func TestConcurrentRecordingAndDeduplication(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Record(1, int64(i%20), i%20, "", "", now); err != nil {
				t.Error(err)
			}
			if _, err := s.Get(now); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := s.Get(now)
	if err != nil {
		t.Fatal(err)
	}
	if got.All != (Period{20, 20}) || len(got.Top) != 10 {
		t.Fatalf("unexpected stats: %+v", got)
	}
}
