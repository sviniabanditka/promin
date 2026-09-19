// Package dvr records live TV to disk: one ffmpeg per recording, copying the
// channel's stream into an HLS directory the ordinary player can play back
// (docs/tv.md). Recordings are scheduled from the EPG, so the service is a
// 30 s ticker that starts what is due, stops what is over and keeps the
// directory inside its size budget.
package dvr

import (
	"context"
	"encoding/hex"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

const (
	// Broadcasters are never on time: start a minute early, stay three minutes
	// late so the last goal of the match is on the recording.
	PadBeforeSec = 60
	PadAfterSec  = 180
	// The scheduler wakes up this often; a minute of padding absorbs the jitter.
	tickInterval = 30 * time.Second
	// Refuse to start a recording with less than this much free space: the
	// SQLite database lives on the same volume and a full disk breaks writes.
	minFreeBytes = 3 << 30 // 3 GiB
	// A recording longer than this is a mistake (a channel left recording all
	// night), and ffmpeg gets the same bound with -t.
	maxDurationSec = 6 * 3600
	// PlaylistFile is the HLS playlist inside a recording's directory.
	PlaylistFile = "playlist.m3u8"
)

// StreamSource hands the recorder the raw upstream URL of a channel plus the
// headers it needs. The recorder does NOT go through /relay: it runs on the
// server, so it can fetch the origin directly (one hop less, no media token).
type StreamSource func(channelID string) (url, userAgent, referer string, err error)

type Service struct {
	repo    *store.RecordingsRepo
	dir     string
	ffmpeg  string
	maxSize int64
	source  StreamSource
	logger  *slog.Logger

	// baseCtx is the process-lifetime context Run was given: a buffer or a
	// recording must outlive the HTTP request that asked for it.
	baseCtx context.Context

	mu      sync.Mutex
	running map[string]context.CancelFunc
	// Rolling timeshift windows by channel id (buffer.go).
	buffers map[string]*buffer
}

// Reasons a recording or a buffer could not start.
type dvrError string

func (e dvrError) Error() string { return string(e) }

const (
	errNoDisk   = dvrError("dvr: not enough free space")
	errNoStream = dvrError("dvr: channel has no stream")
)

func New(repo *store.RecordingsRepo, dir, ffmpeg string, maxSizeBytes int64, source StreamSource, logger *slog.Logger) *Service {
	return &Service{
		baseCtx: context.Background(),
		repo:    repo,
		dir:     dir,
		ffmpeg:  ffmpeg,
		maxSize: maxSizeBytes,
		source:  source,
		logger:  logger,
		running: map[string]context.CancelFunc{},
		buffers: map[string]*buffer{},
	}
}

// Dir is where a recording's playlist and segments live.
func (s *Service) Dir(id string) string { return filepath.Join(s.dir, id) }

// Schedule stores a recording. start/end are the programme's own times; the
// padding is added here so every caller gets it.
func (s *Service) Schedule(userID int64, channelID, channelTitle, title string, start, end int64) (store.Recording, error) {
	now := time.Now().Unix()
	start -= PadBeforeSec
	end += PadAfterSec
	if start < now {
		start = now // "record what is on right now" starts immediately
	}
	if end <= start {
		return store.Recording{}, fmt.Errorf("dvr: end before start")
	}
	if end-start > maxDurationSec {
		end = start + maxDurationSec
	}
	rec := store.Recording{
		ID:           newID(),
		UserID:       userID,
		ChannelID:    channelID,
		ChannelTitle: channelTitle,
		Title:        title,
		StartAt:      start,
		EndAt:        end,
		State:        store.RecScheduled,
		CreatedAt:    now,
	}
	if err := s.repo.Add(rec); err != nil {
		return store.Recording{}, err
	}
	return rec, nil
}

// Delete stops the recording if it is running and removes its files.
func (s *Service) Delete(id string, userID int64) error {
	s.stop(id)
	if err := s.repo.Delete(id, userID); err != nil {
		return err
	}
	return os.RemoveAll(s.Dir(id))
}

// Run is the scheduler loop; it returns when ctx is done (killing whatever is
// still recording, which leaves a playable partial recording behind).
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.baseCtx = ctx
	s.mu.Unlock()
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			s.stopAll()
			s.stopBuffers()
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	pending, err := s.repo.Pending()
	if err != nil {
		s.logger.Warn("dvr: list pending failed", "error", err)
		return
	}
	now := time.Now().Unix()
	for _, rec := range pending {
		switch {
		case rec.State == store.RecScheduled && now >= rec.StartAt && now < rec.EndAt:
			s.start(ctx, rec)
		case rec.State == store.RecScheduled && now >= rec.EndAt:
			// The server was down for the whole programme.
			s.fail(rec.ID, "missed")
		case rec.State == store.RecRecording && now >= rec.EndAt+30:
			// ffmpeg's own -t should have ended it; make sure.
			s.stop(rec.ID)
			s.finish(rec.ID)
		case rec.State == store.RecRecording && !s.isRunning(rec.ID):
			// A restart left the row mid-recording: resume for what is left.
			s.start(ctx, rec)
		}
	}
	s.sweep()
	s.bufferSweep()
}

