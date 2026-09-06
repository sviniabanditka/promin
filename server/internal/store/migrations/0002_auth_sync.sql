-- Phase 3: auth + sync tables, verbatim per docs/data-model.md section 2
-- (users, sessions, bookmarks, playlists, playlist_items, history,
-- timecodes, user_settings). tmdb_cache already landed in 0001_init.sql;
-- torrent_cache_meta is deferred to the torrent-module phase.

CREATE TABLE users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    login      TEXT NOT NULL UNIQUE,
    pass_hash  TEXT NOT NULL,           -- PHC string (argon2id)
    created_at INTEGER NOT NULL         -- unix seconds
);

-- Sessions = devices (one row = one login on one device).
CREATE TABLE sessions (
    token       TEXT PRIMARY KEY,       -- opaque, base64url(32 random bytes)
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_name TEXT NOT NULL DEFAULT '',
    device_type TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions(user_id);

-- Bookmarks ("избранное").
CREATE TABLE bookmarks (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tmdb_id    INTEGER NOT NULL,
    media_type TEXT NOT NULL CHECK (media_type IN ('movie','tv')),
    added_at   INTEGER NOT NULL,
    PRIMARY KEY (user_id, tmdb_id, media_type)
);
CREATE INDEX idx_bookmarks_user_added ON bookmarks(user_id, added_at DESC);

-- Playlists (user-curated collections).
CREATE TABLE playlists (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_playlists_user ON playlists(user_id);

CREATE TABLE playlist_items (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    tmdb_id     INTEGER NOT NULL,
    media_type  TEXT NOT NULL CHECK (media_type IN ('movie','tv')),
    position    INTEGER NOT NULL DEFAULT 0,
    added_at    INTEGER NOT NULL,
    UNIQUE (playlist_id, tmdb_id, media_type)
);
CREATE INDEX idx_playlist_items_playlist ON playlist_items(playlist_id, position);

-- Watch history (append-only log of "watched an episode/movie" facts).
CREATE TABLE history (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tmdb_id    INTEGER NOT NULL,
    media_type TEXT NOT NULL CHECK (media_type IN ('movie','tv')),
    season     INTEGER,                 -- NULL for movies
    episode    INTEGER,                 -- NULL for movies
    watched_at INTEGER NOT NULL
);
CREATE INDEX idx_history_user_watched ON history(user_id, watched_at DESC);
CREATE INDEX idx_history_user_title ON history(user_id, tmdb_id, media_type);

-- "Resume playback" timecodes — one live row per (user, title, season, episode).
CREATE TABLE timecodes (
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tmdb_id       INTEGER NOT NULL,
    season        INTEGER NOT NULL DEFAULT 0,  -- 0 for movies
    episode       INTEGER NOT NULL DEFAULT 0,  -- 0 for movies
    position_sec  REAL NOT NULL,
    duration_sec  REAL NOT NULL,
    updated_at    INTEGER NOT NULL,
    PRIMARY KEY (user_id, tmdb_id, season, episode)
);
CREATE INDEX idx_timecodes_user_updated ON timecodes(user_id, updated_at DESC);

-- Per-user key/value settings (UI language, autoplay, per-device capabilities...).
CREATE TABLE user_settings (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, key)
);
