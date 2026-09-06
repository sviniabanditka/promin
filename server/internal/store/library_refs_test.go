package store

import (
	"path/filepath"
	"testing"
)

// LibraryRefs unions three tables — a wrong column name would only surface at
// runtime, so exercise it against a real migrated database.
func TestLibraryRefsUnionsAllProfiles(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	u1, err := db.Users.Create("a", "h", 1)
	if err != nil {
		t.Fatalf("user a: %v", err)
	}
	u2, err := db.Users.Create("b", "h", 1)
	if err != nil {
		t.Fatalf("user b: %v", err)
	}

	if _, err := db.Bookmarks.Add(u1.ID, 111, "movie", 100); err != nil {
		t.Fatalf("bookmark: %v", err)
	}
	if _, _, err := db.Timecodes.Upsert(Timecode{
		UserID: u2.ID, TMDBID: 222, MediaType: "tv", Season: 1, Episode: 2,
		PositionSec: 30, DurationSec: 100, UpdatedAt: 200,
	}); err != nil {
		t.Fatalf("timecode: %v", err)
	}
	if _, err := db.History.Add(HistoryEntry{UserID: u1.ID, TMDBID: 333, MediaType: "movie", WatchedAt: 300}); err != nil {
		t.Fatalf("history: %v", err)
	}
	// Duplicate across profiles + tables must collapse to one ref.
	if _, err := db.Bookmarks.Add(u2.ID, 333, "movie", 50); err != nil {
		t.Fatalf("dup bookmark: %v", err)
	}

	refs, err := db.History.LibraryRefs(0)
	if err != nil {
		t.Fatalf("LibraryRefs: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("want 3 distinct refs, got %d: %+v", len(refs), refs)
	}
	// Most-recently-touched first: history 333 (300) > timecode 222 (200) > bookmark 111 (100).
	if refs[0].TMDBID != 333 || refs[1].TMDBID != 222 || refs[2].TMDBID != 111 {
		t.Errorf("wrong order: %+v", refs)
	}
	if refs[1].MediaType != "tv" {
		t.Errorf("media_type lost: %+v", refs[1])
	}

	// The limit is honoured.
	if got, err := db.History.LibraryRefs(2); err != nil || len(got) != 2 {
		t.Errorf("limit ignored: %d refs err=%v", len(got), err)
	}
}
