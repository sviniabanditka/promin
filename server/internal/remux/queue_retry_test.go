package remux

import (
	"log/slog"
	"testing"
	"time"
)

// A FAILED job must not pin its dedup key for the whole JobTTL: the next
// Submit for the same source gets a fresh job (a cold torrent's first ffprobe
// timeout used to lock the retry out for 30 minutes).
func TestSubmitAfterFailedJobStartsFresh(t *testing.T) {
	q, err := NewQueue(Config{
		DataDir:       t.TempDir(),
		FFmpegPath:    "/nonexistent/ffmpeg", // every job fails fast
		FFprobePath:   "/nonexistent/ffprobe",
		MaxTranscodes: 1,
		MaxCopyJobs:   1,
		JobTTL:        time.Minute,
		Logger:        slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	j1, err := q.Submit(KindCopyHLS, "http://example.invalid/a.m3u8", 0)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for j1.State() != StateFailed {
		if time.Now().After(deadline) {
			t.Fatalf("job never failed without ffmpeg (state %s)", j1.State())
		}
		time.Sleep(20 * time.Millisecond)
	}
	j2, err := q.Submit(KindCopyHLS, "http://example.invalid/a.m3u8", 0)
	if err != nil {
		t.Fatal(err)
	}
	if j2.ID == j1.ID {
		t.Fatal("resubmit after failure returned the dead job")
	}
}
