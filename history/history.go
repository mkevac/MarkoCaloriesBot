package history

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }
type Meal struct {
	FileIDs []string
	Prompt  string
}

// Open uses the existing stats database but keeps meal context in separate tables.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000;
 PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS meals (
 chat_id INTEGER NOT NULL, meal_key TEXT NOT NULL, user_id INTEGER NOT NULL,
 PRIMARY KEY(chat_id, meal_key));
 CREATE TABLE IF NOT EXISTS meal_photos (
 chat_id INTEGER NOT NULL, message_id INTEGER NOT NULL, meal_key TEXT NOT NULL,
 file_id TEXT NOT NULL, caption TEXT NOT NULL, PRIMARY KEY(chat_id,message_id));
 CREATE TABLE IF NOT EXISTS meal_replies (
 chat_id INTEGER NOT NULL, message_id INTEGER NOT NULL, meal_key TEXT NOT NULL,
 PRIMARY KEY(chat_id,message_id));
 CREATE TABLE IF NOT EXISTS meal_corrections (
 chat_id INTEGER NOT NULL, message_id INTEGER NOT NULL, meal_key TEXT NOT NULL,
 text TEXT NOT NULL, PRIMARY KEY(chat_id,message_id));
 CREATE INDEX IF NOT EXISTS meal_photos_lookup ON meal_photos(chat_id,meal_key);
 CREATE INDEX IF NOT EXISTS meal_corrections_lookup ON meal_corrections(chat_id,meal_key);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) AddPhoto(chatID, userID int64, messageID int, albumID, fileID, caption string) (string, error) {
	key := fmt.Sprintf("photo:%d", messageID)
	if albumID != "" {
		key = fmt.Sprintf("album:%d:%s", userID, albumID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO meals VALUES(?,?,?)`, chatID, key, userID); err != nil {
		return "", err
	}
	var owner int64
	if err = tx.QueryRow(`SELECT user_id FROM meals WHERE chat_id=? AND meal_key=?`, chatID, key).Scan(&owner); err != nil {
		return "", err
	}
	if owner != userID {
		return "", fmt.Errorf("meal belongs to another user")
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO meal_photos VALUES(?,?,?,?,?)`, chatID, messageID, key, fileID, caption); err != nil {
		return "", err
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO meal_replies VALUES(?,?,?)`, chatID, messageID, key); err != nil {
		return "", err
	}
	return key, tx.Commit()
}

// Correct resolves only this user's meal in this chat and deduplicates updates.
// A correction message itself becomes a valid target for further replies.
func (s *Store) Correct(chatID, userID int64, targetID, messageID int, text string) (key string, added bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`SELECT m.meal_key FROM meal_replies r JOIN meals m ON m.chat_id=r.chat_id AND m.meal_key=r.meal_key WHERE r.chat_id=? AND r.message_id=? AND m.user_id=?`, chatID, targetID, userID).Scan(&key)
	if err != nil {
		return "", false, err
	}
	result, err := tx.Exec(`INSERT OR IGNORE INTO meal_corrections VALUES(?,?,?,?)`, chatID, messageID, key, strings.TrimSpace(text))
	if err != nil {
		return "", false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return "", false, err
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO meal_replies VALUES(?,?,?)`, chatID, messageID, key); err != nil {
		return "", false, err
	}
	return key, count > 0, tx.Commit()
}

func (s *Store) LinkReply(chatID int64, messageID int, key string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO meal_replies VALUES(?,?,?)`, chatID, messageID, key)
	return err
}

// Load includes only corrections up to this request, so a later queued
// clarification cannot change an earlier request's prompt.
func (s *Store) Load(chatID, userID int64, key string, throughMessageID int) (Meal, error) {
	var meal Meal
	tx, err := s.db.Begin()
	if err != nil {
		return meal, err
	}
	defer tx.Rollback()
	var owner int64
	err = tx.QueryRow(`SELECT user_id FROM meals WHERE chat_id=? AND meal_key=?`, chatID, key).Scan(&owner)
	if err != nil {
		return meal, err
	}
	if owner != userID {
		return meal, sql.ErrNoRows
	}
	rows, err := tx.Query(`SELECT file_id,caption FROM meal_photos WHERE chat_id=? AND meal_key=? ORDER BY message_id`, chatID, key)
	if err != nil {
		return meal, err
	}
	var captions []string
	for rows.Next() {
		var id, caption string
		if err = rows.Scan(&id, &caption); err != nil {
			rows.Close()
			return meal, err
		}
		meal.FileIDs = append(meal.FileIDs, id)
		if caption != "" {
			captions = append(captions, caption)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return meal, err
	}
	if len(meal.FileIDs) == 0 {
		return meal, sql.ErrNoRows
	}
	meal.Prompt = strings.Join(captions, "\n")
	rows, err = tx.Query(`SELECT text FROM meal_corrections WHERE chat_id=? AND meal_key=? AND message_id<=? ORDER BY message_id`, chatID, key, throughMessageID)
	if err != nil {
		return meal, err
	}
	defer rows.Close()
	var corrections []string
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			return meal, err
		}
		corrections = append(corrections, text)
	}
	if len(corrections) > 0 {
		meal.Prompt = "Re-analyze the original meal photos using the user's clarifications below. Recalculate all foods and totals. When clarifications conflict, use the latest one.\n\nOriginal captions:\n" + meal.Prompt + "\n\nUser clarifications (oldest first):\n"
		for i, text := range corrections {
			meal.Prompt += fmt.Sprintf("%d. %s\n", i+1, text)
		}
	}
	return meal, rows.Err()
}
