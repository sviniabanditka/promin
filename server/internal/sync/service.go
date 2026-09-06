package sync

import (
	"errors"
	"fmt"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// ErrInvalidMediaType is returned by any method taking a media_type that
// isn't "movie" or "tv".
var ErrInvalidMediaType = errors.New("sync: media_type must be movie or tv")

// ErrNotFound wraps store.ErrNotFound for callers that only import sync.
var ErrNotFound = store.ErrNotFound

const (
	bootstrapHistoryLimit  = 50
	bootstrapTimecodeLimit = 50
	continueWatchingLimit  = 20
)

// Service implements the bookmarks/playlists/history/timecodes/settings
// business logic and publishes a Hub event on every mutation, per
// docs/backend.md
type Service struct {
	bookmarks *store.BookmarksRepo
	playlists *store.PlaylistsRepo
	history   *store.HistoryRepo
	timecodes *store.TimecodesRepo
	settings  *store.SettingsRepo
	hub       *Hub
	now       func() time.Time
}

// NewService builds a Service around db's repositories and hub.
func NewService(db *store.DB, hub *Hub) *Service {
	return &Service{
		bookmarks: db.Bookmarks,
		playlists: db.Playlists,
		history:   db.History,
		timecodes: db.Timecodes,
		settings:  db.Settings,
		hub:       hub,
		now:       time.Now,
	}
}

// Hub exposes the underlying event hub (for the WS handler and catalog
// integration).
func (s *Service) Hub() *Hub { return s.hub }

func validMediaType(t string) bool { return t == "movie" || t == "tv" }

// --- Bookmarks ----------------------------------------------------------

func (s *Service) ListBookmarks(userID int64) ([]BookmarkDTO, error) {
	rows, err := s.bookmarks.List(userID)
	if err != nil {
		return nil, err
	}
	out := make([]BookmarkDTO, 0, len(rows))
	for _, b := range rows {
		out = append(out, BookmarkDTO{TMDBID: b.TMDBID, MediaType: b.MediaType, AddedAt: b.AddedAt})
	}
	return out, nil
}

// AddBookmark upserts a bookmark idempotently. created reports whether it
// was newly inserted (201 vs 200 at the HTTP layer).
func (s *Service) AddBookmark(userID, tmdbID int64, mediaType string) (dto BookmarkDTO, created bool, err error) {
	if !validMediaType(mediaType) {
		return BookmarkDTO{}, false, ErrInvalidMediaType
	}
	now := s.now().Unix()
	created, err = s.bookmarks.Add(userID, tmdbID, mediaType, now)
	if err != nil {
		return BookmarkDTO{}, false, err
	}
	dto = BookmarkDTO{TMDBID: tmdbID, MediaType: mediaType, AddedAt: now}
	if created {
		s.hub.Publish(userID, EventBookmarkAdded, dto)
	}
	return dto, created, nil
}

func (s *Service) RemoveBookmark(userID, tmdbID int64, mediaType string) error {
	if !validMediaType(mediaType) {
		return ErrInvalidMediaType
	}
	if err := s.bookmarks.Remove(userID, tmdbID, mediaType); err != nil {
		return err
	}
	s.hub.Publish(userID, EventBookmarkRemoved, BookmarkDTO{TMDBID: tmdbID, MediaType: mediaType})
	return nil
}

// --- Playlists ------------------------------------------------------------

func (s *Service) ListPlaylists(userID int64) ([]PlaylistDTO, error) {
	rows, err := s.playlists.List(userID)
	if err != nil {
		return nil, err
	}
	out := make([]PlaylistDTO, 0, len(rows))
	for _, p := range rows {
		n, err := s.playlists.ItemsCount(p.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, PlaylistDTO{ID: p.ID, Name: p.Name, ItemsCount: n, UpdatedAt: p.UpdatedAt})
	}
	return out, nil
}

func (s *Service) CreatePlaylist(userID int64, name string) (PlaylistDTO, error) {
	p, err := s.playlists.Create(userID, name, s.now().Unix())
	if err != nil {
		return PlaylistDTO{}, err
	}
	dto := PlaylistDTO{ID: p.ID, Name: p.Name, ItemsCount: 0, UpdatedAt: p.UpdatedAt}
	s.hub.Publish(userID, EventPlaylistCreated, dto)
	return dto, nil
}

func (s *Service) RenamePlaylist(userID, id int64, name string) (PlaylistDTO, error) {
	now := s.now().Unix()
	if err := s.playlists.Rename(userID, id, name, now); err != nil {
		return PlaylistDTO{}, err
	}
	n, err := s.playlists.ItemsCount(id)
	if err != nil {
		return PlaylistDTO{}, err
	}
	dto := PlaylistDTO{ID: id, Name: name, ItemsCount: n, UpdatedAt: now}
	s.hub.Publish(userID, EventPlaylistUpdated, dto)
	return dto, nil
}

func (s *Service) DeletePlaylist(userID, id int64) error {
	if err := s.playlists.Delete(userID, id); err != nil {
		return err
	}
	s.hub.Publish(userID, EventPlaylistUpdated, map[string]any{"id": id, "deleted": true})
	return nil
}

func (s *Service) ListPlaylistItems(userID, playlistID int64) ([]PlaylistItemDTO, error) {
	if _, err := s.playlists.Get(userID, playlistID); err != nil {
		return nil, err
	}
	rows, err := s.playlists.Items(playlistID)
	if err != nil {
		return nil, err
	}
	out := make([]PlaylistItemDTO, 0, len(rows))
	for _, it := range rows {
		out = append(out, PlaylistItemDTO{ID: it.ID, TMDBID: it.TMDBID, MediaType: it.MediaType, Position: it.Position})
	}
	return out, nil
}

func (s *Service) AddPlaylistItem(userID, playlistID, tmdbID int64, mediaType string) (PlaylistItemDTO, error) {
	if !validMediaType(mediaType) {
		return PlaylistItemDTO{}, ErrInvalidMediaType
	}
	if _, err := s.playlists.Get(userID, playlistID); err != nil {
		return PlaylistItemDTO{}, err
	}
	now := s.now().Unix()
	it, err := s.playlists.AddItem(playlistID, tmdbID, mediaType, now)
	if err != nil {
		return PlaylistItemDTO{}, err
	}
	dto := PlaylistItemDTO{ID: it.ID, TMDBID: it.TMDBID, MediaType: it.MediaType, Position: it.Position}
	s.hub.Publish(userID, EventPlaylistItemAdded, map[string]any{"playlist_id": playlistID, "item": dto})
	return dto, nil
}

func (s *Service) RemovePlaylistItem(userID, playlistID, itemID int64) error {
	if _, err := s.playlists.Get(userID, playlistID); err != nil {
		return err
	}
	if err := s.playlists.RemoveItem(playlistID, itemID); err != nil {
		return err
	}
	s.hub.Publish(userID, EventPlaylistItemRemoved, map[string]any{"playlist_id": playlistID, "item_id": itemID})
	return nil
}

// --- History --------------------------------------------------------------

func (s *Service) ListHistory(userID int64, limit, offset int) ([]HistoryItemDTO, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.history.List(userID, limit, offset)
	if err != nil {
		return nil, err
	}
	return toHistoryDTOs(rows), nil
}

func toHistoryDTOs(rows []store.HistoryEntry) []HistoryItemDTO {
	out := make([]HistoryItemDTO, 0, len(rows))
	for _, e := range rows {
		out = append(out, HistoryItemDTO{
			TMDBID: e.TMDBID, MediaType: e.MediaType, Season: e.Season, Episode: e.Episode, WatchedAt: e.WatchedAt,
		})
	}
	return out
}

func (s *Service) AddHistory(userID, tmdbID int64, mediaType string, season, episode *int) (HistoryItemDTO, error) {
	if !validMediaType(mediaType) {
		return HistoryItemDTO{}, ErrInvalidMediaType
	}
	now := s.now().Unix()
	e, err := s.history.Add(store.HistoryEntry{
		UserID: userID, TMDBID: tmdbID, MediaType: mediaType, Season: season, Episode: episode, WatchedAt: now,
	})
	if err != nil {
		return HistoryItemDTO{}, err
	}
	dto := HistoryItemDTO{TMDBID: e.TMDBID, MediaType: e.MediaType, Season: e.Season, Episode: e.Episode, WatchedAt: e.WatchedAt}
	s.hub.Publish(userID, EventHistoryAdded, dto)
	return dto, nil
}

// --- Timecodes --------------------------------------------------------------

func (s *Service) GetTimecode(userID, tmdbID int64, mediaType string, season, episode int) (TimecodeDTO, error) {
	if mediaType != "" && !validMediaType(mediaType) {
		return TimecodeDTO{}, ErrInvalidMediaType
	}
	t, err := s.timecodes.Get(userID, tmdbID, mediaType, season, episode)
	if err != nil {
		return TimecodeDTO{}, err
	}
	return TimecodeDTO{MediaType: t.MediaType, PositionSec: t.PositionSec, DurationSec: t.DurationSec, UpdatedAt: t.UpdatedAt}, nil
}

// LatestTimecode is the user's newest spot on a title across all episodes
// (ok=false when none). Errors are swallowed: it only enriches a card.
func (s *Service) LatestTimecode(userID, tmdbID int64, mediaType string) (store.Timecode, bool) {
	t, ok, err := s.timecodes.LatestForTitle(userID, tmdbID, mediaType)
	if err != nil {
		return store.Timecode{}, false
	}
	return t, ok
}

// UpsertTimecode implements the last-write-wins upsert from
// docs/data-model.md On acceptance, publishes
// timecode_updated with the full new record, per that section's closing
// note. mediaType defaults to "movie" when the client omits it, for
// backward compatibility with clients that predate the media_type
// column (Phase 3 добивание).
func (s *Service) UpsertTimecode(userID, tmdbID int64, mediaType string, season, episode int, positionSec, durationSec float64, updatedAt int64) (TimecodeUpsertResult, error) {
	if mediaType == "" {
		mediaType = "movie"
	}
	if !validMediaType(mediaType) {
		return TimecodeUpsertResult{}, ErrInvalidMediaType
	}
	current, accepted, err := s.timecodes.Upsert(store.Timecode{
		UserID: userID, TMDBID: tmdbID, MediaType: mediaType, Season: season, Episode: episode,
		PositionSec: positionSec, DurationSec: durationSec, UpdatedAt: updatedAt,
	})
	if err != nil {
		return TimecodeUpsertResult{}, fmt.Errorf("upsert timecode: %w", err)
	}
	if accepted {
		s.hub.Publish(userID, EventTimecodeUpdated, TimecodeDTO{
			TMDBID: tmdbID, MediaType: current.MediaType, Season: season, Episode: episode,
			PositionSec: current.PositionSec, DurationSec: current.DurationSec, UpdatedAt: current.UpdatedAt,
		})
	}
	return TimecodeUpsertResult{Accepted: accepted, PositionSec: current.PositionSec, UpdatedAt: current.UpdatedAt}, nil
}

// ListContinueWatching returns the userID's most recent unfinished
// timecodes, for the "continue watching" home shelf (task brief item 5).
func (s *Service) ListContinueWatching(userID int64, limit int) ([]store.Timecode, error) {
	if limit <= 0 {
		limit = continueWatchingLimit
	}
	return s.timecodes.ListContinueWatching(userID, limit)
}

// ListFinished returns the user's finished-title recommendation seeds (see
// store.TimecodesRepo.ListFinished).
func (s *Service) ListFinished(userID int64, limit int) ([]store.Timecode, error) {
	if limit <= 0 {
		limit = continueWatchingLimit
	}
	return s.timecodes.ListFinished(userID, limit)
}

// WatchedSet returns the exclude set (history ∪ bookmarks) for recommendation
// shelves as a lookup map keyed by tmdb id.
func (s *Service) WatchedSet(userID int64) (map[int]bool, error) {
	ids, err := s.history.WatchedTmdbIDs(userID)
	if err != nil {
		return nil, err
	}
	set := make(map[int]bool, len(ids))
	for _, id := range ids {
		set[int(id)] = true
	}
	return set, nil
}

// --- Settings ---------------------------------------------------------------

func (s *Service) GetSettings(userID int64) (map[string]string, error) {
	return s.settings.GetAll(userID)
}

func (s *Service) SetSetting(userID int64, key, value string) error {
	now := s.now().Unix()
	if err := s.settings.Set(userID, key, value, now); err != nil {
		return err
	}
	s.hub.Publish(userID, EventSettingsUpdated, map[string]string{"key": key, "value": value})
	return nil
}

// --- Bootstrap / poll fallback ------------------------------------------

// Bootstrap implements GET /api/v1/sync/bootstrap (docs/api.md
// section 7): a full snapshot for the client to seed its local cache.
func (s *Service) Bootstrap(userID int64) (BootstrapDTO, error) {
	bookmarks, err := s.ListBookmarks(userID)
	if err != nil {
		return BootstrapDTO{}, err
	}
	playlists, err := s.ListPlaylists(userID)
	if err != nil {
		return BootstrapDTO{}, err
	}
	historyRows, err := s.history.List(userID, bootstrapHistoryLimit, 0)
	if err != nil {
		return BootstrapDTO{}, err
	}
	timecodeRows, err := s.timecodes.ListRecent(userID, bootstrapTimecodeLimit)
	if err != nil {
		return BootstrapDTO{}, err
	}
	settings, err := s.settings.GetAll(userID)
	if err != nil {
		return BootstrapDTO{}, err
	}

	timecodes := make([]TimecodeDTO, 0, len(timecodeRows))
	for _, t := range timecodeRows {
		timecodes = append(timecodes, TimecodeDTO{
			TMDBID: t.TMDBID, MediaType: t.MediaType, Season: t.Season, Episode: t.Episode,
			PositionSec: t.PositionSec, DurationSec: t.DurationSec, UpdatedAt: t.UpdatedAt,
		})
	}

	return BootstrapDTO{
		Bookmarks: bookmarks,
		Playlists: playlists,
		History:   toHistoryDTOs(historyRows),
		Timecodes: timecodes,
		Settings:  settings,
		Cursor:    s.hub.Cursor(userID),
	}, nil
}

// PollEvents implements GET /api/v1/sync/events?since=. gone=true means
// the caller must respond 410 and the client must bootstrap instead.
func (s *Service) PollEvents(userID, since int64) (events []Event, cursor int64, gone bool) {
	evs, ok := s.hub.Since(userID, since)
	if !ok {
		return nil, 0, true
	}
	cursor = since
	if len(evs) > 0 {
		cursor = evs[len(evs)-1].ID
	}
	return evs, cursor, false
}

// ClearHistory wipes watch history and resume positions (Settings → Danger
// zone → clear history). Other devices get EventDataCleared{scope:"history"}.
func (s *Service) ClearHistory(userID int64) error {
	if err := s.history.ClearUser(userID); err != nil {
		return err
	}
	if err := s.timecodes.ClearUser(userID); err != nil {
		return err
	}
	s.hub.Publish(userID, EventDataCleared, map[string]string{"scope": "history"})
	return nil
}

// ClearAll wipes everything the profile owns except the profile itself:
// bookmarks, playlists, history, resume positions, synced settings. The caller
// (httpapi) then revokes every session so all devices fall back to the PIN gate.
func (s *Service) ClearAll(userID int64) error {
	for _, f := range []func(int64) error{
		s.bookmarks.ClearUser, s.playlists.ClearUser, s.history.ClearUser, s.timecodes.ClearUser, s.settings.ClearUser,
	} {
		if err := f(userID); err != nil {
			return err
		}
	}
	s.hub.Publish(userID, EventDataCleared, map[string]string{"scope": "all"})
	return nil
}
