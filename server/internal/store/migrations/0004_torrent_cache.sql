-- Phase 4: the torrent module's disk LRU cache needs somewhere to track
-- per-infohash size/last_access without scanning PROMIN_DATA_DIR/torrents
-- on every eviction tick (docs/data-model.md section 2 and section 4
-- "LRU торрент-кэша").
CREATE TABLE torrent_cache_meta (
    infohash    TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    size        INTEGER NOT NULL DEFAULT 0,
    last_access INTEGER NOT NULL
);
CREATE INDEX idx_torrent_cache_last_access ON torrent_cache_meta(last_access);
