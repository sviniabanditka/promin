-- Per-profile feature access (docs/auth.md → admin panel): JSON
-- {"online":bool,"torrents":bool,"youtube":bool,"tv":bool,"tv_countries":[...]}.
-- '' = everything the server offers (existing profiles keep working as before).
ALTER TABLE users ADD COLUMN features TEXT NOT NULL DEFAULT '';

-- Manual EPG mapping from the admin panel (docs/tv.md): pins a channel to a
-- feed channel, or to no guide at all (xmltv_id = ''). Wins over name matching.
CREATE TABLE IF NOT EXISTS tv_epg_overrides (
  channel_id TEXT PRIMARY KEY,
  source     TEXT NOT NULL,
  xmltv_id   TEXT NOT NULL
);

-- Every channel each feed carries (id + display names), so the admin can
-- search for the right one. Replaced per source on every EPG sync.
CREATE TABLE IF NOT EXISTS tv_epg_channels (
  source   TEXT NOT NULL,
  xmltv_id TEXT NOT NULL,
  names    TEXT NOT NULL,   -- display names joined with " | "
  names_lc TEXT NOT NULL,   -- lower-cased (Go, Unicode-aware) for LIKE
  PRIMARY KEY (source, xmltv_id)
);
