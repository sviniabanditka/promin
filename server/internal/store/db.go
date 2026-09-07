// Package store is the only place in Promin that talks SQL. It opens the
// SQLite database (modernc.org/sqlite, cgo-free), applies embedded
// migrations tracked via PRAGMA user_version, and exposes small
// repositories over *sql.DB for the rest of the application.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps the underlying *sql.DB plus the repositories built on top of it.
type DB struct {
	SQL *sql.DB

	TMDBCache    *TMDBCacheRepo
	Users        *UsersRepo
	Sessions     *SessionsRepo
	Bookmarks    *BookmarksRepo
	Playlists    *PlaylistsRepo
	Timecodes    *TimecodesRepo
	Settings     *SettingsRepo
	TorrentCache *TorrentCacheRepo
	Telegram     *TelegramRepo
	Queue        *QueueRepo
}

// Open opens (creating if necessary) the SQLite database at path, applies
// the connection pragmas from docs/data-model.md, and runs
// any pending embedded migrations.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	// Per-connection pragmas go in the DSN so EVERY pooled connection gets them
	// (a plain `PRAGMA` Exec would only configure whichever connection ran it).
	// journal_mode=WAL is persistent in the file but harmless to repeat.
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// A small pool, not a single connection. WAL lets readers run concurrently
	// with the one writer, and busy_timeout(5000) absorbs writer-vs-writer
	// contention. With one connection every read (2 SELECTs of auth per
	// request, ~17 for home, the 15s /sync/events poll from every TV) queued
	// behind timecode writes during playback and behind the nightly VACUUM INTO.
	// ponytail: single pool; split reader/writer pools if writes ever dominate.
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)

	// Probe once so a bad DSN fails at startup, not on the first request.
	if _, err := sqlDB.Exec("PRAGMA journal_mode"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("sqlite probe: %w", err)
	}

	if err := migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &DB{
		SQL:          sqlDB,
		TMDBCache:    &TMDBCacheRepo{db: sqlDB},
		Users:        &UsersRepo{db: sqlDB},
		Sessions:     &SessionsRepo{db: sqlDB},
		Bookmarks:    &BookmarksRepo{db: sqlDB},
		Playlists:    &PlaylistsRepo{db: sqlDB},
		Timecodes:    &TimecodesRepo{db: sqlDB},
		Settings:     &SettingsRepo{db: sqlDB},
		TorrentCache: &TorrentCacheRepo{db: sqlDB},
		Telegram:     &TelegramRepo{db: sqlDB},
		Queue:        &QueueRepo{db: sqlDB},
	}, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	return d.SQL.Close()
}

func migrate(db *sql.DB) error {
	var userVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.version <= userVersion {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", m.name, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply %s: %w", m.name, err)
		}
		// PRAGMA user_version doesn't accept bound parameters.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("set user_version after %s: %w", m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s: %w", m.name, err)
		}
	}

	return nil
}
