package remux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sviniabanditka/promin/server/internal/metrics"
)

// Config configures a Queue, per docs/backend.md
type Config struct {
	DataDir     string // PROMIN_DATA_DIR; jobs live under DataDir/remux/{id}/
	FFmpegPath  string // PROMIN_FFMPEG_PATH
	FFprobePath string // PROMIN_FFPROBE_PATH (codec/HDR detection for transcode)
	// MaxTranscodes bounds concurrent transcode_hevc jobs
	// (PROMIN_REMUX_MAX_TRANSCODES). Transcode is CPU-heavy (~2-3 cores per
	// 1080p job, docs/streaming.md), so it gets its OWN semaphore separate
	// from the cheap copy jobs — never sharing the copy limiter.
	MaxTranscodes int
	// MaxCopyJobs bounds concurrent copy_hls/copy_mkv ffmpeg processes.
	// Copy is cheap on CPU (~0.1 core, docs/streaming.md) but
	// disk/memory for temp segments is finite, so it's still capped.
	MaxCopyJobs int
	// JobTTL: a job with no /remux/{job}/... request for this long is
	// considered abandoned and cleaned up (docs/backend.md).
	JobTTL time.Duration
	Logger *slog.Logger
}

// Queue is the in-memory remux job queue (no external broker — matches
// the rest of Promin's single-binary/SQLite-only design,
// docs/backend.md intro).
type Queue struct {
	cfg Config

	mu      sync.Mutex
	jobs    map[string]*Job
	dedup   map[string]string // dedupKey -> job id
	closing chan struct{}

	copySem      chan struct{}
	transcodeSem chan struct{}
	zscale       bool // ffmpeg build has zscale (libzimg) → HDR tone-map possible
}

