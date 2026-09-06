package logbuf

import (
	"log/slog"
	"testing"
)

// The ring must wrap: after size+2 records History returns the newest `size`
// in chronological order with monotonically increasing seq.
func TestBufferRingWrapKeepsNewestInOrder(t *testing.T) {
	b := NewBuffer(3)
	for i := 1; i <= 5; i++ {
		b.add(Record{Level: "INFO", Msg: "m" + string(rune('0'+i))})
	}
	got := b.History(0, slog.LevelDebug, "")
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	want := []string{"m3", "m4", "m5"}
	for i, r := range got {
		if r.Msg != want[i] {
			t.Errorf("[%d] = %s, want %s", i, r.Msg, want[i])
		}
		if i > 0 && got[i].Seq <= got[i-1].Seq {
			t.Errorf("seq not increasing at %d", i)
		}
	}
	if lim := b.History(1, slog.LevelDebug, ""); len(lim) != 1 || lim[0].Msg != "m5" {
		t.Fatalf("limit=1 must return the newest, got %+v", lim)
	}
}
