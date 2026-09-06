package sync

import (
	"testing"
	"time"
)

// Since is the poll fallback: it must hand back exactly the events after the
// cursor, and say "gone" (→ 410, full bootstrap) when the cursor predates the
// journal — never silently skip events.
func TestHubSinceCursorSemantics(t *testing.T) {
	h := NewHub()
	h.Publish(1, "a", nil)
	h.Publish(2, "other-user", nil)
	e2 := h.Publish(1, "b", nil)
	h.Publish(1, "c", nil)

	all, ok := h.Since(1, 0)
	if !ok || len(all) != 3 {
		t.Fatalf("Since(0): ok=%v n=%d", ok, len(all))
	}
	tail, ok := h.Since(1, e2.ID)
	if !ok || len(tail) != 1 || tail[0].Type != "c" {
		t.Fatalf("Since(after b): ok=%v %+v", ok, tail)
	}
	none, ok := h.Since(1, 999)
	if !ok || len(none) != 0 {
		t.Fatalf("Since(future cursor): ok=%v n=%d", ok, len(none))
	}
	// Ids are global, journals per user: user 2's journal starts at id 2, so a
	// cursor of 1 (its bootstrap cursor) yields exactly its own event.
	if got, ok := h.Since(2, 1); !ok || len(got) != 1 || got[0].Type != "other-user" {
		t.Fatalf("user isolation broken: ok=%v %+v", ok, got)
	}
}

func TestHubSinceReportsGoneAfterPrune(t *testing.T) {
	h := NewHub()
	h.Publish(1, "old", nil)
	h.Publish(1, "old2", nil)
	// Age the journal past the TTL, then add one fresh event.
	h.mu.Lock()
	for i := range h.log[1] {
		h.log[1][i].at = time.Now().Add(-eventLogTTL - time.Minute)
	}
	h.mu.Unlock()
	fresh := h.Publish(1, "fresh", nil)

	if _, ok := h.Since(1, 0); ok {
		t.Fatal("cursor 0 predates the pruned journal — must be gone (410)")
	}
	got, ok := h.Since(1, fresh.ID-1)
	if !ok || len(got) != 1 || got[0].Type != "fresh" {
		t.Fatalf("cursor at the journal head must still work: ok=%v %+v", ok, got)
	}
}
