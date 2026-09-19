package store

import (
	"database/sql"
	"time"
)

// SkipSegment is one learned intro/recap stretch of a season.
type SkipSegment struct {
	ID       int64
	StartSec float64
	EndSec   float64
	Votes    int
}

// SkipsRepo is the repository over skip_segments (docs/player.md, "Skip intro").
type SkipsRepo struct {
	db *sql.DB
}

// skipTolerance: two jumps belong to the same intro when both ends are within
// this many seconds. Viewers release the seek key at slightly different spots;
// 15 s is wider than that jitter and narrower than a real second segment.
const skipTolerance = 15.0

// Observe records one manual forward jump. It joins the closest matching
// segment of that season (averaging the bounds, weighted by the votes it
// already has) or starts a new one. An episode that already voted for a
// segment only nudges its bounds — it does not vote twice.
func (r *SkipsRepo) Observe(tmdbID int64, season, episode int, start, end float64) error {
	rows, err := r.db.Query(
		`SELECT id, start_sec, end_sec, votes, last_episode FROM skip_segments WHERE tmdb_id = ? AND season = ?`,
		tmdbID, season)
	if err != nil {
		return err
	}
	type cand struct {
		id          int64
		start, end  float64
		votes       int
		lastEpisode int
		dist        float64
	}
	var best *cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.start, &c.end, &c.votes, &c.lastEpisode); err != nil {
			rows.Close()
			return err
		}
		ds, de := abs(c.start-start), abs(c.end-end)
		if ds > skipTolerance || de > skipTolerance {
			continue
		}
		c.dist = ds + de
		if best == nil || c.dist < best.dist {
			cp := c
			best = &cp
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now().Unix()
	if best == nil {
		_, err = r.db.Exec(
			`INSERT INTO skip_segments (tmdb_id, season, start_sec, end_sec, votes, last_episode, updated_at)
			 VALUES (?, ?, ?, ?, 1, ?, ?)`,
			tmdbID, season, start, end, episode, now)
		return err
	}

	w := float64(best.votes)
	newStart := (best.start*w + start) / (w + 1)
	newEnd := (best.end*w + end) / (w + 1)
	votes := best.votes
	if episode != best.lastEpisode {
		votes++
	}
	_, err = r.db.Exec(
		`UPDATE skip_segments SET start_sec = ?, end_sec = ?, votes = ?, last_episode = ?, updated_at = ? WHERE id = ?`,
		newStart, newEnd, votes, episode, now, best.id)
	return err
}

// List returns the season's segments that at least minVotes episodes agreed on.
func (r *SkipsRepo) List(tmdbID int64, season, minVotes int) ([]SkipSegment, error) {
	rows, err := r.db.Query(
		`SELECT id, start_sec, end_sec, votes FROM skip_segments
		 WHERE tmdb_id = ? AND season = ? AND votes >= ? ORDER BY start_sec`,
		tmdbID, season, minVotes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SkipSegment{}
	for rows.Next() {
		var s SkipSegment
		if err := rows.Scan(&s.ID, &s.StartSec, &s.EndSec, &s.Votes); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
