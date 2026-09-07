package store

import (
	"database/sql"
	"errors"
)

// QueueItem is a row of the watch_queue table. Season/Episode are nil for a
// movie (or a whole series).
type QueueItem struct {
	ID        int64
	UserID    int64
	TMDBID    int64
	MediaType string
	Season    *int
	Episode   *int
	Position  int
	CreatedAt int64
}

// QueueRepo is the repository over watch_queue (docs/miniapp.md, watch queue).
type QueueRepo struct {
	db *sql.DB
}

const queueCols = `id, user_id, tmdb_id, media_type, season, episode, position, created_at`

func scanQueueItem(row interface{ Scan(...any) error }) (QueueItem, error) {
	var it QueueItem
	var season, episode sql.NullInt64
	if err := row.Scan(&it.ID, &it.UserID, &it.TMDBID, &it.MediaType, &season, &episode, &it.Position, &it.CreatedAt); err != nil {
		return QueueItem{}, err
	}
	if season.Valid {
		s := int(season.Int64)
		it.Season = &s
	}
	if episode.Valid {
		e := int(episode.Int64)
		it.Episode = &e
	}
	return it, nil
}

// List returns userID's queue, head first.
func (r *QueueRepo) List(userID int64) ([]QueueItem, error) {
	rows, err := r.db.Query(`SELECT `+queueCols+` FROM watch_queue WHERE user_id = ? ORDER BY position, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueueItem{}
	for rows.Next() {
		it, err := scanQueueItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Add appends an item (position = max+1). Re-adding the same
// (tmdb_id, media_type, season, episode) returns the existing row with
// created=false.
func (r *QueueRepo) Add(userID, tmdbID int64, mediaType string, season, episode *int, now int64) (QueueItem, bool, error) {
	existing := r.db.QueryRow(
		`SELECT `+queueCols+` FROM watch_queue
		 WHERE user_id = ? AND tmdb_id = ? AND media_type = ? AND season IS ? AND episode IS ?`,
		userID, tmdbID, mediaType, season, episode)
	if it, err := scanQueueItem(existing); err == nil {
		return it, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return QueueItem{}, false, err
	}
	res, err := r.db.Exec(
		`INSERT INTO watch_queue (user_id, tmdb_id, media_type, season, episode, position, created_at)
		 VALUES (?, ?, ?, ?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM watch_queue WHERE user_id = ?), ?)`,
		userID, tmdbID, mediaType, season, episode, userID, now)
	if err != nil {
		return QueueItem{}, false, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return QueueItem{}, false, err
	}
	it, err := scanQueueItem(r.db.QueryRow(`SELECT `+queueCols+` FROM watch_queue WHERE id = ?`, id))
	return it, true, err
}

// Remove deletes one item, scoped to userID. ErrNotFound when absent.
func (r *QueueRepo) Remove(userID, id int64) error {
	res, err := r.db.Exec(`DELETE FROM watch_queue WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	return checkRowsAffected(res)
}

// Move puts item id at index newIndex (clamped to 0..n-1) and renumbers the
// queue densely. ErrNotFound when the item is not in userID's queue.
// ponytail: rewrites every row; queues are a handful of items.
func (r *QueueRepo) Move(userID, id int64, newIndex int) error {
	items, err := r.List(userID)
	if err != nil {
		return err
	}
	from := -1
	for i := range items {
		if items[i].ID == id {
			from = i
		}
	}
	if from < 0 {
		return ErrNotFound
	}
	if newIndex < 0 {
		newIndex = 0
	}
	if newIndex > len(items)-1 {
		newIndex = len(items) - 1
	}
	moved := items[from]
	items = append(items[:from], items[from+1:]...)
	items = append(items[:newIndex], append([]QueueItem{moved}, items[newIndex:]...)...)

	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	for i := range items {
		if _, err := tx.Exec(`UPDATE watch_queue SET position = ? WHERE id = ?`, i, items[i].ID); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Clear empties userID's queue.
func (r *QueueRepo) Clear(userID int64) error {
	_, err := r.db.Exec(`DELETE FROM watch_queue WHERE user_id = ?`, userID)
	return err
}

// ClearUser is Clear under the Danger-zone naming (Service.ClearAll).
func (r *QueueRepo) ClearUser(userID int64) error { return r.Clear(userID) }

// Pop removes and returns the head; ok=false on an empty queue.
func (r *QueueRepo) Pop(userID int64) (QueueItem, bool, error) {
	it, err := scanQueueItem(r.db.QueryRow(`SELECT `+queueCols+` FROM watch_queue WHERE user_id = ? ORDER BY position, id LIMIT 1`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return QueueItem{}, false, nil
	}
	if err != nil {
		return QueueItem{}, false, err
	}
	if _, err := r.db.Exec(`DELETE FROM watch_queue WHERE id = ?`, it.ID); err != nil {
		return QueueItem{}, false, err
	}
	return it, true, nil
}
