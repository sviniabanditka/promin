package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

// Sidecar subtitles come from balancers as .srt about as often as .vtt, but a
// <track> element only renders WebVTT — an SRT file selected in the player
// showed a checkmark and no text, silently. The relay converts on the fly.

const subtitleReadLimit = 4 << 20

func isSubtitle(path, contentType string) bool {
	p := strings.ToLower(path)
	if strings.HasSuffix(p, ".srt") || strings.HasSuffix(p, ".vtt") {
		return true
	}
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/vtt") || strings.Contains(ct, "application/x-subrip")
}

func relaySubtitle(w http.ResponseWriter, resp *http.Response, logger *slog.Logger) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, subtitleReadLimit))
	if err != nil {
		logger.Warn("relay: failed reading subtitle", "error", err)
		writeError(w, http.StatusBadGateway, "upstream_unavailable", "не вдалося прочитати субтитри")
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(toWebVTT(body))
}

// srtTime matches an SRT cue timing line; WebVTT differs only in the decimal
// separator (comma → dot).
var srtTime = regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}),(\d{3})`)

// assTag strips {\an8}-style override tags some SRT dumps carry.
var assTag = regexp.MustCompile(`\{\\[^}]*\}`)

// toWebVTT returns body unchanged if it already is WebVTT, else converts SRT:
// BOM dropped, "WEBVTT" header prepended, cue numbers kept (WebVTT allows an
// identifier line), comma decimals → dots, override tags removed.
func toWebVTT(body []byte) []byte {
	body = bytes.TrimPrefix(body, []byte("\xEF\xBB\xBF"))
	if bytes.HasPrefix(body, []byte("WEBVTT")) {
		return body
	}
	body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	body = srtTime.ReplaceAll(body, []byte("$1.$2"))
	body = assTag.ReplaceAll(body, nil)
	out := make([]byte, 0, len(body)+8)
	out = append(out, "WEBVTT\n\n"...)
	return append(out, body...)
}