func (s *Service) isRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[id]
	return ok
}

func (s *Service) start(parent context.Context, rec store.Recording) {
	if s.isRunning(rec.ID) {
		return
	}
	free, err := freeBytes(s.dir)
	if err == nil && free < minFreeBytes {
		s.logger.Warn("dvr: not enough free space", "id", rec.ID, "free_mb", free>>20)
		s.fail(rec.ID, "no_disk")
		return
	}
	url, ua, ref, err := s.source(rec.ChannelID)
	if err != nil || url == "" {
		s.fail(rec.ID, "no_stream")
		return
	}
	dir := s.Dir(rec.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.fail(rec.ID, "mkdir")
		return
	}
	left := rec.EndAt - time.Now().Unix()
	if left <= 0 {
		s.fail(rec.ID, "missed")
		return
	}

	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.running[rec.ID] = cancel
	s.mu.Unlock()
	_ = s.repo.SetState(rec.ID, store.RecRecording, "")

	args := ffmpegArgs(url, ua, ref, dir, left)
	cmd := exec.CommandContext(ctx, s.ffmpeg, args...)
	// SIGINT, not SIGKILL: ffmpeg then closes the playlist with an ENDLIST and
	// the recording is a normal VOD instead of a stream that never ends.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		s.clearRunning(rec.ID)
		s.fail(rec.ID, "ffmpeg")
		return
	}
	s.logger.Info("dvr: recording", "id", rec.ID, "channel", rec.ChannelID, "title", rec.Title, "seconds", left)
	go func() {
		err := cmd.Wait()
		cancel()
		s.clearRunning(rec.ID)
		if err != nil {
			s.logger.Info("dvr: ffmpeg exited", "id", rec.ID, "error", err)
		}
		s.finish(rec.ID)
	}()
}

// finish marks a recording done when it produced something, failed when not.
func (s *Service) finish(id string) {
	rec, err := s.repo.Get(id)
	if err != nil {
		return
	}
	if rec.State == store.RecDone || rec.State == store.RecFailed {
		return
	}
	size := dirSize(s.Dir(id))
	_ = s.repo.SetBytes(id, size)
	if size == 0 {
		s.fail(id, "empty")
		return
	}
	_ = s.repo.SetState(id, store.RecDone, "")
	s.logger.Info("dvr: recorded", "id", id, "mb", size>>20)
}

func (s *Service) fail(id, reason string) {
	_ = s.repo.SetState(id, store.RecFailed, reason)
	_ = os.RemoveAll(s.Dir(id))
}

func (s *Service) stop(id string) {
	s.mu.Lock()
	cancel := s.running[id]
	delete(s.running, id)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) clearRunning(id string) {
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
}

func (s *Service) stopBuffers() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.buffers))
	for id := range s.buffers {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.StopBuffer(id)
	}
}

func (s *Service) stopAll() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.running))
	for id, c := range s.running {
		cancels = append(cancels, c)
		delete(s.running, id)
	}
	s.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

// sweep keeps the recordings under the size budget by deleting the oldest
// finished ones, and refreshes the size of what is being recorded now.
func (s *Service) sweep() {
	for id := range s.snapshotRunning() {
		_ = s.repo.SetBytes(id, dirSize(s.Dir(id)))
	}
	total, err := s.repo.TotalBytes()
	if err != nil || s.maxSize <= 0 || total <= s.maxSize {
		return
	}
	old, err := s.repo.OldestDone()
	if err != nil {
		return
	}
	for _, rec := range old {
		if total <= s.maxSize {
			return
		}
		s.logger.Info("dvr: retention drop", "id", rec.ID, "title", rec.Title, "mb", rec.Bytes>>20)
		_ = os.RemoveAll(s.Dir(rec.ID))
		_ = s.repo.DeleteByID(rec.ID)
		total -= rec.Bytes
	}
}

func (s *Service) snapshotRunning() map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]struct{}, len(s.running))
	for id := range s.running {
		out[id] = struct{}{}
	}
	return out
}

// ffmpegArgs copies the live stream into an HLS directory. `-t` bounds the
// recording even if the scheduler never gets to stop it.
func ffmpegArgs(url, ua, ref, dir string, seconds int64) []string {
	args := []string{"-y", "-nostdin", "-nostats", "-loglevel", "warning"}
	if ua != "" {
		args = append(args, "-user_agent", ua)
	}
	if ref != "" {
		args = append(args, "-headers", "Referer: "+ref+"\r\n")
	}
	args = append(args,
		"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5",
		"-i", url,
		"-t", strconv.FormatInt(seconds, 10),
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c", "copy",
		"-f", "hls",
		"-hls_time", "6",
		"-hls_list_size", "0",
		"-hls_playlist_type", "event",
		"-hls_segment_filename", filepath.Join(dir, "seg-%05d.ts"),
		filepath.Join(dir, PlaylistFile),
	)
	return args
}

func dirSize(dir string) int64 {
	var total int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}
