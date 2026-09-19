-- Intro/recap segments LEARNED from what viewers actually skip: the player
-- reports a manual forward jump near the start of an episode, matching jumps
-- from other episodes of the same season pile up in one row, and from the
-- second agreeing episode on the segment is offered as an auto-skip.
-- Shared across profiles on purpose — the intro of a show is the same for
-- everyone in the house.
CREATE TABLE IF NOT EXISTS skip_segments (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  tmdb_id      INTEGER NOT NULL,
  season       INTEGER NOT NULL,
  start_sec    REAL NOT NULL,
  end_sec      REAL NOT NULL,
  -- How many DISTINCT episodes agreed on this segment; last_episode keeps one
  -- episode from voting twice (a viewer re-seeking the same intro).
  votes        INTEGER NOT NULL DEFAULT 1,
  last_episode INTEGER NOT NULL DEFAULT 0,
  updated_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_skip_segments_show ON skip_segments(tmdb_id, season);
