package store

import "database/sql"

// Reaction is a row of the reactions table: a like (1) or dislike (-1).
type Reaction struct {
	UserID    int64
	TMDBID    int64
	MediaType string
	Value     int
	UpdatedAt int64
}

// ReactionsRepo is the repository over the reactions table.
type ReactionsRepo struct {
	db *sql.DB
}

// List returns userID's reactions, most recent first.
func (r *ReactionsRepo) List(userID int64) ([]Reaction, error) {
	rows, err := r.db.Query(
		`SELECT user_id, tmdb_id, media_type, value, updated_at FROM reactions
		 WHERE user_id = ? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reaction
	for rows.Next() {
		var x Reaction
		if err := rows.Scan(&x.UserID, &x.TMDBID, &x.MediaType, &x.Value, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Set upserts the reaction; value 0 removes it.
func (r *ReactionsRepo) Set(userID, tmdbID int64, mediaType string, value int, now int64) error {
	if value == 0 {
		_, err := r.db.Exec(`DELETE FROM reactions WHERE user_id = ? AND tmdb_id = ? AND media_type = ?`, userID, tmdbID, mediaType)
		return err
	}
	_, err := r.db.Exec(
		`INSERT INTO reactions (user_id, tmdb_id, media_type, value, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, tmdb_id, media_type) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		userID, tmdbID, mediaType, value, now)
	return err
}

func (r *ReactionsRepo) ClearUser(userID int64) error {
	_, err := r.db.Exec(`DELETE FROM reactions WHERE user_id = ?`, userID)
	return err
}
