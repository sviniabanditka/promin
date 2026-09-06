package httpapi

import (
	"net/url"
	"strings"
)

// withMediaToken appends the media-gate token to one of OUR OWN URLs
// (/relay, /remux, /stream, loopback /stream). Every child URL a manifest or
// redirect hands to the player must carry it — the recurring bug class here
// was "one more site forgot the token" (or, once, appended it unescaped). All
// sites now route through this. Empty token → URL unchanged.
func withMediaToken(u, token string) string {
	if token == "" {
		return u
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "t=" + url.QueryEscape(token)
}

// m3u8URIAttr locates the value of a tag's URI="..." attribute, returning the
// [start,end) byte range of the value inside line. ok=false when absent or
// unterminated. Shared by the relay manifest rewriter and the remux token
// appender, which used to each carry their own copy of this scan.
func m3u8URIAttr(line string) (start, end int, ok bool) {
	const attr = "URI=\""
	idx := strings.Index(line, attr)
	if idx < 0 {
		return 0, 0, false
	}
	start = idx + len(attr)
	rel := strings.Index(line[start:], "\"")
	if rel < 0 {
		return 0, 0, false
	}
	return start, start + rel, true
}
