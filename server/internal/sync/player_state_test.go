package sync

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPlayerStateThrottle(t *testing.T) {
	h := NewHub()
	now := time.Unix(1_788_800_000, 0)
	h.now = func() time.Time { return now }
	ch, cancel := h.Subscribe(1, "tv1", "TV")
	defer cancel()

	st := PlayerState{TMDBID: 1399, MediaType: "tv", Season: 2, Episode: 5, PositionSec: 10}
	h.SetPlayerState(1, "tv1", st)
	st.PositionSec = 11
	now = now.Add(300 * time.Millisecond)
	h.SetPlayerState(1, "tv1", st) // throttled, but stored
	if got := h.PlayerState(1, "tv1"); got == nil || got.PositionSec != 11 || got.UpdatedAt != now.Unix() {
		t.Fatalf("state %+v", got)
	}
	if h.PlayerState(1, "other") != nil || h.PlayerState(2, "tv1") != nil {
		t.Fatal("unknown device must be nil")
	}
	now = now.Add(time.Second)
	st.PositionSec = 12
	h.SetPlayerState(1, "tv1", st) // gap passed → published
	h.ClearPlayerState(1, "tv1")   // closed → always published
	if h.PlayerState(1, "tv1") != nil {
		t.Fatal("cleared state must be nil")
	}

	var got []Event
	for len(ch) > 0 {
		got = append(got, <-ch)
	}
	if len(got) != 3 {
		t.Fatalf("published %d events, want 3 (first, after gap, closed)", len(got))
	}
	pos := func(ev Event) float64 { return ev.Payload.(PlayerStatePayload).PositionSec }
	if got[0].Type != EventPlayerState || pos(got[0]) != 10 || pos(got[1]) != 12 {
		t.Fatalf("events %+v", got)
	}
	b, _ := json.Marshal(got[2].Payload)
	if string(b) != `{"device_id":"tv1","closed":true}` {
		t.Fatalf("closed payload: %s", b)
	}
	b, _ = json.Marshal(got[1].Payload)
	if string(b) != `{"tmdb_id":1399,"media_type":"tv","title":"","season":2,"episode":5,"position_sec":12,"duration_sec":0,"paused":false,"updated_at":1788800001,"device_id":"tv1"}` {
		t.Fatalf("state payload: %s", b)
	}

	// Stale state (TV died) reads as nothing playing.
	h.SetPlayerState(1, "tv1", st)
	now = now.Add(2 * time.Minute)
	if h.PlayerState(1, "tv1") != nil {
		t.Fatal("stale state must be nil")
	}
}
