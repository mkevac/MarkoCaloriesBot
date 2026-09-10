package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mkevac/markocaloriesbot/stats"
)

func TestFormatStats(t *testing.T) {
	now := time.Now().UTC()
	empty := formatStats(stats.Stats{Since: now}, now)
	if strings.Contains(empty, "NaN") || strings.Contains(empty, "Inf") || !strings.Contains(empty, "0 users, 0 requests") {
		t.Fatal(empty)
	}
	got := formatStats(stats.Stats{Since: now.Add(-48 * time.Hour), All: stats.Period{Requests: 6, Users: 2}, Top: []stats.User{{ID: 42, Requests: 3}}}, now)
	for _, want := range []string{"Average requests/day since tracking began: 3.0", "Average requests/user: 3.0", "User 42: 3 requests"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}
