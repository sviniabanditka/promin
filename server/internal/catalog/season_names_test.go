package catalog

import (
	"context"
	"log/slog"
	"testing"
)

func TestIsGenericEpisodeName(t *testing.T) {
	generic := []string{"", "Серія 5", "серія 12", "Серия 5", "Эпизод 3", "Епізод 7", "Episode 10", "EPISODE 1"}
	real := []string{"Пілотна серія", "Cat's in the Bag...", "Сіра речовина", "Червоне світло, зелене світло", "Episode of the Dead"}
	for _, n := range generic {
		if !isGenericEpisodeName(n, 1) {
			t.Errorf("expected generic: %q", n)
		}
	}
	for _, n := range real {
		if isGenericEpisodeName(n, 1) {
			t.Errorf("expected real: %q", n)
		}
	}
}

// Backfill: a uk season with placeholder names + an en season with real names →
// placeholders replaced by en, real uk names kept.
func TestFetchSeasonBackfillsFromEn(t *testing.T) {
	cache := newTestCache(t)
	uk := `{"season_number":1,"episodes":[
		{"episode_number":1,"name":"Справжня назва"},
		{"episode_number":2,"name":"Серія 2"},
		{"episode_number":3,"name":"Серія 3"}]}`
	en := `{"season_number":1,"episodes":[
		{"episode_number":1,"name":"Real EN 1"},
		{"episode_number":2,"name":"The Real Title"},
		{"episode_number":3,"name":"Episode 3"}]}`
	// seed both language caches (stale-ok read via getCached: fresh expiry)
	cache.Set("title:tv:99:season:1:uk", uk, 1<<62)
	cache.Set("title:tv:99:season:1:en", en, 1<<62)

	svc := NewClient([]string{"http://127.0.0.1:0"}, "k", cache, slog.Default())
	sd, err := svc.fetchSeason(context.Background(), 99, 1, "uk")
	if err != nil {
		t.Fatalf("fetchSeason: %v", err)
	}
	got := map[int]string{}
	for _, e := range sd.Episodes {
		got[e.EpisodeNumber] = e.Name
	}
	if got[1] != "Справжня назва" {
		t.Errorf("ep1 real uk name should stay, got %q", got[1])
	}
	if got[2] != "The Real Title" {
		t.Errorf("ep2 should backfill en, got %q", got[2])
	}
	// ep3: both generic → keep the localized placeholder (en also generic)
	if got[3] != "Серія 3" {
		t.Errorf("ep3 both generic → keep uk, got %q", got[3])
	}
}
