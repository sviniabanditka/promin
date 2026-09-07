-- Watch queue (docs/miniapp.md): per-profile ordered list of movies / episodes
-- the phone lines up for the TV. position is a dense 0..n-1 order; the unique
-- index dedupes an item (NULL season/episode = the movie itself; COALESCE
-- because SQLite treats NULLs as distinct in UNIQUE).
CREATE TABLE watch_queue (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tmdb_id    INTEGER NOT NULL,
    media_type TEXT NOT NULL CHECK (media_type IN ('movie','tv')),
    season     INTEGER,
    episode    INTEGER,
    position   INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_watch_queue_user_pos ON watch_queue(user_id, position);
CREATE UNIQUE INDEX idx_watch_queue_uniq ON watch_queue(user_id, tmdb_id, media_type, COALESCE(season, 0), COALESCE(episode, 0));
