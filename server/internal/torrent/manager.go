// Package torrent wraps github.com/anacrolix/torrent into the Manager
// interface sketched in docs/backend.md: add a magnet, list
// its files, open one for sequential/readahead streaming, and enforce the
// disk LRU cache + active-torrent limits from docs/streaming.md
// section 3 and docs/data-model.md.
package torrent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	anacrolix "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/sviniabanditka/promin/server/internal/metrics"
	"github.com/sviniabanditka/promin/server/internal/store"
)

// Errors returned by Manager methods; httpapi maps these to the error
// envelope from docs/api.md.
var (
	// ErrMetadataTimeout is returned by AddMagnet when the swarm doesn't
	// hand over torrent info (file list) within Config.MetadataTimeout —
	// docs/streaming.md ("если за N секунд метадата не
	// получена, отдаём клиенту ошибку").
	ErrMetadataTimeout = errors.New("torrent: metadata fetch timed out")
	// ErrNotFound is returned when infoHash isn't a currently-added
	// torrent (never added, or dropped/evicted since).
	ErrNotFound = errors.New("torrent: not found")
	// ErrFileNotFound is returned by OpenFile/ListFiles when fileIdx is
	// out of range for the torrent.
	ErrFileNotFound = errors.New("torrent: file index out of range")
	// ErrTooManyActive is returned by AddMagnet when the active-torrent
	// limit is reached and every current torrent is being actively
	// streamed (so none can be evicted to make room), per
	// docs/api.md ("409 too_many_active_torrents").
	ErrTooManyActive = errors.New("torrent: too many active torrents")
)

// videoExts is the file-extension allowlist used to flag video files in
// ListFiles and to pick the "biggest video file" default, per
// docs/backend.md
var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true,
	".webm": true, ".m4v": true, ".ts": true, ".wmv": true,
}

// Config configures a Manager, per docs/backend.md
// (PROMIN_TORRENT_MAX_ACTIVE, PROMIN_TORRENT_CACHE_LIMIT_GB).
type Config struct {
	// DataDir is PROMIN_DATA_DIR; torrent data is stored under
	// DataDir/torrents/.
	DataDir string
	// MaxActive bounds the number of torrents added to the client at once
	// (PROMIN_TORRENT_MAX_ACTIVE, default 5).
	MaxActive int
	// CacheLimitBytes bounds total on-disk torrent data
	// (PROMIN_TORRENT_CACHE_LIMIT_GB * 1e9, default 30GB).
	CacheLimitBytes int64
	// MetadataTimeout bounds AddMagnet's wait for the swarm to hand over
	// torrent info (docs/streaming.md, "~20-30 c").
	MetadataTimeout time.Duration
	// IdleTimeout: a torrent with no open reader for this long is Dropped
	// from the client (network activity stops) but its on-disk data and
	// torrent_cache_meta row survive until LRU eviction picks it,
	// per docs/backend.md
	IdleTimeout time.Duration
	// Readahead is how far ahead of the current read position pieces get
	// bumped to top priority (docs/backend.md, "8-16 MB").
	Readahead int64
	// ListenPort is the BitTorrent peer port (PROMIN_TORRENT_PORT). 0 = the
	// anacrolix default (42069). Set it to run a second dev instance beside a
	// running one without the global-port clash.
	ListenPort int

	Repo   *store.TorrentCacheRepo
	Logger *slog.Logger
}

