package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sviniabanditka/promin/server/internal/store"
)

func TestObservableSkip(t *testing.T) {
	cases := []struct {
		name     string
		from, to float64
		want     bool
	}{
		{"intro", 60, 150, true},
		{"backwards", 150, 60, false},
		{"too short", 60, 65, false},
		{"too long", 60, 400, false},
		{"too late in the episode", 1200, 1300, false},
		{"negative", -5, 100, false},
		{"at the window edge", skipObserveWindowSec, skipObserveWindowSec + 60, true},
	}
	for _, c := range cases {
		if got := observableSkip(c.from, c.to); got != c.want {
			t.Errorf("%s: observableSkip(%.0f, %.0f) = %v, want %v", c.name, c.from, c.to, got, c.want)
		}
	}
}

// The HTTP surface end to end over a real (temp) database: a jump from one
// episode stays invisible, a second agreeing episode turns it into a segment.
func TestSkipsHandlersLearnOverHTTP(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	h := &skipHandlers{repo: db.Skips}

	observe := func(episode int, from, to float64) int {
		body := fmt.Sprintf(`{"tmdb_id":1399,"season":1,"episode":%d,"from":%f,"to":%f}`, episode, from, to)
		rec := httptest.NewRecorder()
		h.observe(rec, httptest.NewRequest("POST", "/api/v1/skips/observe", strings.NewReader(body)))
		return rec.Code
	}
	list := func() []skipSegmentDTO {
		rec := httptest.NewRecorder()
		h.list(rec, httptest.NewRequest("GET", "/api/v1/skips?tmdb_id=1399&season=1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("list: status %d", rec.Code)
		}
		var out struct {
			Segments []skipSegmentDTO `json:"segments"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("list: decode %v", err)
		}
		return out.Segments
	}

	if code := observe(1, 60, 150); code != http.StatusNoContent {
		t.Fatalf("observe episode 1: status %d", code)
	}
	if segs := list(); len(segs) != 0 {
		t.Fatalf("a single episode must not produce a segment: %+v", segs)
	}
	// A jump that is not intro-shaped is accepted and dropped.
	if code := observe(2, 60, 62); code != http.StatusNoContent {
		t.Fatalf("short jump: status %d", code)
	}
	if code := observe(2, 64, 154); code != http.StatusNoContent {
		t.Fatalf("observe episode 2: status %d", code)
	}
	segs := list()
	if len(segs) != 1 || segs[0].Votes != 2 {
		t.Fatalf("want one two-vote segment, got %+v", segs)
	}
	if segs[0].Start != 62 || segs[0].End != 152 {
		t.Fatalf("bounds should average, got %.1f..%.1f", segs[0].Start, segs[0].End)
	}

	// Missing tmdb_id is a bad request, not an empty list.
	rec := httptest.NewRecorder()
	h.list(rec, httptest.NewRequest("GET", "/api/v1/skips?season=1", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no tmdb_id: status %d", rec.Code)
	}
}
