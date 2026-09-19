-- Recorded live TV (docs/tv.md, "Recording"). One row per recording; the media
-- itself is an HLS directory under <data>/dvr/<id>/ written by ffmpeg.
-- Per profile, like bookmarks: what one profile records is theirs.
CREATE TABLE IF NOT EXISTS tv_recordings (
  id            TEXT PRIMARY KEY,
  user_id       INTEGER NOT NULL,
  channel_id    TEXT NOT NULL,
  channel_title TEXT NOT NULL DEFAULT '',
  title         TEXT NOT NULL,
  -- Programme bounds from the EPG, already padded (see internal/dvr).
  start_at      INTEGER NOT NULL,
  end_at        INTEGER NOT NULL,
  -- scheduled | recording | done | failed
  state         TEXT NOT NULL,
  error         TEXT NOT NULL DEFAULT '',
  bytes         INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tv_recordings_user ON tv_recordings(user_id, start_at DESC);
CREATE INDEX IF NOT EXISTS idx_tv_recordings_state ON tv_recordings(state, start_at);