func (c *Config) applyDefaults() {
	if c.MaxActive <= 0 {
		c.MaxActive = 5
	}
	if c.CacheLimitBytes <= 0 {
		c.CacheLimitBytes = 30_000_000_000
	}
	if c.MetadataTimeout <= 0 {
		c.MetadataTimeout = 30 * time.Second
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = 10 * time.Minute
	}
	if c.Readahead <= 0 {
		// 64 MB: bigger buffer ahead of the play head so playback rarely catches
		// the swarm's download edge mid-stream. When it did (16 MB), the reader
		// blocked on an undelivered piece long enough for old Tizen's <video> to
		// give up with a network error. Client auto-recovers, but a deeper
		// readahead makes the stall itself far less frequent.
		c.Readahead = 64 << 20
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// FileInfo is one file inside an added torrent, as returned by
// AddMagnet/ListFiles.
type FileInfo struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	IsVideo bool   `json:"is_video"`
}

// ActiveTorrent describes one torrent currently held by the Manager, for
// GET /api/v1/torrents/active.
type ActiveTorrent struct {
	InfoHash   string    `json:"infohash"`
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	Downloaded int64     `json:"downloaded"`
	Progress   float64   `json:"progress"`
	Readers    int       `json:"readers"`
	AddedAt    time.Time `json:"added_at"`
}

// entry tracks Manager-side bookkeeping for one added torrent, beyond what
// *anacrolix.Torrent itself holds.
type entry struct {
	t       *anacrolix.Torrent
	addedAt time.Time
	name    string // on-disk name (t.Info().BestName()) — file or dir under DataDir

	mu             sync.Mutex
	readers        int // open OpenFile readers right now — never evicted while > 0
	lastAccess     time.Time
	lastAccessSync time.Time // last time lastAccess was flushed to SQLite (rate-limited)
}

// Manager is the Promin torrent engine: a thin, streaming-oriented
// wrapper over an anacrolix/torrent Client plus the disk LRU
// cache/active-limit policy from docs/backend.md
type Manager struct {
	cfg Config
	cl  *anacrolix.Client

	mu      sync.Mutex
	entries map[string]*entry // infohash (hex) -> entry

	closing chan struct{}
	closeWg sync.WaitGroup
}

// NewManager builds a Manager rooted at cfg.DataDir/torrents.
func NewManager(cfg Config) (*Manager, error) {
	cfg.applyDefaults()

	dataDir := filepath.Join(cfg.DataDir, "torrents")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("torrent: create data dir: %w", err)
	}

	cc := anacrolix.NewDefaultClientConfig()
	cc.DataDir = dataDir
	if cfg.ListenPort > 0 {
		cc.ListenPort = cfg.ListenPort
	}
	// Conservative connection budget so the torrent engine doesn't starve
	// remux/online streams sharing the same VPS NIC (docs/backend.md
	// section 6, "порядка 40-80 суммарно").
	cc.EstablishedConnsPerTorrent = 40
	cc.Logger = cc.Logger.WithNames("torrent")

	cl, err := anacrolix.NewClient(cc)
	if err != nil {
		return nil, fmt.Errorf("torrent: new client: %w", err)
	}

	m := &Manager{
		cfg:     cfg,
		cl:      cl,
		entries: make(map[string]*entry),
		closing: make(chan struct{}),
	}
	m.sweepOrphanFiles(dataDir)
	return m, nil
}

// sweepOrphanFiles deletes files/dirs under the torrent data dir that no
// tracked cache row claims — leaked data from torrents that were evicted before
// deletion worked (the old code targeted a non-existent infohash subdir), or
// left over after a crash. The cache is disposable (re-downloadable), so an
// orphan is always safe to remove. Runs once at startup, before any torrent is
// added, so every on-disk name is either a valid cache entry or garbage.
func (m *Manager) sweepOrphanFiles(dataDir string) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return
	}
	names, err := m.cfg.Repo.Names()
	if err != nil {
		m.cfg.Logger.Warn("torrent: orphan sweep skipped (cache names unavailable)", "error", err)
		return
	}
	keep := make(map[string]bool, len(names)*2)
	for _, n := range names {
		keep[n] = true
		keep[n+".part"] = true
	}
	for _, e := range entries {
		name := e.Name()
		// Skip anacrolix internals (.torrent.bolt.db and any other dotfile) and
		// files a cache row still claims.
		if strings.HasPrefix(name, ".") || keep[name] {
			continue
		}
		p := filepath.Join(dataDir, name)
		if err := os.RemoveAll(p); err != nil {
			m.cfg.Logger.Warn("torrent: failed to remove orphan file", "path", p, "error", err)
			continue
		}
		m.cfg.Logger.Info("torrent: removed orphan file (no cache row)", "name", name)
	}
}

