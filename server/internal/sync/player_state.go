package sync

import (
	"github.com/sviniabanditka/promin/server/internal/metrics"
	stdsync "sync"
	"time"
)

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
	// Audio/subtitle menus as the TV player shows them (Mini App remote picks
	// by id: remote set_voice / set_subtitle). The TV sends the lists only when
	// they change or every 30 s (request flag `lists`); the server keeps the
	// last ones per device.
	Voices     []PlayerVoice    `json:"voices,omitempty"`
	VoiceID    string           `json:"voice_id,omitempty"`
	Subtitles  []PlayerSubtitle `json:"subtitles,omitempty"`
	SubtitleID string           `json:"subtitle_id,omitempty"` // "off" when none
	Volume     int              `json:"volume,omitempty"`      // 0..100
	Muted      bool             `json:"muted,omitempty"`
	UpdatedAt  int64            `json:"updated_at"` // set by the server
}

// PlayerVoice is one audio choice: a source dub (id = voice id) or an
// in-stream track (id = "track:<n>").
type PlayerVoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PlayerSubtitle is one subtitle choice: "off", a sidecar file ("sub:<n>")
// or an in-manifest text track ("hls:<n>").
type PlayerSubtitle struct {
	ID    string `json:"id"`
	Label string `json:"label"`
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
	h.updatePlayingGauge(now)
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
	h.updatePlayingGauge(h.now())
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

// ---- device-local TV settings (reported by the TV, read by the Mini App) ----

// DeviceSettingKeys are the TV settings that live on the device, not in the
// profile: the Mini App shows and flips them per device via remote set_local.
var DeviceSettingKeys = map[string]bool{"legacy_tv_mode": true, "reduce_motion": true, "debug_mode": true}

type deviceKey struct {
	user   int64
	device string
}

var (
	devSettingsMu stdsync.Mutex
	devSettings   = map[deviceKey]map[string]string{}
)

// SetDeviceSettings stores the device's local settings (whitelisted keys only).
func (h *Hub) SetDeviceSettings(userID int64, deviceID string, settings map[string]string) {
	clean := map[string]string{}
	for k, v := range settings {
		if DeviceSettingKeys[k] {
			clean[k] = v
		}
	}
	devSettingsMu.Lock()
	devSettings[deviceKey{userID, deviceID}] = clean
	devSettingsMu.Unlock()
	h.Publish(userID, EventDeviceSettings, map[string]any{"device_id": deviceID, "settings": clean})
}

// DeviceSettings returns the last reported settings of the device (nil = unknown).
func (h *Hub) DeviceSettings(userID int64, deviceID string) map[string]string {
	devSettingsMu.Lock()
	defer devSettingsMu.Unlock()
	return devSettings[deviceKey{userID, deviceID}]
}

// updatePlayingGauge: devices currently playing (not paused, fresh). Caller holds h.mu.
func (h *Hub) updatePlayingGauge(now time.Time) {
	n := 0
	for uid, devs := range h.players {
		for dev, e := range devs {
			age := now.Sub(time.Unix(e.state.UpdatedAt, 0))
			// A device that has not reported for a day is gone (token revoked,
			// TV retired); drop it so the map cannot grow with dead sessions.
			if age > 24*time.Hour {
				delete(devs, dev)
				continue
			}
			if !e.state.Paused && age <= playerStateTTL {
				n++
			}
		}
		if len(devs) == 0 {
			delete(h.players, uid)
		}
	}
	metrics.PlayerDevicesPlaying.Set(float64(n))
}
