package store

import (
	"path/filepath"
	"testing"
)

// Danger zone: wiping one profile must not touch another.
func TestClearUserIsScopedToTheProfile(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	u1, _ := db.Users.Create("a", "h", 1)
	u2, _ := db.Users.Create("b", "h", 1)
	for _, u := range []User{u1, u2} {
		if _, err := db.Bookmarks.Add(u.ID, 1, "movie", 1); err != nil {
			t.Fatal(err)
		}
		pl, err := db.Playlists.Create(u.ID, "p", 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Playlists.AddItem(pl.ID, 5, "movie", 1); err != nil {
			t.Fatal(err)
		}
		if _, err := db.History.Add(HistoryEntry{UserID: u.ID, TMDBID: 3, MediaType: "movie", WatchedAt: 1}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := db.Timecodes.Upsert(Timecode{UserID: u.ID, TMDBID: 3, MediaType: "movie", PositionSec: 1, DurationSec: 10, UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		if err := db.Settings.Set(u.ID, "night_mode", "true", 1); err != nil {
			t.Fatal(err)
		}
		if err := db.Sessions.Create(Session{Token: "tok-" + u.Login, UserID: u.ID, DeviceName: "tv", DeviceType: "tv", CreatedAt: 1, LastSeen: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []func(int64) error{db.Bookmarks.ClearUser, db.Playlists.ClearUser, db.History.ClearUser, db.Timecodes.ClearUser, db.Settings.ClearUser, db.Sessions.DeleteByUser} {
		if err := f(u1.ID); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := db.Bookmarks.List(u1.ID); len(b) != 0 {
		t.Fatal("u1 bookmarks left")
	}
	if p, _ := db.Playlists.List(u1.ID); len(p) != 0 {
		t.Fatal("u1 playlists left")
	}
	if h, _ := db.History.List(u1.ID, 10, 0); len(h) != 0 {
		t.Fatal("u1 history left")
	}
	if s, _ := db.Settings.GetAll(u1.ID); len(s) != 0 {
		t.Fatal("u1 settings left")
	}
	if ss, _ := db.Sessions.ListByUser(u1.ID); len(ss) != 0 {
		t.Fatal("u1 sessions left")
	}
	if b, _ := db.Bookmarks.List(u2.ID); len(b) != 1 {
		t.Fatal("u2 bookmarks touched")
	}
	if p, _ := db.Playlists.List(u2.ID); len(p) != 1 {
		t.Fatal("u2 playlists touched")
	}
	if ss, _ := db.Sessions.ListByUser(u2.ID); len(ss) != 1 {
		t.Fatal("u2 sessions touched")
	}
	if _, err := db.Users.GetByID(u1.ID); err != nil {
		t.Fatal("profile itself must survive")
	}
}