// AddMagnet adds a magnet link and waits for the swarm to hand over
// torrent metadata (file list), per docs/backend.md It
// enforces MaxActive by dropping the least-recently-accessed *inactive*
// torrent (0 open readers) to make room; if every current torrent is
// active, it returns ErrTooManyActive.
func (m *Manager) AddMagnet(ctx context.Context, magnet string) (string, error) {
	t, err := m.cl.AddMagnet(magnet)
	if err != nil {
		return "", fmt.Errorf("torrent: add magnet: %w", err)
	}

	ih := t.InfoHash().HexString()

	// Already added (e.g. a second AddMagnet for a torrent we're already
	// streaming) — short-circuit rather than re-running eviction/limit
	// bookkeeping.
	m.mu.Lock()
	if _, ok := m.entries[ih]; ok {
		m.mu.Unlock()
		if err := m.waitInfo(ctx, t); err != nil {
			return "", err
		}
		return ih, nil
	}
	m.mu.Unlock()

	if err := m.waitInfo(ctx, t); err != nil {
		t.Drop()
		return "", err
	}

	now := time.Now()
	e := &entry{t: t, addedAt: now, lastAccess: now, name: t.Info().BestName()}

	m.mu.Lock()
	m.entries[ih] = e
	activeCount := len(m.entries)
	metrics.TorrentsActive.Set(float64(activeCount))
	m.mu.Unlock()

	if err := m.cfg.Repo.Upsert(ih, t.Info().BestName(), t.Length(), now.Unix()); err != nil {
		m.cfg.Logger.Warn("torrent: failed to persist cache meta on add", "infohash", ih, "error", err)
	}

	if activeCount > m.cfg.MaxActive {
		if err := m.evictOldestInactive(ih); err != nil {
			// Roll back: this magnet made us go over the limit and nothing
			// could be freed for it. Also delete the just-Upserted DB row — else
			// it survives with size=Length() forever, inflating TotalSize().
			m.dropEntry(ih)
			if err := m.cfg.Repo.Delete(ih); err != nil {
				m.cfg.Logger.Warn("torrent: failed to delete cache meta on rollback", "infohash", ih, "error", err)
			}
			return "", ErrTooManyActive
		}
	}

	return ih, nil
}

func (m *Manager) waitInfo(ctx context.Context, t *anacrolix.Torrent) error {
	ctx, cancel := context.WithTimeout(ctx, m.cfg.MetadataTimeout)
	defer cancel()
	select {
	case <-t.GotInfo():
		return nil
	case <-ctx.Done():
		return ErrMetadataTimeout
	}
}

// evictOldestInactive drops the least-recently-accessed torrent with no
// open readers, other than keep. Returns an error if none qualifies.
func (m *Manager) evictOldestInactive(keep string) error {
	m.mu.Lock()
	var candidateIH string
	var oldest time.Time
	for ih, e := range m.entries {
		if ih == keep {
			continue
		}
		e.mu.Lock()
		readers := e.readers
		la := e.lastAccess
		e.mu.Unlock()
		if readers > 0 {
			continue
		}
		if candidateIH == "" || la.Before(oldest) {
			candidateIH = ih
			oldest = la
		}
	}
	m.mu.Unlock()

	if candidateIH == "" {
		return errors.New("torrent: no inactive torrent to evict")
	}
	m.cfg.Logger.Info("torrent: dropping oldest inactive torrent to respect active limit", "infohash", candidateIH)
	m.dropEntry(candidateIH)
	// Also delete the cache_meta row — else TotalSize() keeps counting a gone
	// torrent and evicts healthy ones prematurely (parity with the other drop paths).
	if err := m.cfg.Repo.Delete(candidateIH); err != nil {
		m.cfg.Logger.Warn("torrent: cache_meta delete failed after evict", "infohash", candidateIH, "error", err)
	}
	return nil
}

func (m *Manager) dropEntry(ih string) {
	m.mu.Lock()
	e, ok := m.entries[ih]
	if ok {
		delete(m.entries, ih)
	}
	metrics.TorrentsActive.Set(float64(len(m.entries)))
	m.mu.Unlock()
	if ok {
		e.t.Drop()
		m.removeTorrentFiles(e.name)
	}
}

// removeTorrentFiles deletes a torrent's on-disk data. anacrolix's default
// storage writes files FLAT under DataDir by their own name — a single-file
// torrent is DataDir/<name> (or DataDir/<name>.part while incomplete), a
// multi-file one is the directory DataDir/<name>/. The old code deleted
// DataDir/torrents/<infohash>/ which never existed, so evicted torrents leaked
// their data forever. Idempotent (RemoveAll on a missing path is a no-op).
func (m *Manager) removeTorrentFiles(name string) {
	if name == "" {
		return
	}
	base := filepath.Join(m.cfg.DataDir, "torrents", name)
	for _, p := range []string{base, base + ".part"} {
		if err := os.RemoveAll(p); err != nil {
			m.cfg.Logger.Warn("torrent: failed to remove on-disk data", "path", p, "error", err)
		}
	}
}

