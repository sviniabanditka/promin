package store

import (
	"database/sql"
	"errors"
)

// ErrNotFound is returned by repository lookups that find no row.
var ErrNotFound = errors.New("store: not found")

// TMDBCacheEntry is a row of tmdb_cache: a normalized DTO JSON blob keyed
// by request identity (endpoint, language, page, ...) with a TTL.
type TMDBCacheEntry struct {
	Key       string
	JSON      string
	ExpiresAt int64
}

// TMDBCacheRepo is the repository over the tmdb_cache table (see
// docs/data-model.md).
type TMDBCacheRepo struct {
	db *sql.DB
}

// Get returns the cache entry for key, or ErrNotFound if absent. Callers
// compare ExpiresAt against time.Now() themselves to decide freshness —
// this repo happily returns stale rows so callers can serve
// stale-while-error (see docs/backend.md).
func (r *TMDBCacheRepo) Get(key string) (TMDBCacheEntry, error) {
	var e TMDBCacheEntry
	err := r.db.QueryRow(`SELECT key, json, expires_at FROM tmdb_cache WHERE key = ?`, key).
		Scan(&e.Key, &e.JSON, &e.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TMDBCacheEntry{}, ErrNotFound
	}
	if err != nil {
		return TMDBCacheEntry{}, err
	}
	return e, nil
}

// PruneExpiredLists deletes expired list-shaped entries (search/discover/
// trending pages — regenerable, and search creates one row per query string).
// Detail rows are kept on purpose: they back stale-ok reads (recommendations,
// continue-watching cards) and the whole "local library" idea. Without this
// the table only ever grew, and the nightly VACUUM INTO copied all of it.
func (r *TMDBCacheRepo) PruneExpiredLists(now int64) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM tmdb_cache WHERE expires_at < ? AND key LIKE 'list:%'`, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Set upserts a cache entry.
func (r *TMDBCacheRepo) Set(key, json string, expiresAt int64) error {
	_, err := r.db.Exec(`
		INSERT INTO tmdb_cache (key, json, expires_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET json = excluded.json, expires_at = excluded.expires_at
	`, key, json, expiresAt)
	return err
}
