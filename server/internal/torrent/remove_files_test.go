package torrent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// The torrent info name is attacker-controlled. ".." joined onto
// DataDir/torrents used to point RemoveAll at the data dir itself.
func TestRemoveTorrentFilesNeverEscapesTorrentsDir(t *testing.T) {
	dir := t.TempDir()
	torrents := filepath.Join(dir, "torrents")
	if err := os.MkdirAll(filepath.Join(torrents, "legit"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dir, "promin.db")
	if err := os.WriteFile(sentinel, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{cfg: Config{DataDir: dir, Logger: slog.Default()}}

	for _, name := range []string{"..", "../..", "../promin.db", "/", ".", "", "sub/../../promin.db"} {
		m.removeTorrentFiles(name)
		if _, err := os.Stat(sentinel); err != nil {
			t.Fatalf("name %q removed data outside torrents/: %v", name, err)
		}
		if _, err := os.Stat(torrents); err != nil {
			t.Fatalf("name %q removed the torrents dir: %v", name, err)
		}
	}
	// A plain name still cleans up its own directory.
	m.removeTorrentFiles("legit")
	if _, err := os.Stat(filepath.Join(torrents, "legit")); !os.IsNotExist(err) {
		t.Fatalf("legit torrent dir not removed: %v", err)
	}
}