// ListFiles returns every file in an already-added torrent.
func (m *Manager) ListFiles(infoHash string) ([]FileInfo, error) {
	e, ok := m.lookup(infoHash)
	if !ok {
		return nil, ErrNotFound
	}
	return filesOf(e.t), nil
}

func filesOf(t *anacrolix.Torrent) []FileInfo {
	files := t.Files()
	out := make([]FileInfo, 0, len(files))
	for i, f := range files {
		ext := strings.ToLower(filepath.Ext(f.DisplayPath()))
		out = append(out, FileInfo{
			Index:   i,
			Name:    f.DisplayPath(),
			Size:    f.Length(),
			IsVideo: videoExts[ext],
		})
	}
	return out
}

// LargestVideoFile returns the index of the biggest video-extension file
// in the torrent, per docs/streaming.md ("выбор наибольшего
// видеофайла"). Returns -1 if the torrent has no video files.
func (m *Manager) LargestVideoFile(infoHash string) (int, error) {
	e, ok := m.lookup(infoHash)
	if !ok {
		return -1, ErrNotFound
	}
	files := e.t.Files()
	best, bestSize := -1, int64(-1)
	for i, f := range files {
		ext := strings.ToLower(filepath.Ext(f.DisplayPath()))
		if !videoExts[ext] {
			continue
		}
		if f.Length() > bestSize {
			best, bestSize = i, f.Length()
		}
	}
	return best, nil
}

// OpenFile returns a ReadSeekCloser over fileIdx's data, with sequential +
// readahead piece priority driven by reads/seeks (the anacrolix Reader's
// standard behaviour, docs/backend.md / docs/streaming.md
// section 3). Callers (the HTTP handler) must Close it when done serving
// the request — this decrements the active-reader count that protects the
// torrent from LRU eviction and idle-drop.
func (m *Manager) OpenFile(infoHash string, fileIdx int) (io.ReadSeekCloser, error) {
	e, ok := m.lookup(infoHash)
	if !ok {
		return nil, ErrNotFound
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return nil, ErrFileNotFound
	}
	f := files[fileIdx]

	r := f.NewReader()
	r.SetReadahead(m.cfg.Readahead)
	r.SetResponsive()

	e.mu.Lock()
	e.readers++
	e.lastAccess = time.Now()
	e.mu.Unlock()
	m.touchAccess(infoHash, e)

	return &fileReader{Reader: r, m: m, infoHash: infoHash, e: e}, nil
}

// FilePath returns the on-disk path a completed/partially-downloaded file
// lives (or will live) at — used by the MKV remux path, which hands
// ffmpeg a local path rather than streaming through Manager itself
// (docs/streaming.md, "MKV: отдать как есть или ремуксить").
func (m *Manager) FilePath(infoHash string, fileIdx int) (string, error) {
	e, ok := m.lookup(infoHash)
	if !ok {
		return "", ErrNotFound
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return "", ErrFileNotFound
	}
	return filepath.Join(m.cfg.DataDir, "torrents", files[fileIdx].Path()), nil
}

// Prefetch marks the whole file wanted so ffmpeg muxes the full runtime instead
// of stalling at the Reader readahead edge. The muxed (== HLS-seekable) region
// then reaches the real end, so a forward scrub no longer clamps back to the
// downloaded edge (the "seek +10min lands +2min" bug). anacrolix File.Download()
// only raises piece priority; the sequential Reader still serves playback near
// the head, so this doesn't starve playback.
// ponytail: whole-file prefetch, no free-disk gate yet — add one if it bites.
func (m *Manager) Prefetch(infoHash string, fileIdx int) error {
	e, ok := m.lookup(infoHash)
	if !ok {
		return ErrNotFound
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return ErrFileNotFound
	}
	files[fileIdx].Download()
	return nil
}

// fileReader wraps an anacrolix torrent.Reader, updating Manager
// bookkeeping (active-reader refcount, last_access) on Close.
type fileReader struct {
	anacrolix.Reader
	m        *Manager
	infoHash string
	e        *entry
	closed   bool
	mu       sync.Mutex
}

