-- The history table was never written by any client: watch history is
-- derived from timecodes everywhere. Drop it and its index.
DROP TABLE IF EXISTS history;
