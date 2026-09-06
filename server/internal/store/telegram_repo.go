package store

import (
	"database/sql"
	"errors"
)

// TelegramRepo is the repository over telegram_links (chat_id -> user_id).
type TelegramRepo struct {
	db *sql.DB
}

// Link binds chatID to userID, replacing any previous binding of that chat.
func (r *TelegramRepo) Link(chatID, userID, now int64) error {
	_, err := r.db.Exec(
		`INSERT INTO telegram_links (chat_id, user_id, created_at) VALUES (?, ?, ?)
		 ON CONFLICT (chat_id) DO UPDATE SET user_id = excluded.user_id, created_at = excluded.created_at`,
		chatID, userID, now,
	)
	return err
}

// UserByChat returns the profile linked to chatID, or ErrNotFound.
func (r *TelegramRepo) UserByChat(chatID int64) (int64, error) {
	var userID int64
	err := r.db.QueryRow(`SELECT user_id FROM telegram_links WHERE chat_id = ?`, chatID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return userID, err
}

// IsUserLinked reports whether userID has at least one linked chat.
func (r *TelegramRepo) IsUserLinked(userID int64) (bool, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM telegram_links WHERE user_id = ?`, userID).Scan(&n)
	return n > 0, err
}

// UnlinkChat removes chatID's binding (bot /unlink).
func (r *TelegramRepo) UnlinkChat(chatID int64) error {
	_, err := r.db.Exec(`DELETE FROM telegram_links WHERE chat_id = ?`, chatID)
	return err
}

// UnlinkUser removes every chat bound to userID (DELETE /api/v1/telegram/link).
func (r *TelegramRepo) UnlinkUser(userID int64) error {
	_, err := r.db.Exec(`DELETE FROM telegram_links WHERE user_id = ?`, userID)
	return err
}