func (fr *fileReader) Read(p []byte) (int, error) {
	n, err := fr.Reader.Read(p)
	if n > 0 {
		// Single e.mu hold: update lastAccess AND decide the rate-limited SQLite
		// sync together (was two lock cycles per chunk — Read then touchAccess).
		fr.e.mu.Lock()
		now := time.Now()
		fr.e.lastAccess = now
		doSync := now.Sub(fr.e.lastAccessSync) >= time.Minute
		if doSync {
			fr.e.lastAccessSync = now
		}
		fr.e.mu.Unlock()
		if doSync {
			if serr := fr.m.cfg.Repo.TouchAccess(fr.infoHash, now.Unix()); serr != nil {
				fr.m.cfg.Logger.Warn("torrent: touch access failed", "infohash", fr.infoHash, "error", serr)
			}
		}
	}
	return n, err
}

func (fr *fileReader) Close() error {
	fr.mu.Lock()
	if fr.closed {
		fr.mu.Unlock()
		return nil
	}
	fr.closed = true
	fr.mu.Unlock()

	fr.e.mu.Lock()
	fr.e.readers--
	fr.e.mu.Unlock()

	return fr.Reader.Close()
}

// touchAccess persists last_access to SQLite, rate-limited to once a
// minute per infohash per docs/data-model.md ("не чаще раза
// в минуту на инфохэш, чтобы не бить БД на каждый чанк").
func (m *Manager) touchAccess(infoHash string, e *entry) {
	e.mu.Lock()
	now := time.Now()
	if now.Sub(e.lastAccessSync) < time.Minute {
		e.mu.Unlock()
		return
	}
	e.lastAccessSync = now
	e.mu.Unlock()

	if err := m.cfg.Repo.TouchAccess(infoHash, now.Unix()); err != nil {
		m.cfg.Logger.Warn("torrent: failed to update last_access", "infohash", infoHash, "error", err)
	}
}

// Remove drops a torrent from the client and removes its cache metadata
// row + on-disk data, per DELETE /api/v1/torrents/{infohash}.
func (m *Manager) Remove(infoHash string) error {
	m.mu.Lock()
	e, ok := m.entries[infoHash]
	if ok {
		delete(m.entries, infoHash)
	}
	metrics.TorrentsActive.Set(float64(len(m.entries)))
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}

	e.t.Drop()
	if err := m.cfg.Repo.Delete(infoHash); err != nil {
		m.cfg.Logger.Warn("torrent: failed to delete cache meta", "infohash", infoHash, "error", err)
	}
	m.removeTorrentFiles(e.name)
	return nil
}

// Active lists every torrent currently held by the Manager, for
// GET /api/v1/torrents/active.
func (m *Manager) Active() []ActiveTorrent {
	m.mu.Lock()
	out := make([]ActiveTorrent, 0, len(m.entries))
	for ih, e := range m.entries {
		e.mu.Lock()
		readers := e.readers
		e.mu.Unlock()
		size := e.t.Length()
		downloaded := e.t.BytesCompleted()
		progress := 0.0
		if size > 0 {
			progress = float64(downloaded) / float64(size)
		}
		out = append(out, ActiveTorrent{
			InfoHash:   ih,
			Name:       e.name, // cached at add — avoids Info().BestName() under m.mu per entry
			Size:       size,
			Downloaded: downloaded,
			Progress:   progress,
			Readers:    readers,
			AddedAt:    e.addedAt,
		})
	}
	m.mu.Unlock()

	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	return out
}

// lookup finds an entry, refusing to hand it out while it isn't wired into
// the Manager's index yet.
func (m *Manager) lookup(infoHash string) (*entry, bool) {
	m.mu.Lock()
	e, ok := m.entries[infoHash]
	m.mu.Unlock()
	return e, ok
}

// InfoHashValid reports whether s parses as a 40-hex-char infohash, used by
// the HTTP layer to reject malformed path parameters before touching the
// Manager.
func InfoHashValid(s string) bool {
	var h metainfo.Hash
	return h.FromHexString(s) == nil && len(s) == 40
}

// Close stops the background workers and the underlying torrent client,
// per docs/backend.md (graceful shutdown step 2).
func (m *Manager) Close() error {
	close(m.closing)
	m.closeWg.Wait()
	errs := m.cl.Close()
	if len(errs) > 0 {
		return fmt.Errorf("torrent: close client: %v", errs)
	}
	return nil
}
