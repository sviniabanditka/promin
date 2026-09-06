package store

// Backups. The cache in this database is regenerable, but the accounts
// (users/PIN), bookmarks, playlists, history and timecodes are NOT — losing
// them means every profile starts over. The whole database is ~13 MB, so a
// nightly snapshot costs nothing and is the cheapest insurance available.
//
// Snapshots use SQLite's own `VACUUM INTO`, which takes a consistent, compact
// copy WHILE the app keeps reading and writing — no downtime, no file-copy race
// (a plain cp of a live SQLite file can capture a torn page).

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// backupPrefix/backupExt bound what Prune is allowed to delete: it must never
// remove anything but its own snapshots.
const (
	backupPrefix = "promin-"
	backupExt    = ".db"
)

// Snapshot writes a consistent copy of the database into dir and returns its
// path. The name carries a UTC timestamp so snapshots sort chronologically.
func (db *DB) Snapshot(dir string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("store: backup dir: %w", err)
	}
	name := backupPrefix + now.UTC().Format("20060102-150405") + backupExt
	path := filepath.Join(dir, name)

	// VACUUM INTO refuses to overwrite, so clear a same-second leftover.
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return "", fmt.Errorf("store: backup replace: %w", err)
		}
	}
	// The path is interpolated because VACUUM INTO takes a literal, not a bind
	// parameter. It is built from our own timestamp + configured dir, never user
	// input; the quote-escape is belt-and-braces.
	stmt := "VACUUM INTO '" + strings.ReplaceAll(path, "'", "''") + "'"
	if _, err := db.SQL.Exec(stmt); err != nil {
		return "", fmt.Errorf("store: vacuum into: %w", err)
	}
	return path, nil
}

// PruneBackups keeps the newest `keep` snapshots in dir and deletes the rest.
// Only files matching our own naming scheme are ever considered.
func PruneBackups(dir string, keep int) (removed int, err error) {
	if keep < 1 {
		keep = 1
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	names := []string{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, backupPrefix) || !strings.HasSuffix(n, backupExt) {
			continue
		}
		names = append(names, n)
	}
	if len(names) <= keep {
		return 0, nil
	}
	sort.Strings(names) // timestamped names sort chronologically
	for _, n := range names[:len(names)-keep] {
		if rerr := os.Remove(filepath.Join(dir, n)); rerr != nil {
			err = rerr
			continue
		}
		removed++
	}
	return removed, err
}

// LatestBackup returns the newest snapshot in dir, if any. Used by tooling that
// ships a copy off-box.
func LatestBackup(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	newest := ""
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, backupPrefix) || !strings.HasSuffix(n, backupExt) {
			continue
		}
		if n > newest {
			newest = n
		}
	}
	if newest == "" {
		return "", false
	}
	return filepath.Join(dir, newest), true
}

// StartBackups snapshots the database every interval until ctx is cancelled,
// keeping the newest `keep` copies. Runs one snapshot shortly after boot so a
// fresh deployment is never long without a copy.
func (db *DB) StartBackups(ctx context.Context, dir string, interval time.Duration, keep int, logger *slog.Logger) {
	if interval <= 0 {
		logger.Info("backups disabled")
		return
	}
	go func() {
		// Small delay: don't compete with startup work.
		select {
		case <-ctx.Done():
			return
		case <-time.After(90 * time.Second):
		}
		for {
			db.snapshotOnce(dir, keep, logger)
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()
}

func (db *DB) snapshotOnce(dir string, keep int, logger *slog.Logger) {
	start := time.Now()
	path, err := db.Snapshot(dir, start)
	if err != nil {
		logger.Error("backup failed", "error", err)
		return
	}
	size := int64(0)
	if fi, serr := os.Stat(path); serr == nil {
		size = fi.Size()
	}
	removed, perr := PruneBackups(dir, keep)
	if perr != nil {
		logger.Warn("backup prune failed", "error", perr)
	}
	logger.Info("backup written", "path", path, "bytes", size, "pruned", removed, "took", time.Since(start))
}
