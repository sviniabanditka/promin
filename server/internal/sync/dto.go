package sync

// DTOs mirror docs/api.md sections 4 and 7 field-for-field so
// internal/httpapi can serialize Service's return values directly.

type BookmarkDTO struct {
	TMDBID    int64  `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	AddedAt   int64  `json:"added_at"`
}

type PlaylistDTO struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ItemsCount int    `json:"items_count"`
	UpdatedAt  int64  `json:"updated_at"`
}

type PlaylistItemDTO struct {
	ID        int64  `json:"id"`
	TMDBID    int64  `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	Position  int    `json:"position"`
}

type HistoryItemDTO struct {
	TMDBID    int64  `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	Season    *int   `json:"season"`
	Episode   *int   `json:"episode"`
	WatchedAt int64  `json:"watched_at"`
}

type TimecodeDTO struct {
	TMDBID      int64   `json:"tmdb_id,omitempty"`
	MediaType   string  `json:"media_type,omitempty"`
	Season      int     `json:"season,omitempty"`
	Episode     int     `json:"episode,omitempty"`
	PositionSec float64 `json:"position_sec"`
	DurationSec float64 `json:"duration_sec"`
	UpdatedAt   int64   `json:"updated_at"`
}

// TimecodeUpsertResult is the response for POST /api/v1/timecodes
// (docs/api.md): Accepted is false if the write lost
// the last-write-wins conflict, in which case PositionSec/UpdatedAt are
// the server's current values so the client can correct its cache.
type TimecodeUpsertResult struct {
	Accepted    bool    `json:"accepted"`
	PositionSec float64 `json:"position_sec"`
	UpdatedAt   int64   `json:"updated_at"`
}

// BootstrapDTO is the response for GET /api/v1/sync/bootstrap
// (docs/api.md).
type BootstrapDTO struct {
	Bookmarks []BookmarkDTO     `json:"bookmarks"`
	Playlists []PlaylistDTO     `json:"playlists"`
	History   []HistoryItemDTO  `json:"history"`
	Timecodes []TimecodeDTO     `json:"timecodes"`
	Settings  map[string]string `json:"settings"`
	Cursor    int64             `json:"cursor"`
}
