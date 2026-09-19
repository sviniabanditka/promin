package torrent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// The cache used to trip on phantoms: rows carried the torrent's full length
// while the disk held a fraction, and nothing ever aged anything out. The
// sweep must measure what is really there, drop rows with no files, and delete
// what nobody touched for CacheTTL — leaving fresh torrents alone.
func TestSweepMeasuresAgesAndKeepsFresh(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	torrents := filepath.Join(dir, "torrents")
	if err := os.MkdirAll(filepath.Join(torrents, "Pack.S02"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel string, n int) {
		if err := os.WriteFile(filepath.Join(torrents, rel), make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Pack.S02/e01.mkv", 1000)
	write("Pack.S02/e02.mkv", 500)
	write("Fresh.mkv.part", 300)
	write("Old.mkv", 700)

	now := time.Now().Unix()
	week := int64(7 * 24 * 3600)
	// Row sizes are the torrents' full lengths, far above what is on disk.
	if err := db.TorrentCache.Upsert("pack", "Pack.S02", 12_000_000_000, now-2*86400); err != nil {
		t.Fatal(err)
	}
	if err := db.TorrentCache.Upsert("fresh", "Fresh.mkv", 20_000_000_000, now-3600); err != nil {
		t.Fatal(err)
	}
	if err := db.TorrentCache.Upsert("old", "Old.mkv", 8_000_000_000, now-week-3600); err != nil {
		t.Fatal(err)
	}
	if err := db.TorrentCache.Upsert("phantom", "Gone.mkv", 8_700_000_000, now-30*86400); err != nil {
		t.Fatal(err)
	}

	m := &Manager{cfg: Config{DataDir: dir, Repo: db.TorrentCache, Logger: slog.Default(), CacheTTL: 7 * 24 * time.Hour}, entries: map[string]*entry{}}

	m.refreshSizes()
	rows, _ := db.TorrentCache.EvictionCandidates()
	sizes := map[string]int64{}
	for _, r := range rows {
		sizes[r.InfoHash] = r.Size
	}
	if _, ok := sizes["phantom"]; ok {
		t.Fatal("a row with no files on disk must be dropped")
	}
	if sizes["pack"] != 1500 || sizes["fresh"] != 300 || sizes["old"] != 700 {
		t.Fatalf("sizes must be what is on disk (dir sum, .part counted): %+v", sizes)
	}
	if total, _ := db.TorrentCache.TotalSize(); total != 2500 {
		t.Fatalf("tracked total %d, want 2500", total)
	}

	m.evictExpired()
	rows, _ = db.TorrentCache.EvictionCandidates()
	if len(rows) != 2 {
		t.Fatalf("want pack+fresh left, got %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(torrents, "Old.mkv")); !os.IsNotExist(err) {
		t.Fatal("the week-old torrent's file must be deleted")
	}
	for _, keep := range []string{"Pack.S02/e01.mkv", "Fresh.mkv.part"} {
		if _, err := os.Stat(filepath.Join(torrents, keep)); err != nil {
			t.Fatalf("%s must survive: %v", keep, err)
		}
	}
}
