-- Live TV: whether the upstream answered the liveness probe with
-- Access-Control-Allow-Origin: * — only then may hls.js on the TV fetch the
-- stream directly; everything else goes through /relay.
ALTER TABLE tv_streams ADD COLUMN cors INTEGER NOT NULL DEFAULT 0;
