package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A snapshot must be a RESTORABLE database, not just a file that exists: open
// it as a fresh DB and read the non-regenerable rows back out.
func TestSnapshotIsRestorable(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	u, err := db.Users.Create("svinia", "hash", 111)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := db.Bookmarks.Add(u.ID, 550, "movie", 222); err != nil {
		t.Fatalf("bookmark: %v", err)
	}

	backupDir := filepath.Join(dir, "backups")
	path, err := db.Snapshot(backupDir, time.Now())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("snapshot missing/empty: %v", err)
	}

	// Keep writing after the snapshot — the copy must be a point-in-time one,
	// and taking it must not have disturbed the live database.
	if _, err := db.Bookmarks.Add(u.ID, 27205, "movie", 333); err != nil {
		t.Fatalf("post-snapshot write: %v", err)
	}
	db.Close()

	restored, err := Open(path)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer restored.Close()

	users, err := restored.Users.List()
	if err != nil || len(users) != 1 || users[0].Login != "svinia" {
		t.Fatalf("accounts not in the snapshot: %+v err=%v", users, err)
	}
	marks, err := restored.Bookmarks.List(users[0].ID)
	if err != nil {
		t.Fatalf("bookmarks: %v", err)
	}
	found := false
	for _, m := range marks {
		if m.TMDBID == 550 {
			found = true
		}
	}
	if !found {
		t.Errorf("bookmark missing from the snapshot: %+v", marks)
	}
}

// Snapshots taken in the same second must not fail (VACUUM INTO refuses to
// overwrite an existing file).
func TestSnapshotSameSecondReplaces(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	at := time.Now()
	first, err := db.Snapshot(dir, at)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := db.Snapshot(dir, at)
	if err != nil {
		t.Fatalf("second snapshot in the same second failed: %v", err)
	}
	if first != second {
		t.Errorf("expected the same path, got %q and %q", first, second)
	}
}

// Prune keeps the newest N and touches nothing else in the directory.
func TestPruneKeepsNewestAndSparesForeignFiles(t *testing.T) {
	dir := t.TempDir()
	// Chronological names, oldest first.
	made := []string{}
	for _, ts := range []string{"20260101-000000", "20260102-000000", "20260103-000000", "20260104-000000"} {
		p := filepath.Join(dir, backupPrefix+ts+backupExt)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		made = append(made, p)
	}
	foreign := filepath.Join(dir, "do-not-touch.txt")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := PruneBackups(dir, 2)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed %d, want 2", removed)
	}
	for _, p := range made[:2] { // oldest two gone
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("old snapshot survived: %s", p)
		}
	}
	for _, p := range made[2:] { // newest two kept
		if _, err := os.Stat(p); err != nil {
			t.Errorf("recent snapshot deleted: %s", p)
		}
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("prune deleted an unrelated file")
	}

	// Idempotent: a second prune with the same keep removes nothing.
	if removed, _ := PruneBackups(dir, 2); removed != 0 {
		t.Errorf("second prune removed %d", removed)
	}
	// Missing directory is not an error.
	if _, err := PruneBackups(filepath.Join(dir, "nope"), 2); err != nil {
		t.Errorf("missing dir: %v", err)
	}
}

func TestLatestBackup(t *testing.T) {
	dir := t.TempDir()
	if _, ok := LatestBackup(dir); ok {
		t.Error("empty dir reported a backup")
	}
	for _, ts := range []string{"20260101-000000", "20260305-120000"} {
		os.WriteFile(filepath.Join(dir, backupPrefix+ts+backupExt), []byte("x"), 0o600)
	}
	os.WriteFile(filepath.Join(dir, "zzz-not-a-backup.db2"), []byte("x"), 0o600)
	got, ok := LatestBackup(dir)
	if !ok || filepath.Base(got) != backupPrefix+"20260305-120000"+backupExt {
		t.Errorf("got %q ok=%v", got, ok)
	}
}
