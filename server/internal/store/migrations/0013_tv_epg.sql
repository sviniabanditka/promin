-- Live TV programme guide (docs/tv.md): programmes matched from public XMLTV
-- feeds onto our iptv-org channels. Rebuilt wholesale twice a day; only a
-- short window (yesterday evening .. +3 days) is kept.
ALTER TABLE tv_channels ADD COLUMN alt_names TEXT NOT NULL DEFAULT '[]'; -- iptv-org alt_names (native spellings) for EPG matching
ALTER TABLE tv_channels ADD COLUMN epg_id TEXT NOT NULL DEFAULT '';     -- "<source>:<xmltv channel id>" it was matched to, '' = no guide

CREATE TABLE IF NOT EXISTS tv_programs (
  channel_id TEXT NOT NULL,   -- our tv_channels.id
  start      INTEGER NOT NULL, -- unix seconds
  stop       INTEGER NOT NULL,
  title      TEXT NOT NULL,
  descr      TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (channel_id, start)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS tv_programs_stop ON tv_programs(stop, start);
