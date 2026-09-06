package sources

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// EncodeRelayURL wraps an upstream stream/subtitle URL into our own
// /relay?u=<base64url> path, per docs/api.md Using
// RawURLEncoding matches the (no-padding) example in that doc.
func EncodeRelayURL(raw string) string {
	return "/relay?u=" + base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// EncodeRemuxURL wraps an upstream HLS master into our /remux endpoint
// (same base64url as relay). Used when the client can't play demuxed-audio
// HLS (old Samsung Tizen: demuxed_hls=false) — the remux service muxes
// video+one audio track into a single stream. audio=0 = default track.
func EncodeRemuxURL(raw string) string {
	// kind=copy_hls must match remux.KindCopyHLS exactly — "hls" was rejected
	// with 400. (Currently unused for online: clients play demuxed HLS via
	// hls.js over /relay; kept correct for any future remux path.)
	return "/remux?u=" + base64.RawURLEncoding.EncodeToString([]byte(raw)) + "&kind=copy_hls&audio=0"
}

// DecodeRelayParam decodes the "u" query param of a /relay request back
// into the upstream URL. Accepts both raw (no padding) and standard
// base64url, since some clients/proxies may re-pad it.
func DecodeRelayParam(u string) (string, error) {
	if u == "" {
		return "", fmt.Errorf("sources: empty relay param")
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(u); err == nil {
		return string(decoded), nil
	}
	decoded, err := base64.URLEncoding.DecodeString(u)
	if err != nil {
		return "", fmt.Errorf("sources: invalid relay param: %w", err)
	}
	return string(decoded), nil
}

// DecodeRelayURL decodes a full "/relay?u=..." path (as produced by
// EncodeRelayURL) back into the upstream URL.
func DecodeRelayURL(relayPath string) (string, error) {
	idx := strings.Index(relayPath, "u=")
	if idx < 0 {
		return "", fmt.Errorf("sources: not a relay url: %s", relayPath)
	}
	q, err := url.ParseQuery(relayPath[strings.Index(relayPath, "?")+1:])
	if err != nil {
		return "", err
	}
	return DecodeRelayParam(q.Get("u"))
}
