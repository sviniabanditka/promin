package httpapi

import (
	"strings"
	"testing"
)

func TestAppendRemuxToken(t *testing.T) {
	in := "#EXTM3U\n" +
		"#EXT-X-MEDIA:TYPE=AUDIO,URI=\"stream-rus.m3u8\"\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1\n" +
		"stream-0.m3u8\n" +
		"#EXTINF:6.0,\n" +
		"seg-0-000001.ts\n" +
		"seg-0-000002.ts?x=1\n" +
		"https://cdn.example/abs.ts\n" +
		"seg-0-000003.ts?t=TOK\n"
	out := string(appendRemuxToken([]byte(in), "TOK"))

	must := []string{
		"URI=\"stream-rus.m3u8?t=TOK\"",
		"stream-0.m3u8?t=TOK",
		"seg-0-000001.ts?t=TOK",
		"seg-0-000002.ts?x=1&t=TOK",
		"https://cdn.example/abs.ts", // absolute untouched
	}
	for _, m := range must {
		if !strings.Contains(out, m) {
			t.Fatalf("missing %q in:\n%s", m, out)
		}
	}
	// no double-stamp on the already-tokened line, no token on absolute
	if strings.Count(out, "t=TOK") != 5 { // media URI + variant + seg1 + seg2 + the pre-existing one
		t.Fatalf("unexpected token count:\n%s", out)
	}
	if strings.Contains(out, "abs.ts?t=") || strings.Contains(out, "abs.ts&t=") {
		t.Fatalf("token leaked onto absolute URL:\n%s", out)
	}
}
