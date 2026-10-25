-- Like / dislike on a title (the title screen's 👍/👎). value is 1 or -1; a
-- neutral title simply has no row. Likes seed the personalised home shelves,
-- dislikes are excluded from every shelf.
CREATE TABLE IF NOT EXISTS reactions (
  user_id    INTEGER NOT NULL,
  tmdb_id    INTEGER NOT NULL,
  media_type TEXT    NOT NULL,
  value      INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, tmdb_id, media_type)
) WITHOUT ROWID;
