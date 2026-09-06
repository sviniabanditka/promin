package httpapi

import (
	"net/url"
	"strings"
	"testing"
)

// After the hard media gate, rewritten child segment/variant URLs must carry
// the caller's ?t= token or hls.js 401s on every segment ("network error").
func TestRewriteManifestPropagatesToken(t *testing.T) {
	base, _ := url.Parse("https://balancer.example/hls/master.m3u8")
	manifest := "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1000000\n" +
		"720p/index.m3u8\n" +
		"#EXT-X-MEDIA:TYPE=AUDIO,URI=\"audio/eng.m3u8\"\n" +
		"seg0.ts\n"

	out := string(rewriteManifest([]byte(manifest), base, "TOK123"))

	// every rewritten /relay wrapper carries the token
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "/relay?u=") && !strings.Contains(ln, "&t=TOK123") {
			t.Fatalf("child URL missing token: %q", ln)
		}
	}
	if strings.Count(out, "&t=TOK123") != 3 { // variant + audio URI + segment
		t.Fatalf("want 3 tokened children, got:\n%s", out)
	}
	// the foreign upstream host is base64-wrapped, never carries the token bare
	if strings.Contains(out, "balancer.example") {
		t.Fatalf("foreign host leaked unwrapped into manifest:\n%s", out)
	}
}

// Empty token (shouldn't happen behind the gate, but be safe) → no &t= appended.
func TestRewriteManifestNoTokenNoAppend(t *testing.T) {
	base, _ := url.Parse("https://b.example/m.m3u8")
	out := string(rewriteManifest([]byte("#EXTM3U\nseg0.ts\n"), base, ""))
	if strings.Contains(out, "&t=") {
		t.Fatalf("unexpected token append with empty token:\n%s", out)
	}
	if !strings.Contains(out, "/relay?u=") {
		t.Fatalf("segment not rewritten:\n%s", out)
	}
}