// NewQueue builds a Queue rooted at cfg.DataDir/remux, clearing any
// leftover job directories from a previous process (orphan cleanup on
// restart, docs/backend.md — jobs aren't persisted, so
// anything found on disk at startup is by definition orphaned).
func NewQueue(cfg Config) (*Queue, error) {
	if cfg.MaxCopyJobs <= 0 {
		cfg.MaxCopyJobs = 4
	}
	if cfg.MaxTranscodes <= 0 {
		cfg.MaxTranscodes = 1
	}
	if cfg.JobTTL <= 0 {
		cfg.JobTTL = 30 * time.Minute
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = "ffmpeg"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	root := filepath.Join(cfg.DataDir, "remux")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("remux: create root dir: %w", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("remux: read root dir: %w", err)
	}
	for _, e := range entries {
		p := filepath.Join(root, e.Name())
		if err := os.RemoveAll(p); err != nil {
			cfg.Logger.Warn("remux: failed removing orphan job dir", "path", p, "error", err)
		} else {
			cfg.Logger.Info("remux: removed orphan job dir from previous run", "path", p)
		}
	}

	q := &Queue{
		cfg:          cfg,
		jobs:         make(map[string]*Job),
		dedup:        make(map[string]string),
		closing:      make(chan struct{}),
		copySem:      make(chan struct{}, cfg.MaxCopyJobs),
		transcodeSem: make(chan struct{}, cfg.MaxTranscodes),
		zscale:       HasZscale(cfg.FFmpegPath),
	}
	// HDR→SDR tone-mapping needs zscale (libzimg), docs/streaming.md Т4.
	cfg.Logger.Info("remux: transcode capabilities", "zscale_hdr_tonemap", q.zscale)
	return q, nil
}

// Submit finds an existing job for (kind, source, audioIndex, start) — exact
// dedup key, or any live job of the same track whose muxed range covers start
// (see coveringLocked) — or creates and starts a new one. Per docs/backend.md:
// "повторный запрос отдаёт уже существующий job".
func (q *Queue) Submit(kind Kind, source string, audioIndex int) (*Job, error) {
	return q.submit(kind, source, audioIndex, false, nil, 0)
}

// SubmitFrom is Submit with a start offset (seconds into the source).
func (q *Queue) SubmitFrom(kind Kind, source string, audioIndex int, startSec float64) (*Job, error) {
	return q.submit(kind, source, audioIndex, false, nil, startSec)
}

// SubmitTranscode submits an HEVC/AV1→H264 transcode job. hdr (from ProbeVideo)
// drives tone-mapping; audio (from ProbeAudio) drives multi-audio output.
func (q *Queue) SubmitTranscode(source string, audioIndex int, hdr bool, audio []AudioMeta) (*Job, error) {
	return q.submit(KindTranscodeHEVC, source, audioIndex, hdr, audio, 0)
}

// SubmitTranscodeFrom is SubmitTranscode starting at startSec.
func (q *Queue) SubmitTranscodeFrom(source string, audioIndex int, hdr bool, audio []AudioMeta, startSec float64) (*Job, error) {
	return q.submit(KindTranscodeHEVC, source, audioIndex, hdr, audio, startSec)
}

// SubmitCopyMKV submits a container-remux job (video copied, audio → AAC).
// audioIndex selects which source audio track is muxed inline (the client picks
// it; a different index is a distinct dedup key → its own job).
func (q *Queue) SubmitCopyMKV(source string, audioIndex int, audio []AudioMeta) (*Job, error) {
	return q.submit(KindCopyMKV, source, audioIndex, false, audio, 0)
}

// SubmitCopyMKVFrom is SubmitCopyMKV starting at startSec.
func (q *Queue) SubmitCopyMKVFrom(source string, audioIndex int, audio []AudioMeta, startSec float64) (*Job, error) {
	return q.submit(KindCopyMKV, source, audioIndex, false, audio, startSec)
}

// SubmitMux2 muxes a video track URL and an audio track URL into one HLS job.
// Both inputs are internal (the YouTube sidecar), so this bypasses the public
// /remux handler and its upstream checks on purpose.
func (q *Queue) SubmitMux2(videoURL, audioURL string) (*Job, error) {
	return q.submit2(KindMux2, videoURL, audioURL, 0, false, nil, 0)
}

// SubmitMux2From is SubmitMux2 for inputs that already begin at startSec (the
// URLs carry the offset; no -ss). StartSec is kept on the job so the playlist
// reports X-Remux-Start like any other offset job.
func (q *Queue) SubmitMux2From(videoURL, audioURL string, startSec float64) (*Job, error) {
	return q.submit2(KindMux2, videoURL, audioURL, 0, false, nil, startSec)
}

func (q *Queue) submit(kind Kind, source string, audioIndex int, hdr bool, audio []AudioMeta, startSec float64) (*Job, error) {
	return q.submit2(kind, source, "", audioIndex, hdr, audio, startSec)
}

func (q *Queue) submit2(kind Kind, source, source2 string, audioIndex int, hdr bool, audio []AudioMeta, startSec float64) (*Job, error) {
	if startSec < 0 {
		startSec = 0
	}
	key := dedupKey(kind, source, audioIndex, startSec)

	q.mu.Lock()
	if id, ok := q.dedup[key]; ok {
		if j, ok := q.jobs[id]; ok {
			// A failed job (e.g. cold-torrent ffprobe timeout) must NOT lock the
			// retry for the whole JobTTL — drop it and fall through to a fresh one.
			if j.State() == StateFailed {
				delete(q.dedup, key)
				delete(q.jobs, id)
				go os.RemoveAll(j.OutputDir)
			} else {
				q.mu.Unlock()
				j.Touch()
				return j, nil
			}
		}
	}
	// Same track, different offset: a live job whose muxed range already covers
	// startSec serves it as-is (the client reads X-Remux-Start / the redirect's
	// start= and seeks inside). An audio switch back, or a seek back before the
	// current job's start, is then instant instead of another ffmpeg.
	if j := q.coveringLocked(kind, source, audioIndex, startSec); j != nil {
		q.mu.Unlock()
		j.Touch()
		return j, nil
	}
	// Per-source cap on ffmpeg processes: the source URL carries the viewer's
	// token, so its siblings are this viewer's own earlier jobs (audio switch /
	// far seek) that the player has already stopped reading. Evict the least
	// recently accessed ones instead of letting them run the whole file out.
	// Transcode is capped at 1 because MaxTranscodes is 1 in prod — a second
	// job would just queue behind the abandoned one forever.
	victims := q.overCapLocked(kind, source)

	id := newJobID()
	outputDir := filepath.Join(q.cfg.DataDir, "remux", id)
	job := newJob(id, kind, source, audioIndex, outputDir, hdr, audio, startSec)
	job.Source2 = source2
	q.jobs[id] = job
	q.dedup[key] = id
	q.mu.Unlock()

	for _, v := range victims {
		q.cfg.Logger.Info("remux: evicting sibling job over per-source cap", "job_id", v.ID, "kind", v.Kind, "start", v.StartSec)
		go q.destroy(v)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		job.setFailed(err)
		return job, fmt.Errorf("remux: create job dir: %w", err)
	}

	go q.run(job)
	return job, nil
}

// coverMarginSec keeps a reused job's edge comfortably past the requested
// offset so the player lands inside published segments, not on the last one.
const coverMarginSec = 10

// coveringLocked finds a non-failed job for (kind, source, audio) whose output
// already covers startSec: a finished job covers everything past its StartSec,
// a running one up to StartSec+MuxedSec()-margin. Caller holds q.mu.
func (q *Queue) coveringLocked(kind Kind, source string, audioIndex int, startSec float64) *Job {
	for _, j := range q.jobs {
		if j.Kind != kind || j.Source != source || j.AudioIndex != audioIndex || j.StartSec > startSec {
			continue
		}
		switch j.State() {
		case StateReady:
			return j
		case StateRunning:
			if startSec-j.StartSec+coverMarginSec <= j.MuxedSec() {
				return j
			}
		}
	}
	return nil
}

// perSourceCap is how many queued/running ffmpeg processes one (kind, source)
// may hold. Copy keeps the previous job alive so a quick switch back reuses it;
// transcode can't afford two (~2-3 cores each, MaxTranscodes=1).
func perSourceCap(kind Kind) int {
	if kind == KindTranscodeHEVC {
		return 1
	}
	return 2
}

// overCapLocked removes from the queue the least recently accessed live jobs of
// (kind, source) so that one more fits under perSourceCap, and returns them for
// the caller to destroy outside the lock. Caller holds q.mu.
func (q *Queue) overCapLocked(kind Kind, source string) []*Job {
	var live []*Job
	for _, j := range q.jobs {
		if j.Kind != kind || j.Source != source {
			continue
		}
		if s := j.State(); s == StateQueued || s == StateRunning {
			live = append(live, j)
		}
	}
	excess := len(live) - perSourceCap(kind) + 1
	if excess <= 0 {
		return nil
	}
	sort.Slice(live, func(a, b int) bool { return live[a].idleSince().Before(live[b].idleSince()) })
	victims := live[:excess]
	for _, v := range victims {
		delete(q.jobs, v.ID)
	}
	for key, id := range q.dedup {
		if _, ok := q.jobs[id]; !ok {
			delete(q.dedup, key)
		}
	}
	return victims
}

// TranscodeQueueDepth counts transcode jobs still waiting for a slot (state
// queued, not yet running). Served in the /remux 202 body as queue_position so
// a waiting viewer sees "N-й у черзі" instead of an opaque spinner (Т6). O(jobs),
// only called on the poll path — fine for the handful of live jobs.
func (q *Queue) TranscodeQueueDepth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, j := range q.jobs {
		if j.Kind == KindTranscodeHEVC && j.State() == StateQueued {
			n++
		}
	}
	return n
}

