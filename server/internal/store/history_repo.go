package store

import "database/sql"

// HistoryEntry is a row of the history table. Season/Episode are nil for
// movies (docs/data-model.md: "NULL для фильмов").
type HistoryEntry struct {
	ID        int64
	UserID    int64
	TMDBID    int64
	MediaType string
	Season    *int
	Episode   *int
	WatchedAt int64
}

// HistoryRepo is the repository over the append-only history table.
type HistoryRepo struct {
	db *sql.DB
}

// Add appends a history entry. History is a log, not state — never
// updated/merged (docs/data-model.md).
func (r *HistoryRepo) Add(e HistoryEntry) (HistoryEntry, error) {
	res, err := r.db.Exec(
		`INSERT INTO history (user_id, tmdb_id, media_type, season, episode, watched_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.UserID, e.TMDBID, e.MediaType, e.Season, e.Episode, e.WatchedAt,
	)
	if err != nil {
		return HistoryEntry{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return HistoryEntry{}, err
	}
	e.ID = id
	return e, nil
}

// WatchedTmdbIDs returns the distinct tmdb ids the user has any relationship
// with — watched (history) or favourited (bookmarks) — as the exclude set for
// recommendation shelves (don't recommend what they already know). One indexed
// UNION, cheap at tens of users.
func (r *HistoryRepo) WatchedTmdbIDs(userID int64) ([]int64, error) {
	rows, err := r.db.Query(
		`SELECT tmdb_id FROM history   WHERE user_id = ?
		 UNION
		 SELECT tmdb_id FROM bookmarks WHERE user_id = ?`,
		userID, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// LibraryRef is one title the household actually cares about — favourited,
// part-watched, or recently played — regardless of which profile owns it.
type LibraryRef struct {
	TMDBID    int64
	MediaType string
}

// LibraryRefs returns the distinct titles across ALL profiles' bookmarks,
// timecodes and recent history, most-recently-touched first. Used by the
// background prewarm job to make sure the household's own library is always
// present locally (fast, and unaffected by a TMDB outage).
func (r *HistoryRepo) LibraryRefs(limit int) ([]LibraryRef, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := r.db.Query(
		`SELECT tmdb_id, media_type, MAX(touched) AS touched FROM (
		     SELECT tmdb_id, media_type, added_at   AS touched FROM bookmarks
		     UNION ALL
		     SELECT tmdb_id, media_type, updated_at AS touched FROM timecodes
		     UNION ALL
		     SELECT tmdb_id, media_type, watched_at AS touched FROM history
		 )
		 GROUP BY tmdb_id, media_type
		 ORDER BY touched DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LibraryRef
	for rows.Next() {
		var ref LibraryRef
		var touched int64
		if err := rows.Scan(&ref.TMDBID, &ref.MediaType, &touched); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// List returns userID's history, most recently watched first, paginated.
func (r *HistoryRepo) List(userID int64, limit, offset int) ([]HistoryEntry, error) {
	rows, err := r.db.Query(
		`SELECT id, user_id, tmdb_id, media_type, season, episode, watched_at
		 FROM history WHERE user_id = ? ORDER BY watched_at DESC LIMIT ? OFFSET ?`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.TMDBID, &e.MediaType, &e.Season, &e.Episode, &e.WatchedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
