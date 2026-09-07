package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sviniabanditka/promin/server/internal/remux"
)

// The redirect carries the job's REAL start (the queue may hand back an older
// job covering the requested offset) and the token, so the player can set its
// timeBase from the final URL.
func TestRedirectToPlaylistCarriesJobStart(t *testing.T) {
	h := &torrentHandlers{}
	r := httptest.NewRequest(http.MethodGet, "/stream/ih/0?mkv=false&audio=1&start=1000&t=tok", nil)
	w := httptest.NewRecorder()
	h.redirectToPlaylist(w, r, &remux.Job{ID: "job_a", StartSec: 120})
	loc := w.Header().Get("Location")
	if w.Code != http.StatusFound || !strings.Contains(loc, "/remux/job_a/playlist.m3u8?hls=1&start=120") || !strings.Contains(loc, "t=tok") {
		t.Fatalf("code %d location %q", w.Code, loc)
	}

	w = httptest.NewRecorder()
	h.redirectToPlaylist(w, r, &remux.Job{ID: "job_b"})
	if loc := w.Header().Get("Location"); strings.Contains(loc, "start=") {
		t.Fatalf("start=0 must not be advertised: %q", loc)
	}
}

// The not-ready (hls=1) playlist still exposes X-Remux-Start so the player's
// pre-fetch learns the offset before the first segment exists.
func TestNotReadyPlaylistExposesStart(t *testing.T) {
	h := &remuxHandlers{logger: slog.Default()}
	job := &remux.Job{ID: "job_a", StartSec: 120, OutputDir: t.TempDir()}
	w := httptest.NewRecorder()
	h.servePlaylist(w, job, remux.PlaylistFile, true, "")
	if w.Code != http.StatusOK || w.Header().Get("X-Remux-Start") != "120" || !strings.HasPrefix(w.Body.String(), "#EXTM3U") {
		t.Fatalf("code %d start %q body %q", w.Code, w.Header().Get("X-Remux-Start"), w.Body.String())
	}
}
