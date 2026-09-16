-- Live TV (docs/tv.md): the iptv-org catalogue filtered to the configured
-- countries, its streams with our own liveness verdicts, and per-profile
-- favourites / last watched. Channels and streams are replaced wholesale by the
-- daily sync; favourites survive because they key on the stable iptv-org id.

CREATE TABLE IF NOT EXISTS tv_channels (
  id          TEXT PRIMARY KEY,          -- iptv-org id, e.g. "1plus1.ua"
  name        TEXT NOT NULL,
  country     TEXT NOT NULL,             -- ISO 3166-1 alpha-2
  categories  TEXT NOT NULL DEFAULT '[]',-- JSON array of category ids
  logo        TEXT NOT NULL DEFAULT '',
  website     TEXT NOT NULL DEFAULT '',
  network     TEXT NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS tv_channels_country ON tv_channels(country, name);

CREATE TABLE IF NOT EXISTS tv_streams (
  id          INTEGER PRIMARY KEY,
  channel_id  TEXT NOT NULL REFERENCES tv_channels(id) ON DELETE CASCADE,
  url         TEXT NOT NULL,
  quality     TEXT NOT NULL DEFAULT '',  -- "1080p" … "" unknown
  user_agent  TEXT NOT NULL DEFAULT '',
  referrer    TEXT NOT NULL DEFAULT '',
  alive       INTEGER NOT NULL DEFAULT 1,-- our last check; unknown counts as alive
  fails       INTEGER NOT NULL DEFAULT 0,-- consecutive failed checks
  checked_at  INTEGER NOT NULL DEFAULT 0,
  UNIQUE(channel_id, url)
);
CREATE INDEX IF NOT EXISTS tv_streams_channel ON tv_streams(channel_id, alive);

CREATE TABLE IF NOT EXISTS tv_favorites (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel_id  TEXT NOT NULL,
  added_at    INTEGER NOT NULL,
  PRIMARY KEY (user_id, channel_id)
);

CREATE TABLE IF NOT EXISTS tv_recent (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel_id  TEXT NOT NULL,
  watched_at  INTEGER NOT NULL,
  PRIMARY KEY (user_id, channel_id)
);

-- Sync bookkeeping (when the catalogue was last pulled / checked).
CREATE TABLE IF NOT EXISTS tv_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
