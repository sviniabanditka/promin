package sources

import "strings"

// wrapStream routes a raw upstream URL through our own origin: an HLS master
// for a client that cannot play demuxed audio (PreferMuxed — old Samsung
// webviews) goes through /remux, everything else through /relay.
func wrapStream(rawURL string, preferMuxed bool) string {
	if preferMuxed && strings.Contains(rawURL, ".m3u8") {
		return EncodeRemuxURL(rawURL)
	}
	return EncodeRelayURL(rawURL)
}
