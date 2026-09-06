package store

import "database/sql"

// Setting is a row of the user_settings table.
type Setting struct {
	UserID    int64
	Key       string
	Value     string
	UpdatedAt int64
}

// SettingsRepo is the repository over the user_settings key/value table.
type SettingsRepo struct {
	db *sql.DB
}

// GetAll returns all settings for userID as a key -> value map.
func (r *SettingsRepo) GetAll(userID int64) (map[string]string, error) {
	rows, err := r.db.Query(`SELECT key, value FROM user_settings WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Set upserts a single setting.
func (r *SettingsRepo) Set(userID int64, key, value string, now int64) error {
	_, err := r.db.Exec(`
		INSERT INTO user_settings (user_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, userID, key, value, now)
	return err
}
