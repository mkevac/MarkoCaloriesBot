package history

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
	_ "time/tzdata"
)

var ErrNotAnalyzed = errors.New("meal has no completed estimate")

type Daily struct {
	Day, Timezone string
	Target        int
	Calories      float64
	Meals         int
}
type SaveResult struct {
	Daily          Daily
	Added, Changed bool
	Calories       float64
}

func (s *Store) initDiary() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS calorie_settings (
 user_id INTEGER PRIMARY KEY, target INTEGER NOT NULL DEFAULT 0, timezone TEXT NOT NULL DEFAULT 'UTC');
 CREATE TABLE IF NOT EXISTS meal_estimates (
 chat_id INTEGER NOT NULL, meal_key TEXT NOT NULL, request_id INTEGER NOT NULL,
 calories REAL NOT NULL, result_json TEXT NOT NULL, PRIMARY KEY(chat_id,meal_key,request_id));
 CREATE TABLE IF NOT EXISTS estimate_replies (
 chat_id INTEGER NOT NULL, message_id INTEGER NOT NULL, meal_key TEXT NOT NULL, request_id INTEGER NOT NULL,
 PRIMARY KEY(chat_id,message_id));
 CREATE TABLE IF NOT EXISTS saved_meals (
 user_id INTEGER NOT NULL, chat_id INTEGER NOT NULL, meal_key TEXT NOT NULL,
 day TEXT NOT NULL, timezone TEXT NOT NULL, request_id INTEGER NOT NULL,
 calories REAL NOT NULL, result_json TEXT NOT NULL,
 PRIMARY KEY(chat_id,meal_key));
 CREATE INDEX IF NOT EXISTS saved_meals_day ON saved_meals(user_id,day);`)
	return err
}

func (s *Store) SetTarget(userID int64, target int) error {
	if target < 0 || target > 100000 {
		return fmt.Errorf("target must be between 0 and 100000")
	}
	_, err := s.db.Exec(`INSERT INTO calorie_settings(user_id,target) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET target=excluded.target`, userID, target)
	return err
}
func (s *Store) SetTimezone(userID int64, zone string) error {
	if zone == "Local" {
		return fmt.Errorf("use an IANA timezone")
	}
	if _, err := time.LoadLocation(zone); err != nil || zone == "" {
		return fmt.Errorf("unknown timezone")
	}
	_, err := s.db.Exec(`INSERT INTO calorie_settings(user_id,timezone) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET timezone=excluded.timezone`, userID, zone)
	return err
}

func settings(tx *sql.Tx, userID int64) (target int, zone string, err error) {
	zone = "UTC"
	err = tx.QueryRow(`SELECT target,timezone FROM calorie_settings WHERE user_id=?`, userID).Scan(&target, &zone)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}
func daily(tx *sql.Tx, userID int64, day, zone string, target int) (Daily, error) {
	result := Daily{Day: day, Timezone: zone, Target: target}
	err := tx.QueryRow(`SELECT COALESCE(SUM(calories),0),COUNT(*) FROM saved_meals WHERE user_id=? AND day=?`, userID, day).Scan(&result.Calories, &result.Meals)
	return result, err
}
func (s *Store) Today(userID int64, at time.Time) (Daily, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Daily{}, err
	}
	defer tx.Rollback()
	target, zone, err := settings(tx, userID)
	if err != nil {
		return Daily{}, err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return Daily{}, err
	}
	return daily(tx, userID, at.In(loc).Format("2006-01-02"), zone, target)
}

func (s *Store) PutEstimate(chatID, userID int64, key string, requestID int, calories float64, resultJSON string) error {
	if math.IsNaN(calories) || math.IsInf(calories, 0) || calories < 0 {
		return fmt.Errorf("invalid calorie estimate")
	}
	result, err := s.db.Exec(`INSERT INTO meal_estimates(chat_id,meal_key,request_id,calories,result_json)
 SELECT chat_id,meal_key,?,?,? FROM meals WHERE chat_id=? AND meal_key=? AND user_id=?
 ON CONFLICT(chat_id,meal_key,request_id) DO NOTHING`, requestID, calories, resultJSON, chatID, key, userID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var exists int
		err = s.db.QueryRow(`SELECT 1 FROM meal_estimates e JOIN meals m ON e.chat_id=m.chat_id AND e.meal_key=m.meal_key WHERE e.chat_id=? AND e.meal_key=? AND e.request_id=? AND m.user_id=?`, chatID, key, requestID, userID).Scan(&exists)
	}
	return err
}
func (s *Store) LinkEstimateReply(chatID int64, messageID int, key string, requestID int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO meal_replies VALUES(?,?,?)`, chatID, messageID, key); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO estimate_replies VALUES(?,?,?,?)`, chatID, messageID, key, requestID); err != nil {
		return err
	}
	return tx.Commit()
}

// Save uses the shown estimate for a bot reply, or the latest completed estimate
// for an original photo. Resaving replaces the same meal on its original day.
func (s *Store) Save(chatID, userID int64, targetID int, at time.Time) (SaveResult, error) {
	var result SaveResult
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var key string
	err = tx.QueryRow(`SELECT m.meal_key FROM meal_replies r JOIN meals m ON m.chat_id=r.chat_id AND m.meal_key=r.meal_key WHERE r.chat_id=? AND r.message_id=? AND m.user_id=?`, chatID, targetID, userID).Scan(&key)
	if err != nil {
		return result, err
	}
	var requestID int
	err = tx.QueryRow(`SELECT request_id FROM estimate_replies WHERE chat_id=? AND message_id=?`, chatID, targetID).Scan(&requestID)
	if errors.Is(err, sql.ErrNoRows) {
		var photo int
		err = tx.QueryRow(`SELECT 1 FROM meal_photos WHERE chat_id=? AND message_id=?`, chatID, targetID).Scan(&photo)
		if errors.Is(err, sql.ErrNoRows) {
			return result, ErrNotAnalyzed
		}
		if err != nil {
			return result, err
		}
		err = tx.QueryRow(`SELECT request_id FROM meal_estimates WHERE chat_id=? AND meal_key=? ORDER BY request_id DESC LIMIT 1`, chatID, key).Scan(&requestID)
		if errors.Is(err, sql.ErrNoRows) {
			return result, ErrNotAnalyzed
		}
		if err != nil {
			return result, err
		}
		var pending int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM meal_corrections WHERE chat_id=? AND meal_key=? AND message_id>?`, chatID, key, requestID).Scan(&pending); err != nil {
			return result, err
		}
		if pending > 0 {
			return result, ErrNotAnalyzed
		}
	} else if err != nil {
		return result, err
	}
	var calories float64
	var resultJSON string
	err = tx.QueryRow(`SELECT calories,result_json FROM meal_estimates WHERE chat_id=? AND meal_key=? AND request_id=?`, chatID, key, requestID).Scan(&calories, &resultJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotAnalyzed
	}
	if err != nil {
		return result, err
	}
	target, zone, err := settings(tx, userID)
	if err != nil {
		return result, err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return result, err
	}
	day := at.In(loc).Format("2006-01-02")
	var oldRequest int
	err = tx.QueryRow(`SELECT day,timezone,request_id FROM saved_meals WHERE chat_id=? AND meal_key=?`, chatID, key).Scan(&day, &zone, &oldRequest)
	if errors.Is(err, sql.ErrNoRows) {
		result.Added = true
	} else if err != nil {
		return result, err
	}
	result.Changed = result.Added || oldRequest != requestID
	if result.Changed {
		_, err = tx.Exec(`INSERT INTO saved_meals VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(chat_id,meal_key) DO UPDATE SET request_id=excluded.request_id,calories=excluded.calories,result_json=excluded.result_json`, userID, chatID, key, day, zone, requestID, calories, resultJSON)
		if err != nil {
			return result, err
		}
	}
	result.Calories = calories
	result.Daily, err = daily(tx, userID, day, zone, target)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
