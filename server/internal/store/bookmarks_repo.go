package store

import "database/sql"

// Bookmark is a row of the bookmarks table.
type Bookmark struct {
	UserID    int64
	TMDBID    int64
	MediaType string
	AddedAt   int64
}

// BookmarksRepo is the repository over the bookmarks table.
type BookmarksRepo struct {
	db *sql.DB
}

// List returns userID's bookmarks, most recently added first.
func (r *BookmarksRepo) List(userID int64) ([]Bookmark, error) {
	rows, err := r.db.Query(
		`SELECT user_id, tmdb_id, media_type, added_at FROM bookmarks
		 WHERE user_id = ? ORDER BY added_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Bookmark
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.UserID, &b.TMDBID, &b.MediaType, &b.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Add idempotently inserts a bookmark (INSERT OR IGNORE on the composite
// PK, per docs/data-model.md). Returns created=false if it already
// existed.
func (r *BookmarksRepo) Add(userID, tmdbID int64, mediaType string, addedAt int64) (created bool, err error) {
	res, err := r.db.Exec(
		`INSERT OR IGNORE INTO bookmarks (user_id, tmdb_id, media_type, added_at) VALUES (?, ?, ?, ?)`,
		userID, tmdbID, mediaType, addedAt,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Remove deletes a bookmark. Not an error if it didn't exist.
func (r *BookmarksRepo) Remove(userID, tmdbID int64, mediaType string) error {
	_, err := r.db.Exec(
		`DELETE FROM bookmarks WHERE user_id = ? AND tmdb_id = ? AND media_type = ?`,
		userID, tmdbID, mediaType,
	)
	return err
}

// Exists reports whether (userID, tmdbID, mediaType) is bookmarked. Used
// by the catalog normalizer to fill in Title.InBookmarks.
func (r *BookmarksRepo) Exists(userID, tmdbID int64, mediaType string) (bool, error) {
	var n int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM bookmarks WHERE user_id = ? AND tmdb_id = ? AND media_type = ?`,
		userID, tmdbID, mediaType,
	).Scan(&n)
	return n > 0, err
}
