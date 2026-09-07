package store

import (
	"path/filepath"
	"testing"
)

func TestQueueRepoOrderDedupeMovePop(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	u, err := db.Users.Create("a", "h", 1)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	s1, e2, e3 := 1, 2, 3

	a, created, err := db.Queue.Add(u.ID, 100, "movie", nil, nil, 10)
	if err != nil || !created || a.Position != 0 {
		t.Fatalf("add movie: created=%v pos=%d err=%v", created, a.Position, err)
	}
	b, _, _ := db.Queue.Add(u.ID, 200, "tv", &s1, &e2, 11)
	c, _, _ := db.Queue.Add(u.ID, 200, "tv", &s1, &e3, 12)
	if b.Position != 1 || c.Position != 2 {
		t.Fatalf("append order: %d %d", b.Position, c.Position)
	}
	// Dedupe: same movie (NULL season/episode) and same episode return the existing row.
	if dup, created, err := db.Queue.Add(u.ID, 100, "movie", nil, nil, 99); err != nil || created || dup.ID != a.ID {
		t.Fatalf("movie dedupe: created=%v id=%d err=%v", created, dup.ID, err)
	}
	if dup, created, _ := db.Queue.Add(u.ID, 200, "tv", &s1, &e2, 99); created || dup.ID != b.ID {
		t.Fatalf("episode dedupe: created=%v id=%d", created, dup.ID)
	}
	if list, _ := db.Queue.List(u.ID); len(list) != 3 {
		t.Fatalf("want 3 items, got %d", len(list))
	}

	// Move the tail to the head; positions stay dense.
	if err := db.Queue.Move(u.ID, c.ID, 0); err != nil {
		t.Fatalf("move: %v", err)
	}
	list, _ := db.Queue.List(u.ID)
	if list[0].ID != c.ID || list[1].ID != a.ID || list[2].ID != b.ID {
		t.Fatalf("order after move: %d %d %d", list[0].ID, list[1].ID, list[2].ID)
	}
	for i, it := range list {
		if it.Position != i {
			t.Fatalf("position not dense: %+v", list)
		}
	}
	if err := db.Queue.Move(u.ID, 12345, 0); err != ErrNotFound {
		t.Fatalf("move unknown: %v", err)
	}

	head, ok, err := db.Queue.Pop(u.ID)
	if err != nil || !ok || head.ID != c.ID || head.Episode == nil || *head.Episode != 3 {
		t.Fatalf("pop: ok=%v %+v err=%v", ok, head, err)
	}
	if err := db.Queue.Remove(u.ID, a.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := db.Queue.Remove(u.ID, a.ID); err != ErrNotFound {
		t.Fatalf("remove twice: %v", err)
	}
	if list, _ := db.Queue.List(u.ID); len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("after pop+remove: %+v", list)
	}
	if err := db.Queue.Clear(u.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok, _ := db.Queue.Pop(u.ID); ok {
		t.Fatal("pop on empty queue returned an item")
	}
}
