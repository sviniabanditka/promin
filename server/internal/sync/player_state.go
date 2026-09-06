package sync

import "time"

const (
	// playerPublishGap throttles EventPlayerState to one publish per second
	// per device (docs/miniapp.md); the stored state is always the latest.
	playerPublishGap = time.Second
	// playerStateTTL: the TV reports every 5 s while playing, so a state
	// this old belongs to a TV that died mid-playback — treat as nothing playing.
	playerStateTTL = time.Minute
)

// PlayerState is a TV's "now playing" report (docs/miniapp.md).
type PlayerState struct {
	TMDBID      int64   `json:"tmdb_id"`
	MediaType   string  `json:"media_type"`
	Title       string  `json:"title"`
	Season      int     `json:"season,omitempty"`
	Episode     int     `json:"episode,omitempty"`
	PositionSec float64 `json:"position_sec"`
	DurationSec float64 `json:"duration_sec"`
	Paused      bool    `json:"paused"`
	Source      string  `json:"source,omitempty"`
	Voice       string  `json:"voice,omitempty"`
	UpdatedAt   int64   `json:"updated_at"` // set by the server
}

// PlayerStatePayload is the EventPlayerState payload: the state plus the
// reporting device; {device_id, closed:true} when the player closed.
type PlayerStatePayload struct {
	*PlayerState
	DeviceID string `json:"device_id"`
	Closed   bool   `json:"closed,omitempty"`
}

type playerEntry struct {
	state   PlayerState
	lastPub time.Time
}

// SetPlayerState stores deviceID's state and publishes it unless the device
// published less than playerPublishGap ago.
// ponytail: a throttled update is not re-sent later; the next 5 s tick covers it.
func (h *Hub) SetPlayerState(userID int64, deviceID string, st PlayerState) {
	h.mu.Lock()
	now := h.now()
	st.UpdatedAt = now.Unix()
	if h.players[userID] == nil {
		h.players[userID] = map[string]*playerEntry{}
	}
	e := h.players[userID][deviceID]
	if e == nil {
		e = &playerEntry{}
		h.players[userID][deviceID] = e
	}
	e.state = st
	publish := now.Sub(e.lastPub) >= playerPublishGap
	if publish {
		e.lastPub = now
	}
	h.mu.Unlock()
	if publish {
		h.Publish(userID, EventPlayerState, PlayerStatePayload{PlayerState: &st, DeviceID: deviceID})
	}
}

// ClearPlayerState drops deviceID's state (player closed) and always
// publishes {device_id, closed: true}.
func (h *Hub) ClearPlayerState(userID int64, deviceID string) {
	h.mu.Lock()
	delete(h.players[userID], deviceID)
	if len(h.players[userID]) == 0 {
		delete(h.players, userID)
	}
	h.mu.Unlock()
	h.Publish(userID, EventPlayerState, PlayerStatePayload{DeviceID: deviceID, Closed: true})
}

// PlayerState returns deviceID's last report, or nil when nothing is playing
// (never reported, closed, or stale beyond playerStateTTL).
func (h *Hub) PlayerState(userID int64, deviceID string) *PlayerState {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.players[userID][deviceID]
	if e == nil || h.now().Sub(time.Unix(e.state.UpdatedAt, 0)) > playerStateTTL {
		return nil
	}
	st := e.state
	return &st
}
