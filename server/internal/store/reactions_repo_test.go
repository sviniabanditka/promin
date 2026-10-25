package store

import (
	"path/filepath"
	"testing"
)

func TestReactionsSetListClear(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.Reactions.Set(1, 100, "movie", 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := db.Reactions.Set(1, 200, "tv", -1, 20); err != nil {
		t.Fatal(err)
	}
	// Flip the like to a dislike: still one row.
	if err := db.Reactions.Set(1, 100, "movie", -1, 30); err != nil {
		t.Fatal(err)
	}
	list, err := db.Reactions.List(1)
	if err != nil || len(list) != 2 || list[0].TMDBID != 100 || list[0].Value != -1 {
		t.Fatalf("list after flip: %+v %v", list, err)
	}
	// value 0 removes.
	if err := db.Reactions.Set(1, 100, "movie", 0, 40); err != nil {
		t.Fatal(err)
	}
	if list, _ = db.Reactions.List(1); len(list) != 1 || list[0].TMDBID != 200 {
		t.Fatalf("list after unset: %+v", list)
	}
	if err := db.Reactions.ClearUser(1); err != nil {
		t.Fatal(err)
	}
	if list, _ = db.Reactions.List(1); len(list) != 0 {
		t.Fatalf("clear: %+v", list)
	}
}
