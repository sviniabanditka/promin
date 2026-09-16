-- Our own display name for a channel (admin panel). Kept in its own table so
-- the daily catalogue rebuild cannot lose it; copied onto tv_channels.title
-- after every rebuild and on every edit. '' = show the catalogue name.
CREATE TABLE IF NOT EXISTS tv_channel_titles (
  channel_id TEXT PRIMARY KEY,
  title      TEXT NOT NULL
);
ALTER TABLE tv_channels ADD COLUMN title TEXT NOT NULL DEFAULT '';
