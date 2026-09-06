package store

import "database/sql"

// TorrentCacheEntry is a row of torrent_cache_meta: bookkeeping for the
// on-disk torrent LRU cache (docs/data-model.md/4). The actual
// downloaded data lives FLAT under PROMIN_DATA_DIR/torrents/<name> (anacrolix
// stores by torrent name, not infohash); this table tracks size/last_access +
// name so the eviction worker can pick candidates and delete their files.
type TorrentCacheEntry struct {
	InfoHash   string
	Name       string
	Size       int64
	LastAccess int64
}

// TorrentCacheRepo is the repository over torrent_cache_meta.
type TorrentCacheRepo struct {
	db *sql.DB
}

// Upsert inserts or updates the row for infohash. Used both when a magnet
// is first added (name/size may still be 0 until metadata/pieces arrive)
// and by the periodic size/last_access refresh.
func (r *TorrentCacheRepo) Upsert(infohash, name string, size, lastAccess int64) error {
	_, err := r.db.Exec(`
		INSERT INTO torrent_cache_meta (infohash, name, size, last_access) VALUES (?, ?, ?, ?)
		ON CONFLICT (infohash) DO UPDATE SET
			name = excluded.name,
			size = excluded.size,
			last_access = excluded.last_access
	`, infohash, name, size, lastAccess)
	return err
}

// TouchAccess updates only last_access, per docs/data-model.md
// ("не чаще раза в минуту на инфохэш") — callers are expected to
// rate-limit calls themselves (see internal/torrent).
func (r *TorrentCacheRepo) TouchAccess(infohash string, lastAccess int64) error {
	_, err := r.db.Exec(`UPDATE torrent_cache_meta SET last_access = ? WHERE infohash = ?`, lastAccess, infohash)
	return err
}

// Delete removes the metadata row (caller is responsible for removing the
// on-disk data separately).
func (r *TorrentCacheRepo) Delete(infohash string) error {
	_, err := r.db.Exec(`DELETE FROM torrent_cache_meta WHERE infohash = ?`, infohash)
	return err
}

// Names returns the on-disk name of every tracked torrent — used by the
// startup orphan sweep to tell valid cached files from leaked ones.
func (r *TorrentCacheRepo) Names() ([]string, error) {
	rows, err := r.db.Query(`SELECT name FROM torrent_cache_meta WHERE name != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// TotalSize returns SUM(size) across all tracked torrents.
func (r *TorrentCacheRepo) TotalSize() (int64, error) {
	var total int64
	err := r.db.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM torrent_cache_meta`).Scan(&total)
	return total, err
}

// EvictionCandidates returns all rows ordered by last_access ascending
// (least-recently-used first) — the eviction worker walks this list,
// skipping infohashes with an active stream, until the running total drops
// under the target.
func (r *TorrentCacheRepo) EvictionCandidates() ([]TorrentCacheEntry, error) {
	rows, err := r.db.Query(`SELECT infohash, name, size, last_access FROM torrent_cache_meta ORDER BY last_access ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TorrentCacheEntry
	for rows.Next() {
		var e TorrentCacheEntry
		if err := rows.Scan(&e.InfoHash, &e.Name, &e.Size, &e.LastAccess); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
