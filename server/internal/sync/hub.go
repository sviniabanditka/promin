// Package sync implements Promin's cross-device sync: bookmarks,
// playlists, history, timecodes and settings, plus the WS push hub and
// its HTTP long-poll fallback, per docs/backend.md and
// docs/api.md sections 4 and 7.
package sync

import (
	"github.com/sviniabanditka/promin/server/internal/metrics"
	"sort"
	"sync"
	"time"
)

// Event is a single sync event, pushed over WS and/or returned by the
// GET /api/v1/sync/events poller (docs/api.md).
type Event struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Payload any    `json:"payload"`
	at      time.Time
}

// Event types, per docs/backend.md / docs/api.md
// section 7.
const (
	EventBookmarkAdded       = "bookmark_added"
	EventBookmarkRemoved     = "bookmark_removed"
	EventPlaylistCreated     = "playlist_created"
	EventPlaylistUpdated     = "playlist_updated"
	EventPlaylistItemAdded   = "playlist_item_added"
	EventPlaylistItemRemoved = "playlist_item_removed"
	EventTimecodeUpdated     = "timecode_updated"
	EventSettingsUpdated     = "settings_updated"
	EventDataCleared         = "data_cleared" // payload {scope: history|all}
	// EventOpenTitle asks ONE device to open a title (Telegram bot → TV).
	// Payload {tmdb_id, media_type, device_id, title}; every socket of the
	// user receives it and only the device whose id matches acts.
	EventOpenTitle = "open_title"
	// EventRemote is a playback command for ONE device (Telegram remote).
	// Payload {device_id, action, value}: action toggle_play|seek|seek_to|
	// prev|next|mute|night|sleep; value is seconds for seek (±30) and
	// seek_to (absolute), minutes for sleep.
	EventRemote = "remote"
	// EventPlayerState is a TV's "now playing" report for the Mini App
	// (docs/miniapp.md). Payload PlayerStatePayload (player_state.go).
	EventDeviceSettings = "device_settings" // payload {device_id, settings}
	EventPlayerState    = "player_state"
	// EventQueueUpdated carries the whole watch queue ({items}) after any change.
	EventQueueUpdated = "queue_updated"
)

// DeviceInfo identifies a connected device: ID is auth.TokenID of its
// session, Name the session's device_name.
type DeviceInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// eventLogTTL is how long a published event stays in a user's in-memory
// journal for poll-based catch-up (docs/backend.md: "TTL
// порядка часа").
const eventLogTTL = time.Hour

// subscriberBuffer is the per-connection channel size; a slow WS writer
// drops events rather than blocking the publisher (the poll fallback and
// bootstrap cover the gap).
const subscriberBuffer = 32

// Hub fans out sync events to every connected device of a user (WS push)
// and keeps a short per-user journal for the HTTP poll fallback. It knows
// nothing about the WebSocket wire protocol — internal/httpapi's WS
// handler owns the actual connection and reads from the channel returned
// by Subscribe.
type Hub struct {
	mu     sync.Mutex
	nextID int64
	log    map[int64][]Event // userID -> events, newest last
	subs   map[int64]map[chan Event]DeviceInfo
	// players: userID -> deviceID -> last reported player state (player_state.go).
	players map[int64]map[string]*playerEntry
	now     func() time.Time
}

// NewHub builds an empty Hub.
func NewHub() *Hub {
	return &Hub{
		log:     map[int64][]Event{},
		subs:    map[int64]map[chan Event]DeviceInfo{},
		players: map[int64]map[string]*playerEntry{},
		now:     time.Now,
	}
}

// Publish records and fans out an event for userID. It returns the
// stored Event (with its assigned cursor id) so callers can echo it back
// in the triggering REST response if useful.
func (h *Hub) Publish(userID int64, eventType string, payload any) Event {
	h.mu.Lock()
	h.nextID++
	ev := Event{ID: h.nextID, Type: eventType, Payload: payload, at: time.Now()}
	h.log[userID] = pruneLog(append(h.log[userID], ev), ev.at)

	subs := make([]chan Event, 0, len(h.subs[userID]))
	for ch := range h.subs[userID] {
		subs = append(subs, ch)
	}
	h.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			// Slow/stuck subscriber: drop rather than block the publisher.
			// The client falls back to /sync/events or bootstrap.
		}
	}
	return ev
}

func pruneLog(events []Event, now time.Time) []Event {
	cutoff := now.Add(-eventLogTTL)
	i := 0
	for i < len(events) && events[i].at.Before(cutoff) {
		i++
	}
	if i == 0 {
		return events
	}
	return append([]Event(nil), events[i:]...)
}

// Since returns userID's events with id > cursor, for the poll fallback.
// ok is false if cursor predates the oldest event still in the journal
// (client was offline too long) — the caller must respond 410 Gone and
// the client must fall back to a full bootstrap
// (docs/api.md).
func (h *Hub) Since(userID, cursor int64) (events []Event, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	log := pruneLog(h.log[userID], time.Now())
	h.log[userID] = log

	if len(log) > 0 && cursor < log[0].ID-1 {
		return nil, false
	}

	out := make([]Event, 0)
	for _, ev := range log {
		if ev.ID > cursor {
			out = append(out, ev)
		}
	}
	return out, true
}

// Cursor returns the id of the most recent event published for userID (0
// if none), for GET /api/v1/sync/bootstrap's "cursor" field.
func (h *Hub) Cursor(userID int64) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	log := h.log[userID]
	if len(log) == 0 {
		return 0
	}
	return log[len(log)-1].ID
}

// Subscribe registers a live listener (a WS connection) for userID's
// events, tagged with the connecting device so OnlineDevices can list it.
// The caller must call the returned cancel func when the connection closes.
func (h *Hub) Subscribe(userID int64, deviceID, deviceName string) (ch chan Event, cancel func()) {
	ch = make(chan Event, subscriberBuffer)

	h.mu.Lock()
	if h.subs[userID] == nil {
		h.subs[userID] = map[chan Event]DeviceInfo{}
	}
	h.subs[userID][ch] = DeviceInfo{ID: deviceID, Name: deviceName}
	metrics.WSClients.Inc()
	h.mu.Unlock()

	cancel = func() {
		h.mu.Lock()
		delete(h.subs[userID], ch)
		metrics.WSClients.Dec()
		if len(h.subs[userID]) == 0 {
			delete(h.subs, userID)
		}
		h.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

// OnlineDevices lists the devices currently subscribed for userID, one
// entry per device id (a reconnecting TV may briefly hold two sockets),
// sorted by name then id so replies are stable.
func (h *Hub) OnlineDevices(userID int64) []DeviceInfo {
	h.mu.Lock()
	seen := map[string]bool{}
	out := make([]DeviceInfo, 0, len(h.subs[userID]))
	for _, d := range h.subs[userID] {
		if d.ID == "" || seen[d.ID] {
			continue
		}
		seen[d.ID] = true
		out = append(out, d)
	}
	h.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}
