package store

import (
	"database/sql"
	"errors"
	"time"
)

// Recording is one row of tv_recordings (docs/tv.md).
type Recording struct {
	ID           string `json:"id"`
	UserID       int64  `json:"-"`
	ChannelID    string `json:"channel_id"`
	ChannelTitle string `json:"channel_title"`
	Title        string `json:"title"`
	StartAt      int64  `json:"start_at"`
	EndAt        int64  `json:"end_at"`
	// The programme's own end from the guide (unpadded) — what the TV matches
	// a guide row against to show ⏺ and to toggle.
	ProgramEnd int64 `json:"program_end"`
	State        string `json:"state"`
	Error        string `json:"error,omitempty"`
	Bytes        int64  `json:"bytes"`
	CreatedAt    int64  `json:"created_at"`
}

// Recording states.
const (
	RecScheduled = "scheduled"
	RecRecording = "recording"
	RecDone      = "done"
	RecFailed    = "failed"
)

// ErrRecordingNotFound is returned when an id matches nothing (or another
// profile's row).
var ErrRecordingNotFound = errors.New("store: recording not found")

// RecordingsRepo is the repository over tv_recordings.
type RecordingsRepo struct {
	db *sql.DB
}

const recCols = `id, user_id, channel_id, channel_title, title, start_at, end_at, state, error, bytes, created_at, program_end`

func scanRecording(row interface{ Scan(...any) error }) (Recording, error) {
	var r Recording
	err := row.Scan(&r.ID, &r.UserID, &r.ChannelID, &r.ChannelTitle, &r.Title,
		&r.StartAt, &r.EndAt, &r.State, &r.Error, &r.Bytes, &r.CreatedAt, &r.ProgramEnd)
	return r, err
}

func (r *RecordingsRepo) Add(rec Recording) error {
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().Unix()
	}
	_, err := r.db.Exec(
		`INSERT INTO tv_recordings (`+recCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.UserID, rec.ChannelID, rec.ChannelTitle, rec.Title,
		rec.StartAt, rec.EndAt, rec.State, rec.Error, rec.Bytes, rec.CreatedAt, rec.ProgramEnd)
	return err
}

// List returns one profile's recordings, newest programme first.
func (r *RecordingsRepo) List(userID int64) ([]Recording, error) {
	rows, err := r.db.Query(`SELECT `+recCols+` FROM tv_recordings WHERE user_id = ? ORDER BY start_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recording{}
	for rows.Next() {
		rec, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Due returns the rows the scheduler has to act on: everything not finished.
func (r *RecordingsRepo) Pending() ([]Recording, error) {
	rows, err := r.db.Query(
		`SELECT `+recCols+` FROM tv_recordings WHERE state IN (?, ?) ORDER BY start_at`,
		RecScheduled, RecRecording)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recording{}
	for rows.Next() {
		rec, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// FindPending returns this profile's not-yet-finished recording of the same
// programme (same channel, same guide end time).
func (r *RecordingsRepo) FindPending(userID int64, channelID string, programEnd int64) (Recording, bool, error) {
	rec, err := scanRecording(r.db.QueryRow(
		`SELECT `+recCols+` FROM tv_recordings WHERE user_id = ? AND channel_id = ? AND program_end = ? AND state IN (?, ?) LIMIT 1`,
		userID, channelID, programEnd, RecScheduled, RecRecording))
	if errors.Is(err, sql.ErrNoRows) {
		return Recording{}, false, nil
	}
	return rec, err == nil, err
}

// Get returns one row regardless of owner; callers that act on behalf of a
// profile compare UserID themselves (the media route has only the token).
func (r *RecordingsRepo) Get(id string) (Recording, error) {
	rec, err := scanRecording(r.db.QueryRow(`SELECT `+recCols+` FROM tv_recordings WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Recording{}, ErrRecordingNotFound
	}
	return rec, err
}

func (r *RecordingsRepo) SetState(id, state, errText string) error {
	_, err := r.db.Exec(`UPDATE tv_recordings SET state = ?, error = ? WHERE id = ?`, state, errText, id)
	return err
}

func (r *RecordingsRepo) SetBytes(id string, bytes int64) error {
	_, err := r.db.Exec(`UPDATE tv_recordings SET bytes = ? WHERE id = ?`, bytes, id)
	return err
}

func (r *RecordingsRepo) Delete(id string, userID int64) error {
	res, err := r.db.Exec(`DELETE FROM tv_recordings WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRecordingNotFound
	}
	return nil
}

// Oldest finished recordings first — what the retention sweep deletes.
func (r *RecordingsRepo) OldestDone() ([]Recording, error) {
	rows, err := r.db.Query(`SELECT `+recCols+` FROM tv_recordings WHERE state IN (?, ?) ORDER BY start_at`, RecDone, RecFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recording{}
	for rows.Next() {
		rec, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// TotalBytes is what every recording of every profile occupies.
func (r *RecordingsRepo) TotalBytes() (int64, error) {
	var n sql.NullInt64
	err := r.db.QueryRow(`SELECT SUM(bytes) FROM tv_recordings`).Scan(&n)
	return n.Int64, err
}

// DeleteByID drops a row whoever owns it (retention sweep).
func (r *RecordingsRepo) DeleteByID(id string) error {
	_, err := r.db.Exec(`DELETE FROM tv_recordings WHERE id = ?`, id)
	return err
}