// Get looks up a job by id and marks it as accessed.
func (q *Queue) Get(id string) (*Job, bool) {
	q.mu.Lock()
	j, ok := q.jobs[id]
	q.mu.Unlock()
	if ok {
		j.Touch()
	}
	return j, ok
}

// run executes the ffmpeg process for job, blocking (called from a
// goroutine). Concurrency is bounded by copySem, per
// PROMIN_REMUX_MAX_TRANSCODES's copy-side counterpart (MaxCopyJobs).
func (q *Queue) run(job *Job) {
	defer close(job.done)
	// Transcode is CPU-heavy and gets its own semaphore; copy jobs share the
	// cheap one. Never let a transcode consume a copy slot or vice versa.
	sem := q.copySem
	if job.Kind == KindTranscodeHEVC {
		sem = q.transcodeSem
	}
	// Create + publish the cancel BEFORE waiting on the sem, so a job stuck in
	// the queue can still be interrupted (stopJob/shutdown) — otherwise its
	// cancel is nil until it acquires a slot.
	// Hard wall clock: a source that trickles bytes forever would otherwise
	// hold one of the few ffmpeg slots indefinitely (per-read timeouts reset
	// on every byte). Six hours covers any film plus a slow start.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	job.mu.Lock()
	job.cancel = cancel
	job.mu.Unlock()
	defer cancel()
	select {
	case sem <- struct{}{}:
		metrics.RemuxJobsActive.Inc()
		defer func() { <-sem; metrics.RemuxJobsActive.Dec() }()
	case <-ctx.Done():
		job.setFailed(context.Canceled)
		return
	case <-q.closing:
		job.setFailed(errors.New("remux: queue shutting down"))
		return
	}

	// Multi-audio HLS (separate alternate-audio renditions in a master playlist)
	// is disabled: the bundled hls.js on the TV target never loads the alternate
	// audio group — it fetches only the video variant, so torrents played silent.
	// Fall back to muxing the primary audio track INLINE with video (single
	// variant, always audible — same shape as the working online copy path).
	// Single inline audio track. The multi-audio master variant was removed:
	// hls.js on the TV target never played alternate-audio renditions, so the
	// branch was hard-disabled and only added a second code path to maintain.
	// In-player track switching re-muxes with another AudioIndex instead.
	var args []string
	switch job.Kind {
	case KindCopyHLS:
		args = buildCopyHLSArgs(job.Source, job.AudioIndex, job.OutputDir)
	case KindCopyMKV:
		args = buildCopyMKVHLSArgs(job.Source, job.AudioIndex, job.OutputDir)
	case KindTranscodeHEVC:
		// Tone-map only when the source is HDR AND the ffmpeg build has zscale;
		// otherwise a plain (washed) SDR conversion, which still plays.
		tonemap := job.HDR && q.zscale
		args = buildTranscodeHEVCArgs(job.Source, job.AudioIndex, job.OutputDir, tonemap)
	case KindMux2:
		args = buildMux2HLSArgs(job.Source, job.Source2, job.OutputDir)
	}
	// mux2 inputs start at the offset themselves (the sidecar seeks upstream).
	if job.StartSec > 0 && len(args) > 0 && job.Kind != KindMux2 {
		args = withInputSeek(args, job.StartSec)
	}
	switch job.Kind {
	case KindCopyHLS, KindCopyMKV, KindTranscodeHEVC, KindMux2:
	default:
		job.setFailed(fmt.Errorf("remux: unsupported job kind %q", job.Kind))
		return
	}

	cmd := exec.CommandContext(ctx, q.cfg.FFmpegPath, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		job.setFailed(err)
		return
	}

	q.cfg.Logger.Info("remux: job starting", "job_id", job.ID, "kind", job.Kind, "source", job.Source, "audio_index", job.AudioIndex)
	job.setState(StateRunning)

	if err := cmd.Start(); err != nil {
		job.setFailed(fmt.Errorf("remux: ffmpeg start: %w", err))
		q.cfg.Logger.Error("remux: job failed to start", "job_id", job.ID, "error", err)
		return
	}

	stderrDone := logFFmpegStderr(q.cfg.Logger, job, stderr)
	<-stderrDone // drain stderr fully before Wait() closes the pipe (keeps the error tail)

	if err := cmd.Wait(); err != nil {
		// Cancellation (job killed by cleanup/eviction/shutdown) isn't a real
		// failure worth logging as one, but the state must still leave
		// running: pinUntilDone (httpapi) polls State() to release its torrent
		// reader, and a stopped job that stayed "running" pinned the torrent
		// forever.
		if ctx.Err() != nil {
			job.setFailed(context.Canceled)
			q.cfg.Logger.Info("remux: job stopped", "job_id", job.ID)
			return
		}
		job.setFailed(fmt.Errorf("remux: ffmpeg exited: %w (%s)", err, strings.Join(job.StderrTail(), " | ")))
		q.cfg.Logger.Error("remux: job failed", "job_id", job.ID, "error", err, "stderr", job.StderrTail())
		return
	}

	job.setState(StateReady)
	q.cfg.Logger.Info("remux: job finished", "job_id", job.ID)
}

