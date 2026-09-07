package sync

import (
	"path/filepath"
	"testing"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// Every queue mutation must fan out the full list as queue_updated (the TV
// keeps only this snapshot), and a duplicate add must stay silent.
func TestQueuePublishesFullList(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	u, _ := db.Users.Create("a", "h", 1)
	hub := NewHub()
	svc := NewService(db, hub)

	ep := 2
	if _, created, err := svc.AddQueue(u.ID, 1, "tv", &ep, &ep); err != nil || !created {
		t.Fatalf("add: created=%v err=%v", created, err)
	}
	if _, created, _ := svc.AddQueue(u.ID, 1, "tv", &ep, &ep); created {
		t.Fatal("duplicate reported as created")
	}
	if _, _, err := svc.AddQueue(u.ID, 2, "movie", &ep, &ep); err != nil {
		t.Fatalf("add movie: %v", err)
	}
	head, ok, err := svc.PopQueue(u.ID)
	if err != nil || !ok || head.TMDBID != 1 {
		t.Fatalf("pop: ok=%v %+v err=%v", ok, head, err)
	}

	evs, _ := hub.Since(u.ID, 0)
	if len(evs) != 3 { // add, add movie, pop — the duplicate published nothing
		t.Fatalf("want 3 queue_updated events, got %d", len(evs))
	}
	last := evs[2].Payload.(map[string]any)["items"].([]QueueItemDTO)
	if evs[2].Type != EventQueueUpdated || len(last) != 1 || last[0].TMDBID != 2 || last[0].Season != nil {
		t.Fatalf("last event: %s %+v", evs[2].Type, last)
	}
	snap, err := svc.Bootstrap(u.ID)
	if err != nil || len(snap.Queue) != 1 {
		t.Fatalf("bootstrap queue: %+v err=%v", snap.Queue, err)
	}
}
