package catalog

import "testing"

func TestParseImdbRatingString(t *testing.T) {
	cases := []struct {
		in     string
		want   float64
		wantOK bool
	}{
		{"8.5", 8.5, true},
		{"N/A", 0, false},
		{"", 0, false},
		{"not-a-number", 0, false},
		{"10", 10, true},
	}
	for _, c := range cases {
		got, ok := parseImdbRatingString(c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("parseImdbRatingString(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestParseOMDbBody(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		want   float64
		wantOK bool
	}{
		{"ok", `{"imdbRating":"8.5","Response":"True"}`, 8.5, true},
		{"na", `{"imdbRating":"N/A","Response":"True"}`, 0, false},
		{"error-response", `{"Response":"False","Error":"Incorrect IMDb ID."}`, 0, false},
		{"malformed", `not json`, 0, false},
	}
	for _, c := range cases {
		got, ok := parseOMDbBody(c.body)
		if ok != c.wantOK || got != c.want {
			t.Errorf("%s: parseOMDbBody(%q) = (%v, %v), want (%v, %v)", c.name, c.body, got, ok, c.want, c.wantOK)
		}
	}
}
