package catalog

import "testing"

// Every language keeps its trailers: a flood of English clips must not push
// the single Ukrainian one out of the list.
func TestNormalizeTrailersCapsPerLanguage(t *testing.T) {
	var vids []tmdbVideo
	for i := 0; i < 20; i++ {
		vids = append(vids, tmdbVideo{Key: "en" + string(rune('a'+i)), Site: "YouTube", Type: "Trailer", Official: true, Iso6391: "en"})
	}
	vids = append(vids, tmdbVideo{Key: "uk1", Site: "YouTube", Type: "Teaser", Iso6391: "uk"})
	out := normalizeTrailers(vids)
	en, uk := 0, 0
	for _, tr := range out {
		switch tr.Lang {
		case "en":
			en++
		case "uk":
			uk++
		}
	}
	if en != maxTrailers || uk != 1 {
		t.Fatalf("want %d en + 1 uk, got en=%d uk=%d", maxTrailers, en, uk)
	}
}
