package store

import (
	"path/filepath"
	"testing"
)

// Last-write-wins: a newer position replaces, an older one is rejected and the
// caller is told which row actually stands (so the WS echo carries the truth).
func TestTimecodesUpsertLastWriteWins(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, err := db.Users.Create("a", "h", 1)
	if err != nil {
		t.Fatal(err)
	}
	base := Timecode{UserID: u.ID, TMDBID: 42, MediaType: "tv", Season: 1, Episode: 3}

	first := base
	first.PositionSec, first.DurationSec, first.UpdatedAt = 100, 2000, 1000
	cur, ok, err := db.Timecodes.Upsert(first)
	if err != nil || !ok || cur.PositionSec != 100 {
		t.Fatalf("first write: ok=%v cur=%+v err=%v", ok, cur, err)
	}

	newer := base
	newer.PositionSec, newer.DurationSec, newer.UpdatedAt = 300, 2000, 2000
	cur, ok, err = db.Timecodes.Upsert(newer)
	if err != nil || !ok || cur.PositionSec != 300 {
		t.Fatalf("newer write must win: ok=%v cur=%+v err=%v", ok, cur, err)
	}

	stale := base
	stale.PositionSec, stale.DurationSec, stale.UpdatedAt = 50, 2000, 1500 // older than 2000
	cur, ok, err = db.Timecodes.Upsert(stale)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a stale write must be rejected")
	}
	if cur.PositionSec != 300 || cur.UpdatedAt != 2000 {
		t.Fatalf("rejected write must report the standing row, got %+v", cur)
	}
}
