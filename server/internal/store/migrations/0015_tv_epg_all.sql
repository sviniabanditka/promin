-- The guide is stored per FEED channel (every channel of every feed within the
-- window), and our channels point at one via tv_channels.epg_id =
-- "<source>:<xmltv id>". Mapping a channel by hand is then a plain UPDATE with
-- the guide available at once — no re-download.
DROP TABLE IF EXISTS tv_programs;
CREATE TABLE IF NOT EXISTS tv_epg_programs (
  key   TEXT NOT NULL,     -- "<source>:<xmltv id>"
  start INTEGER NOT NULL,  -- unix seconds
  stop  INTEGER NOT NULL,
  title TEXT NOT NULL,
  descr TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (key, start)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS tv_epg_programs_time ON tv_epg_programs(start, stop);
