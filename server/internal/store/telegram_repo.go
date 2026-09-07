package store

import (
	"database/sql"
	"errors"
)

// TelegramRepo is the repository over telegram_links (chat_id -> user_id).
type TelegramRepo struct {
	db *sql.DB
}

// TelegramLink is one linked chat of a profile.
type TelegramLink struct {
	ChatID    int64  `json:"chat_id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
	CreatedAt int64  `json:"created_at"`
}

// Link binds chatID to userID, replacing any previous binding of that chat.
func (r *TelegramRepo) Link(chatID, userID int64, firstName, username string, now int64) error {
	_, err := r.db.Exec(
		`INSERT INTO telegram_links (chat_id, user_id, first_name, username, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (chat_id) DO UPDATE SET user_id = excluded.user_id, first_name = excluded.first_name,
		   username = excluded.username, created_at = excluded.created_at`,
		chatID, userID, firstName, username, now,
	)
	return err
}

// List returns every chat linked to userID, oldest first.
func (r *TelegramRepo) List(userID int64) ([]TelegramLink, error) {
	rows, err := r.db.Query(`SELECT chat_id, first_name, username, created_at FROM telegram_links WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TelegramLink
	for rows.Next() {
		var l TelegramLink
		if err := rows.Scan(&l.ChatID, &l.FirstName, &l.Username, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// UnlinkChatOfUser removes one chat, but only if it belongs to userID.
func (r *TelegramRepo) UnlinkChatOfUser(userID, chatID int64) error {
	_, err := r.db.Exec(`DELETE FROM telegram_links WHERE user_id = ? AND chat_id = ?`, userID, chatID)
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
