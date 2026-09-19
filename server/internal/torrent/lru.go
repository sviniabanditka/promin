package torrent

import (
	"context"
	"time"
)

// evictionHysteresis: once the cache exceeds the limit, eviction runs
// until usage drops to this fraction of the limit, per
// docs/data-model.md ("гистерезис, чтобы не эвиктить каждые 5
// минут по чуть-чуть").
const evictionHysteresis = 0.90

// sweepInterval matches docs/data-model.md ("тикер раз в 5
// минут").
const sweepInterval = 5 * time.Minute

// StartBackgroundWorkers runs the LRU-eviction and idle-drop sweeps until
// ctx is cancelled. Call once from main in a goroutine.
func (m *Manager) StartBackgroundWorkers(ctx context.Context) {
	m.closeWg.Add(1)
	defer m.closeWg.Done()

	// Once at start: what a restart left behind gets measured and aged out
	// without waiting for the first tick.
	m.refreshSizes()
	m.evictExpired()
	m.enforceCacheLimit()
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.closing:
			return
		case <-ticker.C:
			m.dropIdleTorrents()
			m.refreshSizes()
			m.evictExpired()
			m.enforceCacheLimit()
		}
	}
}

// refreshSizes replaces each row's size with what is really on disk. Upsert
// records the torrent's full length, so a series pack someone opened once
// counted 12 GB while holding 15 MB — the limit tripped on phantoms and spared
// nothing real. A row whose files are gone (and that is not being downloaded
// right now) is dropped: it was only ever inflating the total.
func (m *Manager) refreshSizes() {
	rows, err := m.cfg.Repo.EvictionCandidates()
	if err != nil {
		m.cfg.Logger.Warn("torrent: failed to list cache rows", "error", err)
		return
	}
	for _, c := range rows {
		if c.Name == "" {
			continue // metadata never arrived; nothing on disk under that name
		}
		size := m.onDiskSize(c.Name)
		if size == 0 {
			if _, held := m.lookup(c.InfoHash); held {
				continue // just added, first pieces not written yet
			}
			m.cfg.Logger.Info("torrent: dropping cache row with no files", "infohash", c.InfoHash, "name", c.Name)
			if err := m.cfg.Repo.Delete(c.InfoHash); err != nil {
				m.cfg.Logger.Warn("torrent: failed to delete phantom cache row", "infohash", c.InfoHash, "error", err)
			}
			continue
		}
		if size != c.Size {
			if err := m.cfg.Repo.SetSize(c.InfoHash, size); err != nil {
				m.cfg.Logger.Warn("torrent: failed to refresh cache size", "infohash", c.InfoHash, "error", err)
			}
		}
	}
}

// evictExpired deletes torrents nobody has touched for CacheTTL, regardless of
// how much room is left — a household's cache is "what we watched this week",
// not an archive. Anything with an open reader is left alone.
func (m *Manager) evictExpired() {
	cutoff := time.Now().Add(-m.cfg.CacheTTL).Unix()
	rows, err := m.cfg.Repo.EvictionCandidates()
	if err != nil {
		return
	}
	for _, c := range rows {
		if c.LastAccess >= cutoff {
			break // ordered by last_access: the rest are fresher
		}
		if m.isActive(c.InfoHash) {
			continue
		}
		m.dropEntry(c.InfoHash)
		if err := m.cfg.Repo.Delete(c.InfoHash); err != nil {
			m.cfg.Logger.Warn("torrent: failed to delete expired cache meta", "infohash", c.InfoHash, "error", err)
		}
		m.removeTorrentFiles(c.Name)
		m.cfg.Logger.Info("torrent: expired torrent removed", "infohash", c.InfoHash, "name", c.Name, "size", c.Size,
			"idle_days", (time.Now().Unix()-c.LastAccess)/86400)
	}
}

// dropIdleTorrents drops (from the anacrolix client, stopping network
// activity) any torrent with zero open readers whose last access is older
// than IdleTimeout, per docs/backend.md ("автоматически
// останавливаются (Drop) через таймаут неактивности"). The on-disk data
// and torrent_cache_meta row are left alone — only LRU eviction
// (enforceCacheLimit) removes those.
func (m *Manager) dropIdleTorrents() {
	cutoff := time.Now().Add(-m.cfg.IdleTimeout)

	m.mu.Lock()
	var idle []*entry
	var idleHashes []string
	for ih, e := range m.entries {
		e.mu.Lock()
		readers := e.readers
		la := e.lastAccess
		e.mu.Unlock()
		if readers == 0 && la.Before(cutoff) {
			idle = append(idle, e)
			idleHashes = append(idleHashes, ih)
		}
	}
	for _, ih := range idleHashes {
		delete(m.entries, ih)
	}
	m.mu.Unlock()

	for i, e := range idle {
		e.t.Drop()
		m.cfg.Logger.Info("torrent: dropping idle torrent (network activity stops, cache kept)", "infohash", idleHashes[i])
	}
}

// enforceCacheLimit implements the LRU sweep from docs/data-model.md
// section 4: if total tracked size exceeds CacheLimitBytes, delete the
// least-recently-accessed torrents (skipping ones with an open reader)
// until usage drops under the hysteresis threshold.
func (m *Manager) enforceCacheLimit() {
	total, err := m.cfg.Repo.TotalSize()
	if err != nil {
		m.cfg.Logger.Warn("torrent: failed to read cache total size", "error", err)
		return
	}
	if total <= m.cfg.CacheLimitBytes {
		return
	}

	target := int64(float64(m.cfg.CacheLimitBytes) * evictionHysteresis)

	candidates, err := m.cfg.Repo.EvictionCandidates()
	if err != nil {
		m.cfg.Logger.Warn("torrent: failed to list eviction candidates", "error", err)
		return
	}

	for _, c := range candidates {
		if total <= target {
			break
		}
		if m.isActive(c.InfoHash) {
			continue
		}

		m.dropEntry(c.InfoHash) // deletes files too if still in entries
		if err := m.cfg.Repo.Delete(c.InfoHash); err != nil {
			m.cfg.Logger.Warn("torrent: failed to delete evicted cache meta", "infohash", c.InfoHash, "error", err)
		}
		// Also remove by name: covers an idle-dropped torrent (no longer in
		// entries, so dropEntry above found nothing) whose data still sits on disk.
		m.removeTorrentFiles(c.Name)
		m.cfg.Logger.Info("torrent: LRU-evicted torrent", "infohash", c.InfoHash, "name", c.Name, "size", c.Size)
		total -= c.Size
	}
}

// isActive reports whether infoHash currently has an open stream reader —
// per docs/data-model.md ("пропуская инфохэши с активным
// стримом"), such torrents are never evicted regardless of last_access.
func (m *Manager) isActive(infoHash string) bool {
	e, ok := m.lookup(infoHash)
	if !ok {
		// Not held by the client right now (already idle-dropped) — still
		// eligible for eviction, "activity" only means an open reader.
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.readers > 0
}
