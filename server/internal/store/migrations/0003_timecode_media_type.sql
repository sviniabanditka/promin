-- Phase 3 (добивание): timecodes didn't record media_type, so the
-- "continue watching" shelf couldn't tell TMDB movie ids from tv ids
-- apart (they aren't unique across types) and therefore couldn't fetch
-- posters/titles for it. Add the column, defaulting existing rows to
-- 'movie' (best-effort backfill; Phase 3 had no tv timecodes in
-- practice since the client didn't send media_type yet).
ALTER TABLE timecodes ADD COLUMN media_type TEXT NOT NULL DEFAULT 'movie' CHECK (media_type IN ('movie','tv'));
