-- Phase 1: only the TMDB response cache is needed. Auth/sync/torrent tables
-- (users, sessions, bookmarks, playlists, playlist_items, history,
-- timecodes, user_settings, torrent_cache_meta) are defined in full in
-- docs/data-model.md but are added in their own migrations in later
-- phases, once the corresponding subsystem lands.

CREATE TABLE tmdb_cache (
    key        TEXT PRIMARY KEY,
    json       TEXT NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX idx_tmdb_cache_expires ON tmdb_cache(expires_at);
