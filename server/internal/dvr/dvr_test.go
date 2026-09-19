package dvr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

func testService(t *testing.T, source StreamSource) (*Service, *store.DB, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(db.Recordings, dir, "ffmpeg", 1<<30, source, logger), db, dir
}

func TestSchedulePadsAndClamps(t *testing.T) {
	svc, db, _ := testService(t, nil)
	now := time.Now().Unix()

	// A programme in the future keeps its padding on both ends.
	rec, err := svc.Schedule(1, "ch.ua", "Channel", "Film", now+3600, now+7200)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if rec.StartAt != now+3600-PadBeforeSec || rec.EndAt != now+7200+PadAfterSec {
		t.Fatalf("padding: %d..%d", rec.StartAt-now, rec.EndAt-now)
	}

	// A programme already on air starts now, not in the past.
	rec, _ = svc.Schedule(1, "ch.ua", "Channel", "Live", now-1800, now+600)
	if rec.StartAt < now-1 || rec.StartAt > now+1 {
		t.Fatalf("a running programme should start now, got %d", rec.StartAt-now)
	}

	// Nothing may record for longer than the ceiling.
	rec, _ = svc.Schedule(1, "ch.ua", "Channel", "Marathon", now+60, now+48*3600)
	if rec.EndAt-rec.StartAt != maxDurationSec {
		t.Fatalf("duration clamp: %d", rec.EndAt-rec.StartAt)
	}

	if _, err := svc.Schedule(1, "ch.ua", "Channel", "Backwards", now+7200, now+3600); err == nil {
		t.Fatal("end before start should be refused")
	}

	list, err := db.Recordings.List(1)
	if err != nil || len(list) != 3 {
		t.Fatalf("stored %d rows (err %v)", len(list), err)
	}
	if list[0].State != store.RecScheduled {
		t.Fatalf("state: %s", list[0].State)
	}
}

func TestFfmpegArgsCarryHeadersAndBound(t *testing.T) {
	args := strings.Join(ffmpegArgs("http://x/live.m3u8", "Mozilla", "http://ref/", "/data/dvr/abc", 120), " ")
	for _, want := range []string{
		"-user_agent Mozilla",
		"Referer: http://ref/",
		"-t 120",
		"-c copy",
		"/data/dvr/abc/seg-%05d.ts",
		"/data/dvr/abc/" + PlaylistFile,
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
	// No headers → no empty flags that ffmpeg would choke on.
	plain := strings.Join(ffmpegArgs("http://x/live.m3u8", "", "", "/tmp/d", 10), " ")
	if strings.Contains(plain, "-user_agent") || strings.Contains(plain, "-headers") {
		t.Errorf("empty headers should be omitted: %s", plain)
	}
}

// The real thing: ffmpeg records a generated "channel" into an HLS directory
// the player could open, and the row ends up done with a size.
func TestRecordsAChannelEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	src := filepath.Join(t.TempDir(), "channel.ts")
	gen := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-t", "6", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("cannot generate a test stream: %v %s", err, out)
	}

	// Serve it over HTTP: the recorder's reconnect flags are http-protocol
	// options, so a local path would make ffmpeg refuse the command line —
	// exactly what a live channel never is.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, src)
	}))
	defer srv.Close()

	svc, db, _ := testService(t, func(string) (string, string, string, error) {
		return srv.URL + "/channel.ts", "", "", nil
	})
	now := time.Now().Unix()
	rec, err := svc.Schedule(7, "test.ua", "Test", "Programme", now, now+2)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go svc.Run(ctx)

	deadline := time.Now().Add(25 * time.Second)
	var got store.Recording
	for time.Now().Before(deadline) {
		got, err = db.Recordings.Get(rec.ID)
		if err == nil && (got.State == store.RecDone || got.State == store.RecFailed) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if got.State != store.RecDone {
		t.Fatalf("state %q (%s)", got.State, got.Error)
	}
	if got.Bytes <= 0 {
		t.Fatalf("recording has no size")
	}
	playlist, err := os.ReadFile(filepath.Join(svc.Dir(rec.ID), PlaylistFile))
	if err != nil {
		t.Fatalf("playlist: %v", err)
	}
	if !strings.Contains(string(playlist), "seg-00000.ts") {
		t.Fatalf("playlist has no segments:\n%s", playlist)
	}
	if !strings.Contains(string(playlist), "#EXT-X-ENDLIST") {
		t.Fatalf("a finished recording must be closed with ENDLIST:\n%s", playlist)
	}

	// Deleting takes the files with it.
	if err := svc.Delete(rec.ID, 7); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(svc.Dir(rec.ID)); !os.IsNotExist(err) {
		t.Fatalf("directory survived the delete")
	}
}