// StartCleanup runs the background TTL sweeper until ctx is cancelled,
// per docs/backend.md ("чистятся через TTL после последнего
// обращения"). Call it once from main in a goroutine.
func (q *Queue) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			close(q.closing)
			q.killAll()
			return
		case <-ticker.C:
			q.sweep()
		}
	}
}

func (q *Queue) sweep() {
	now := time.Now()
	q.mu.Lock()
	var expired []*Job
	for id, j := range q.jobs {
		if now.Sub(j.idleSince()) >= q.cfg.JobTTL {
			expired = append(expired, j)
			delete(q.jobs, id)
		}
	}
	for key, id := range q.dedup {
		if _, ok := q.jobs[id]; !ok {
			delete(q.dedup, key)
		}
	}
	q.mu.Unlock()

	for _, j := range expired {
		q.cfg.Logger.Info("remux: job TTL expired, cleaning up", "job_id", j.ID, "kind", j.Kind)
		go q.destroy(j)
	}
}

// destroy kills a job already removed from the maps and deletes its output —
// after ffmpeg has actually exited (bounded wait), so a segment it was still
// writing doesn't resurrect the directory.
func (q *Queue) destroy(j *Job) {
	stopJob(j)
	select {
	case <-j.done:
	case <-time.After(10 * time.Second):
	}
	if err := os.RemoveAll(j.OutputDir); err != nil {
		q.cfg.Logger.Warn("remux: failed removing job dir", "job_id", j.ID, "error", err)
	}
}

func (q *Queue) killAll() {
	q.mu.Lock()
	jobs := make([]*Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		jobs = append(jobs, j)
	}
	q.mu.Unlock()
	for _, j := range jobs {
		stopJob(j)
	}
}

// stopJob terminates a running ffmpeg process: SIGTERM via context
// cancellation (exec.CommandContext sends Kill on ctx.Done by default,
// which is fine here — copy jobs are cheap to restart and we don't need a
// graceful-then-force two-step for a headless muxer).
func stopJob(j *Job) {
	j.mu.Lock()
	cancel := j.cancel
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func newJobID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "job_" + hex.EncodeToString(b)
}

func dedupKey(kind Kind, source string, audioIndex int, startSec float64) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%.0f", kind, source, audioIndex, startSec)
}
