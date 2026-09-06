package store

import (
	"database/sql"
	"errors"
	"time"
)

// Timecode is a row of the timecodes table: "where I stopped" for a
// (user, title, season, episode). For movies season=0, episode=0 by
// convention (docs/data-model.md).
type Timecode struct {
	UserID      int64
	TMDBID      int64
	MediaType   string
	Season      int
	Episode     int
	PositionSec float64
	DurationSec float64
	UpdatedAt   int64
}

// TimecodesRepo is the repository over the timecodes table.
type TimecodesRepo struct {
	db *sql.DB
}

// Get returns the timecode for (userID, tmdbID, season, episode), or
// ErrNotFound. mediaType is optional (the PK doesn't include it, per
// docs/data-model.md, since a single (season, episode) pair
// already disambiguates a live row in practice); when non-empty it's
// applied as an extra filter so a caller that does know the type can
// avoid an accidental cross-type match.
func (r *TimecodesRepo) Get(userID, tmdbID int64, mediaType string, season, episode int) (Timecode, error) {
	query := `SELECT user_id, tmdb_id, media_type, season, episode, position_sec, duration_sec, updated_at
		 FROM timecodes WHERE user_id = ? AND tmdb_id = ? AND season = ? AND episode = ?`
	args := []any{userID, tmdbID, season, episode}
	if mediaType != "" {
		query += " AND media_type = ?"
		args = append(args, mediaType)
	}

	var t Timecode
	err := r.db.QueryRow(query, args...).
		Scan(&t.UserID, &t.TMDBID, &t.MediaType, &t.Season, &t.Episode, &t.PositionSec, &t.DurationSec, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Timecode{}, ErrNotFound
	}
	return t, err
}

// Upsert writes t using the last-write-wins UPSERT from
// docs/data-model.md (WHERE excluded.updated_at >=
// timecodes.updated_at). It returns the row now stored in the database
// (which is t itself if the write won, or the pre-existing row if t lost
// the conflict) plus whether t's write was accepted.
func (r *TimecodesRepo) Upsert(t Timecode) (current Timecode, accepted bool, err error) {
	mediaType := t.MediaType
	if mediaType == "" {
		mediaType = "movie"
	}
	// RETURNING yields the row only when the write actually happened (a
	// conflict rejected by the WHERE returns nothing), so the common accepted
	// case is ONE statement and atomic — the old Exec+Get pair let another
	// device's upsert land in between and misreport `accepted`.
	var got Timecode
	err = r.db.QueryRow(`
		INSERT INTO timecodes (user_id, tmdb_id, media_type, season, episode, position_sec, duration_sec, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, tmdb_id, season, episode)
		DO UPDATE SET
			media_type   = excluded.media_type,
			position_sec = excluded.position_sec,
			duration_sec = excluded.duration_sec,
			updated_at   = excluded.updated_at
		WHERE excluded.updated_at >= timecodes.updated_at
		RETURNING user_id, tmdb_id, media_type, season, episode, position_sec, duration_sec, updated_at
	`, t.UserID, t.TMDBID, mediaType, t.Season, t.Episode, t.PositionSec, t.DurationSec, t.UpdatedAt).
		Scan(&got.UserID, &got.TMDBID, &got.MediaType, &got.Season, &got.Episode, &got.PositionSec, &got.DurationSec, &got.UpdatedAt)
	if err == nil {
		return got, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Timecode{}, false, err
	}
	// Lost the last-write-wins race: report the row that won.
	current, err = r.Get(t.UserID, t.TMDBID, "", t.Season, t.Episode)
	if err != nil {
		return Timecode{}, false, err
	}
	return current, false, nil
}

// ListRecent returns the userID's most recently updated timecodes, for
// GET /api/v1/sync/bootstrap (docs/api.md).
// LatestForTitle returns the user's most recently updated timecode on a title
// (any episode), or ok=false. The title screen attaches it to the card so
// "Continue" works even when the client's local timecode cache has evicted
// the record.
func (r *TimecodesRepo) LatestForTitle(userID, tmdbID int64, mediaType string) (Timecode, bool, error) {
	rows, err := r.queryTimecodes(
		`SELECT user_id, tmdb_id, media_type, season, episode, position_sec, duration_sec, updated_at
		 FROM timecodes WHERE user_id = ? AND tmdb_id = ? AND media_type = ?
		 ORDER BY updated_at DESC LIMIT 1`,
		userID, tmdbID, mediaType,
	)
	if err != nil || len(rows) == 0 {
		return Timecode{}, false, err
	}
	return rows[0], true, nil
}

func (r *TimecodesRepo) ListRecent(userID int64, limit int) ([]Timecode, error) {
	return r.queryTimecodes(
		`SELECT user_id, tmdb_id, media_type, season, episode, position_sec, duration_sec, updated_at
		 FROM timecodes WHERE user_id = ? ORDER BY updated_at DESC LIMIT ?`,
		userID, limit,
	)
}

// ListContinueWatching returns the userID's most recently updated
// timecodes that aren't finished (position < 90% of duration), for the
// "continue watching" home-page shelf (docs/backend.md task brief,
// item 5).
//
// One row per title — the most recently updated one — so a series with two
// half-watched episodes doesn't appear twice. A series whose latest episode is
// FINISHED stays (for 30 days): the client's resume point moves on to the next
// episode; a finished movie leaves the shelf.
func (r *TimecodesRepo) ListContinueWatching(userID int64, limit int) ([]Timecode, error) {
	since := time.Now().Add(-30 * 24 * time.Hour).Unix()
	return r.queryTimecodes(
		`SELECT t.user_id, t.tmdb_id, t.media_type, t.season, t.episode, t.position_sec, t.duration_sec, t.updated_at
		 FROM timecodes t
		 JOIN (SELECT tmdb_id, media_type, MAX(updated_at) AS mu FROM timecodes WHERE user_id = ? GROUP BY tmdb_id, media_type) l
		   ON l.tmdb_id = t.tmdb_id AND l.media_type = t.media_type AND l.mu = t.updated_at
		 WHERE t.user_id = ? AND t.duration_sec > 0
		   AND (t.position_sec < t.duration_sec * 0.9 OR (t.media_type = 'tv' AND t.updated_at > ?))
		 ORDER BY t.updated_at DESC LIMIT ?`,
		userID, userID, since, limit,
	)
}

// ListFinished returns the user's FINISHED titles (position >= 90% of
// duration) as recommendation seeds, deduped to one row per (tmdb_id,
// media_type) and ordered by most-recently-finished. Mirror of
// ListContinueWatching with the completion predicate flipped. Only tmdb_id +
// media_type matter to the recommender; the aggregated season/episode/pos are
// arbitrary-but-valid so queryTimecodes can scan the same 8 columns.
func (r *TimecodesRepo) ListFinished(userID int64, limit int) ([]Timecode, error) {
	return r.queryTimecodes(
		`SELECT user_id, tmdb_id, media_type,
		        MAX(season), MAX(episode), MAX(position_sec), MAX(duration_sec), MAX(updated_at)
		 FROM timecodes
		 WHERE user_id = ? AND duration_sec > 0 AND position_sec >= duration_sec * 0.9
		 GROUP BY tmdb_id, media_type
		 ORDER BY MAX(updated_at) DESC LIMIT ?`,
		userID, limit,
	)
}

func (r *TimecodesRepo) queryTimecodes(query string, args ...any) ([]Timecode, error) {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Timecode
	for rows.Next() {
		var t Timecode
		if err := rows.Scan(&t.UserID, &t.TMDBID, &t.MediaType, &t.Season, &t.Episode, &t.PositionSec, &t.DurationSec, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
