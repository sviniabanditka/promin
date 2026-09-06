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
			m.enforceCacheLimit()
		}
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
