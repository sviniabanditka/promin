-- Sessions stop storing the bearer token itself: `token` becomes
-- hex(sha256(token)) and `token_id` keeps the 12-character public device id
-- (the prefix of the raw token, which the TV also derives client-side).
-- Existing rows are converted at startup (SessionsRepo.HashLegacy) — rows
-- with an empty token_id are still raw.
ALTER TABLE sessions ADD COLUMN token_id TEXT NOT NULL DEFAULT '';
