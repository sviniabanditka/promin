package store

import (
	"path/filepath"
	"testing"
)

func TestSkipsObserveClustersAndVotes(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Episode 1: the viewer skips 62 → 152.
	if err := db.Skips.Observe(1399, 1, 1, 62, 152); err != nil {
		t.Fatalf("observe: %v", err)
	}
	// One episode is not enough to offer the segment.
	if segs, _ := db.Skips.List(1399, 1, 2); len(segs) != 0 {
		t.Fatalf("one vote should not be offered: %+v", segs)
	}

	// Episode 2 releases the key a few seconds off: same intro, second vote.
	if err := db.Skips.Observe(1399, 1, 2, 68, 158); err != nil {
		t.Fatalf("observe 2: %v", err)
	}
	segs, err := db.Skips.List(1399, 1, 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(segs) != 1 || segs[0].Votes != 2 {
		t.Fatalf("want one segment with 2 votes, got %+v", segs)
	}
	if segs[0].StartSec != 65 || segs[0].EndSec != 155 {
		t.Fatalf("bounds should average to 65..155, got %.1f..%.1f", segs[0].StartSec, segs[0].EndSec)
	}

	// The same episode seeking the intro again nudges the bounds but never
	// votes twice.
	if err := db.Skips.Observe(1399, 1, 2, 65, 155); err != nil {
		t.Fatalf("observe repeat: %v", err)
	}
	segs, _ = db.Skips.List(1399, 1, 2)
	if len(segs) != 1 || segs[0].Votes != 2 {
		t.Fatalf("a repeat from the same episode must not vote: %+v", segs)
	}

	// A jump far from the intro starts its own segment, which stays unoffered
	// until a second episode agrees.
	if err := db.Skips.Observe(1399, 1, 3, 2400, 2460); err != nil {
		t.Fatalf("observe credits: %v", err)
	}
	if segs, _ = db.Skips.List(1399, 1, 2); len(segs) != 1 {
		t.Fatalf("the lone credits jump must not be offered: %+v", segs)
	}
	if all, _ := db.Skips.List(1399, 1, 1); len(all) != 2 {
		t.Fatalf("both clusters should exist: %+v", all)
	}

	// Seasons and shows do not mix.
	if segs, _ = db.Skips.List(1399, 2, 1); len(segs) != 0 {
		t.Fatalf("season 2 borrowed season 1's segments: %+v", segs)
	}
}
