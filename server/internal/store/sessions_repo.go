package store

import (
	"database/sql"
	"errors"
)

// Session is a row of the sessions table: doubles as "session" and
// "device" per docs/data-model.md
type Session struct {
	// Token is hex(sha256(bearer)) — the bearer itself is never stored, so a
	// database read or a backup cannot yield a usable session.
	Token string
	// ID is the public device identifier: the first 12 characters of the raw
	// bearer (auth.TokenID), which the TV computes on its side as well.
	ID         string
	UserID     int64
	DeviceName string
	DeviceType string
	CreatedAt  int64
	LastSeen   int64
}

// SessionsRepo is the repository over the sessions table.
type SessionsRepo struct {
	db *sql.DB
}

// Create inserts a new session row (issued by auth.Service.Login/Register).
func (r *SessionsRepo) Create(s Session) error {
	_, err := r.db.Exec(
		`INSERT INTO sessions (token, token_id, user_id, device_name, device_type, created_at, last_seen)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.Token, s.ID, s.UserID, s.DeviceName, s.DeviceType, s.CreatedAt, s.LastSeen,
	)
	return err
}

// Get returns the session for token, or ErrNotFound.
func (r *SessionsRepo) Get(token string) (Session, error) {
	var s Session
	err := r.db.QueryRow(
		`SELECT token, token_id, user_id, device_name, device_type, created_at, last_seen FROM sessions WHERE token = ?`,
		token,
	).Scan(&s.Token, &s.ID, &s.UserID, &s.DeviceName, &s.DeviceType, &s.CreatedAt, &s.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	return s, nil
}

// TouchLastSeen updates last_seen for token. Callers throttle how often
// this is called (docs/backend.md: "не чаще раза в 5 минут на
// устройство") — this repo just does the write.
func (r *SessionsRepo) TouchLastSeen(token string, ts int64) error {
	_, err := r.db.Exec(`UPDATE sessions SET last_seen = ? WHERE token = ?`, ts, token)
	return err
}

// ListByUser returns all sessions (devices) for userID, most recently
// active first.
func (r *SessionsRepo) ListByUser(userID int64) ([]Session, error) {
	rows, err := r.db.Query(
		`SELECT token, token_id, user_id, device_name, device_type, created_at, last_seen
		 FROM sessions WHERE user_id = ? ORDER BY last_seen DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.Token, &s.ID, &s.UserID, &s.DeviceName, &s.DeviceType, &s.CreatedAt, &s.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Delete removes a session (device revocation / logout).
func (r *SessionsRepo) Delete(token string) error {
	_, err := r.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// DeleteByUserType removes every session of one device type for a user
// (Telegram unlink → the phone's Mini App sessions die with the link).
func (r *SessionsRepo) DeleteByUserType(userID int64, deviceType string) error {
	_, err := r.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND device_type = ?`, userID, deviceType)
	return err
}

// HashLegacy converts rows written before 0010 (raw bearer in `token`, empty
// token_id) to the hashed form. Idempotent; returns how many rows changed.
func (r *SessionsRepo) HashLegacy(hash, id func(raw string) string) (int, error) {
	rows, err := r.db.Query(`SELECT token FROM sessions WHERE token_id = ''`)
	if err != nil {
		return 0, err
	}
	var raws []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return 0, err
		}
		raws = append(raws, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, raw := range raws {
		if _, err := r.db.Exec(`UPDATE sessions SET token = ?, token_id = ? WHERE token = ?`, hash(raw), id(raw), raw); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
