package httpapi

import (
	"net/url"
	"strings"
	"testing"
)

// The live-TV ua=/ref= extras ride behind the media token; the "&" that
// strings.Cut eats must come back (prod 401'd every segment without it).
func TestRelayResolveKeepsExtraSeparator(t *testing.T) {
	base, _ := url.Parse("http://up.example/live/index.m3u8")
	got := relayResolve("chunk.ts", base, "TOK&ua=UA1&ref=R1")
	if !strings.Contains(got, "&t=TOK&ua=UA1&ref=R1") {
		t.Fatalf("bad child url: %s", got)
	}
	if got := relayResolve("chunk.ts", base, "TOK"); !strings.HasSuffix(got, "&t=TOK") {
		t.Fatalf("bad child url without extras: %s", got)
	}
}
