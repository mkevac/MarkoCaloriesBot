package stats

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

type Period struct{ Requests, Users int64 }
type Stats struct {
	All, Day, Week, Month Period
	Since                 time.Time
	Top                   []User
}

type User struct {
	ID       int64
	Username string
	Requests int64
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection serializes concurrent Telegram handlers and keeps pragmas applied.
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout = 5000;
 PRAGMA journal_mode = WAL;
 CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
 INSERT OR IGNORE INTO metadata VALUES ('tracking_started', unixepoch());
 CREATE TABLE IF NOT EXISTS users (user_id INTEGER PRIMARY KEY, username TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS requests (
 chat_id INTEGER NOT NULL,
 request_key TEXT NOT NULL,
 user_id INTEGER NOT NULL,
 created_at INTEGER NOT NULL,
 PRIMARY KEY (chat_id, request_key)
 );
 CREATE INDEX IF NOT EXISTS requests_created_at ON requests(created_at);`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize stats: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Record counts submitted photos, deduplicating albums and Telegram redeliveries.
// User IDs remain stable when usernames change, and work without a username.
func (s *Store) Record(chatID, userID int64, messageID int, albumID, username string, at time.Time) error {
	key := fmt.Sprintf("message:%d", messageID)
	if albumID != "" {
		key = "album:" + albumID
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO users (user_id, username) VALUES (?, ?) ON CONFLICT(user_id) DO UPDATE SET username = excluded.username`, userID, username); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO requests (chat_id, request_key, user_id, created_at) VALUES (?, ?, ?, ?)`, chatID, key, userID, at.Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Get(now time.Time) (Stats, error) {
	var result Stats
	var since int64
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	// Keep the summary and top users in the same snapshot while handlers write.
	// Periods are rolling UTC durations.
	err = tx.QueryRow(`SELECT
 (SELECT value FROM metadata WHERE key = 'tracking_started'),
 COUNT(*), COUNT(DISTINCT user_id),
 COUNT(CASE WHEN created_at >= ? THEN 1 END), COUNT(DISTINCT CASE WHEN created_at >= ? THEN user_id END),
 COUNT(CASE WHEN created_at >= ? THEN 1 END), COUNT(DISTINCT CASE WHEN created_at >= ? THEN user_id END),
 COUNT(CASE WHEN created_at >= ? THEN 1 END), COUNT(DISTINCT CASE WHEN created_at >= ? THEN user_id END)
 FROM requests WHERE created_at <= ?`,
		now.Add(-24*time.Hour).Unix(), now.Add(-24*time.Hour).Unix(),
		now.Add(-7*24*time.Hour).Unix(), now.Add(-7*24*time.Hour).Unix(),
		now.Add(-30*24*time.Hour).Unix(), now.Add(-30*24*time.Hour).Unix(), now.Unix()).Scan(
		&since, &result.All.Requests, &result.All.Users,
		&result.Day.Requests, &result.Day.Users, &result.Week.Requests, &result.Week.Users, &result.Month.Requests, &result.Month.Users)
	result.Since = time.Unix(since, 0).UTC()
	if err != nil {
		return result, err
	}
	rows, err := tx.Query(`SELECT r.user_id, u.username, COUNT(*) AS total FROM requests r JOIN users u ON u.user_id = r.user_id WHERE r.created_at <= ? GROUP BY r.user_id, u.username ORDER BY total DESC, r.user_id LIMIT 10`, now.Unix())
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Username, &user.Requests); err != nil {
			return result, err
		}
		result.Top = append(result.Top, user)
	}
	return result, rows.Err()
}
