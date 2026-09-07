package remux

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeFFmpeg is an "ffmpeg" that just sleeps, so a job sits in StateRunning
// until the queue kills it (exec: no sh child holding the stderr pipe open).
func fakeFFmpeg(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestQueue(t *testing.T) *Queue {
	t.Helper()
	q, err := NewQueue(Config{
		DataDir:       t.TempDir(),
		FFmpegPath:    fakeFFmpeg(t),
		FFprobePath:   "/nonexistent/ffprobe",
		MaxTranscodes: 2,
		MaxCopyJobs:   4,
		JobTTL:        time.Minute,
		Logger:        slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.killAll)
	return q
}

func waitState(t *testing.T, j *Job, want State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for j.State() != want {
		if time.Now().After(deadline) {
			t.Fatalf("job %s: state %s, want %s", j.ID, j.State(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func writePlaylist(t *testing.T, j *Job, segs int) {
	t.Helper()
	body := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:EVENT\n"
	for i := 0; i < segs; i++ {
		body += "#EXTINF:6.000000,\nseg-000000.ts\n"
	}
	if err := os.WriteFile(j.PlaylistPath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDedupKeyIncludesStart(t *testing.T) {
	a := dedupKey(KindCopyMKV, "src", 0, 100)
	if a == dedupKey(KindCopyMKV, "src", 0, 200) || a == dedupKey(KindCopyMKV, "src", 1, 100) ||
		a == dedupKey(KindTranscodeHEVC, "src", 0, 100) || a == dedupKey(KindCopyMKV, "other", 0, 100) {
		t.Fatal("dedup key must include kind, source, audio and start")
	}
	if a != dedupKey(KindCopyMKV, "src", 0, 100.4) {
		t.Fatal("start is rounded to whole seconds in the key")
	}
}

func TestMuxedSec(t *testing.T) {
	j := &Job{OutputDir: t.TempDir()}
	if got := j.MuxedSec(); got != 0 {
		t.Fatalf("no playlist: got %v, want 0", got)
	}
	writePlaylist(t, j, 10)
	if got := j.MuxedSec(); got != 60 {
		t.Fatalf("10 × 6s segments: got %v, want 60", got)
	}
}

// A start=N request for a track whose live job already muxed past N reuses that
// job (audio switch back / seek back before the current job's start is instant);
// a request past its edge, or for another track, gets its own job.
func TestSubmitReusesCoveringJob(t *testing.T) {
	q := newTestQueue(t)
	src := "http://127.0.0.1/stream/ih/0?t=tok"
	j1, err := q.SubmitCopyMKVFrom(src, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, j1, StateRunning)
	writePlaylist(t, j1, 10) // covers 0..60s

	same, _ := q.SubmitCopyMKVFrom(src, 0, nil, 0)
	if same.ID != j1.ID {
		t.Fatal("exact key must dedup")
	}
	covered, _ := q.SubmitCopyMKVFrom(src, 0, nil, 40)
	if covered.ID != j1.ID {
		t.Fatalf("start=40 inside 0..60 must reuse %s, got %s", j1.ID, covered.ID)
	}
	edge, _ := q.SubmitCopyMKVFrom(src, 0, nil, 55)
	if edge.ID == j1.ID {
		t.Fatal("start=55 within the 10s margin of the edge must start a new job")
	}
	j1.setState(StateReady)
	far, _ := q.SubmitCopyMKVFrom(src, 0, nil, 5000)
	if far.ID != j1.ID {
		t.Fatal("a finished job covers everything past its start")
	}
	other, _ := q.SubmitCopyMKVFrom(src, 1, nil, 40)
	if other.ID == j1.ID || other.ID == edge.ID {
		t.Fatal("another audio track is never covered")
	}
}

// Copy jobs keep one sibling (quick switch back) and evict the least recently
// accessed beyond that; a transcode evicts its sibling at once. Evicted jobs
// leave the queue and reach a terminal state so pinUntilDone releases.
func TestPerSourceCapEvictsLRUSibling(t *testing.T) {
	q := newTestQueue(t)
	src := "http://127.0.0.1/stream/ih/0?t=tok"
	j1, _ := q.SubmitCopyMKVFrom(src, 0, nil, 0)
	j2, _ := q.SubmitCopyMKVFrom(src, 0, nil, 1000)
	waitState(t, j1, StateRunning)
	waitState(t, j2, StateRunning)
	time.Sleep(5 * time.Millisecond)
	j1.Touch() // j2 is now the least recently accessed

	j3, _ := q.SubmitCopyMKVFrom(src, 0, nil, 2000)
	if j3.ID == j1.ID || j3.ID == j2.ID {
		t.Fatal("expected a fresh job")
	}
	if _, ok := q.Get(j2.ID); ok {
		t.Fatal("LRU sibling j2 must be evicted")
	}
	if _, ok := q.Get(j1.ID); !ok {
		t.Fatal("recently used sibling j1 must survive (cap 2)")
	}
	waitState(t, j2, StateFailed) // killed → terminal, not stuck in running
	if _, err := os.Stat(j2.OutputDir); !os.IsNotExist(err) {
		// destroy() runs async after the process exits; give it a moment.
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(j2.OutputDir); os.IsNotExist(err) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("evicted job dir not removed")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	t1, _ := q.SubmitTranscodeFrom(src, 0, false, nil, 0)
	waitState(t, t1, StateRunning)
	t2, _ := q.SubmitTranscodeFrom(src, 1, false, nil, 600)
	if t2.ID == t1.ID {
		t.Fatal("expected a fresh transcode job")
	}
	if _, ok := q.Get(t1.ID); ok {
		t.Fatal("transcode cap is 1: the abandoned sibling must be evicted")
	}
	waitState(t, t1, StateFailed)
}
