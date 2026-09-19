package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sviniabanditka/promin/server/internal/dvr"
	"github.com/sviniabanditka/promin/server/internal/store"
)

// The routes end to end over a real recording: schedule → the scheduler runs
// ffmpeg → the playlist and its segments come back off the media route with the
// caller's token stamped onto the relative child URIs.
func TestDVRRoutesRecordAndServe(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	src := filepath.Join(t.TempDir(), "channel.ts")
	gen := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=5",
		"-c:v", "libx264", "-preset", "ultrafast", "-t", "5", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("cannot generate a test stream: %v %s", err, out)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, src)
	}))
	defer origin.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	svc := dvr.New(db.Recordings, t.TempDir(), "ffmpeg", 1<<30, func(string) (string, string, string, error) {
		return origin.URL + "/channel.ts", "", "", nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := &dvrHandlers{svc: svc, repo: db.Recordings}

	// A programme that is on right now records at once.
	now := time.Now().Unix()
	body := `{"channel_id":"test.ua","channel_title":"Test","title":"News","start_at":` +
		itoa(now-60) + `,"end_at":` + itoa(now+2) + `}`
	rec := httptest.NewRecorder()
	h.add(rec, httptest.NewRequest("POST", "/api/v1/tv/records", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	var created store.Recording
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("add: decode %v", err)
	}

	// Bad input is refused rather than scheduled.
	for _, bad := range []string{
		`{"channel_id":"../etc","start_at":1,"end_at":2}`,
		`{"channel_id":"test.ua","start_at":0,"end_at":0}`,
		`{"channel_id":"test.ua","start_at":` + itoa(now-7200) + `,"end_at":` + itoa(now-3600) + `}`,
	} {
		r := httptest.NewRecorder()
		h.add(r, httptest.NewRequest("POST", "/api/v1/tv/records", strings.NewReader(bad)))
		if r.Code != http.StatusBadRequest {
			t.Errorf("%s → %d, want 400", bad, r.Code)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go svc.Run(ctx)

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		got, err := db.Recordings.Get(created.ID)
		if err == nil && got.State == store.RecDone {
			break
		}
		if err == nil && got.State == store.RecFailed {
			t.Fatalf("recording failed: %s", got.Error)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// The list shows it for this profile.
	rec = httptest.NewRecorder()
	h.list(rec, httptest.NewRequest("GET", "/api/v1/tv/records", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"News"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	// The playlist comes back with the token stamped onto its segments, so the
	// TV's HLS engine can fetch them through the hard gate.
	req := httptest.NewRequest("GET", "/tv/records/"+created.ID+"/playlist.m3u8?t=secret", nil)
	req.SetPathValue("id", created.ID)
	req.SetPathValue("file", "playlist.m3u8")
	rec = httptest.NewRecorder()
	h.serveFile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("playlist: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "seg-00000.ts?t=secret") {
		t.Fatalf("segments are not tokenised:\n%s", rec.Body)
	}

	// A segment is served as media.
	req = httptest.NewRequest("GET", "/tv/records/"+created.ID+"/seg-00000.ts?t=secret", nil)
	req.SetPathValue("id", created.ID)
	req.SetPathValue("file", "seg-00000.ts")
	rec = httptest.NewRecorder()
	h.serveFile(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("segment: %d (%d bytes)", rec.Code, rec.Body.Len())
	}

	// Nothing else may be read out of the directory.
	for _, file := range []string{"../../../etc/passwd", "seg-00000.ts.bak", "playlist.m3u8x"} {
		req = httptest.NewRequest("GET", "/tv/records/"+created.ID+"/x", nil)
		req.SetPathValue("id", created.ID)
		req.SetPathValue("file", file)
		r := httptest.NewRecorder()
		h.serveFile(r, req)
		if r.Code != http.StatusBadRequest {
			t.Errorf("file %q → %d, want 400", file, r.Code)
		}
	}

	// Delete takes the files with it.
	dir := svc.Dir(created.ID)
	req = httptest.NewRequest("DELETE", "/api/v1/tv/records/"+created.ID, nil)
	req.SetPathValue("id", created.ID)
	rec = httptest.NewRecorder()
	h.remove(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("files survived the delete")
	}
}

// A server without recording says so instead of 500ing.
func TestDVRDisabled(t *testing.T) {
	h := &dvrHandlers{}
	rec := httptest.NewRecorder()
	h.list(rec, httptest.NewRequest("GET", "/api/v1/tv/records", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "dvr_disabled") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
