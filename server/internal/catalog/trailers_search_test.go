package catalog

import "testing"

func TestParseYouTubeTrailers(t *testing.T) {
	page := []byte(`<script>var ytInitialData = {"contents":[` +
		`{"videoRenderer":{"videoId":"aaaaaaaaaaa","title":{"runs":[{"text":"Матриця | Український трейлер"}]},"lengthText":{"simpleText":"2:21"}}},` +
		`{"videoRenderer":{"videoId":"bbbbbbbbbbb","title":{"runs":[{"text":"Матриця повний фільм"}]},"lengthText":{"simpleText":"2:16:00"}}},` +
		`{"videoRenderer":{"videoId":"ccccccccccc","title":{"runs":[{"text":"Розбір трейлера 40 хв"}]},"lengthText":{"simpleText":"40:12"}}},` +
		`{"videoRenderer":{"videoId":"aaaaaaaaaaa","title":{"runs":[{"text":"dup trailer"}]}}}` +
		`]};</script>`)
	out := parseYouTubeTrailers(page, "uk")
	if len(out) != 1 || out[0].Key != "aaaaaaaaaaa" || out[0].Lang != "uk" {
		t.Fatalf("want the one short trailer, got %+v", out)
	}
	if q := trailerQuery("Матриця", 1999, "uk"); q != "Матриця 1999 трейлер українською" {
		t.Fatalf("query: %q", q)
	}
}
