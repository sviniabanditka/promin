package torrent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// sweepOrphanFiles deletes everything in the torrent dir that no cache row
// claims — the single most destructive routine in the codebase, and it had no
// test. It must keep claimed files (and their .part), keep anacrolix's
// dotfiles, and delete nothing at all when the repo cannot be read.
func TestSweepOrphanFilesKeepsClaimedAndDotfiles(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.TorrentCache.Upsert("aaaa", "Keep.Me.2020.mkv", 1, 1); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, n := range []string{"Keep.Me.2020.mkv", "Keep.Me.2020.mkv.part", "Orphan.mkv", ".torrent.bolt.db"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{cfg: Config{Repo: db.TorrentCache, Logger: slog.Default()}}
	m.sweepOrphanFiles(dir)

	for _, n := range []string{"Keep.Me.2020.mkv", "Keep.Me.2020.mkv.part", ".torrent.bolt.db"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s must survive the sweep: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "Orphan.mkv")); !os.IsNotExist(err) {
		t.Error("orphan must be removed")
	}
}

func TestSweepOrphanFilesSkipsWhenRepoUnavailable(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close() // Names() will fail → the sweep must not touch anything
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Unclaimed.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{cfg: Config{Repo: db.TorrentCache, Logger: slog.Default()}}
	m.sweepOrphanFiles(dir)
	if _, err := os.Stat(filepath.Join(dir, "Unclaimed.mkv")); err != nil {
		t.Fatal("a repo failure must never turn into a mass delete")
	}
}
