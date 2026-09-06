package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sviniabanditka/promin/server/internal/sources"
)

func TestToWebVTTConvertsSRT(t *testing.T) {
	srt := "\xEF\xBB\xBF1\r\n00:00:01,000 --> 00:00:02,500\r\n{\\an8}Hello\r\n\r\n2\r\n00:01:02,750 --> 00:01:04,000\r\nWorld\r\n"
	got := string(toWebVTT([]byte(srt)))
	want := "WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.500\nHello\n\n2\n00:01:02.750 --> 00:01:04.000\nWorld\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestToWebVTTKeepsVTT(t *testing.T) {
	vtt := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nx\n"
	if string(toWebVTT([]byte(vtt))) != vtt {
		t.Fatal("WebVTT must pass through untouched")
	}
}

func TestRelayServesSRTAsVTT(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "1\n00:00:01,000 --> 00:00:02,000\nHi\n")
	}))
	defer up.Close()
	relayAllowLoopback = true
	defer func() { relayAllowLoopback = false }()
	h := relayHandler(slog.Default())
	req := httptest.NewRequest(http.MethodGet, sources.EncodeRelayURL(up.URL+"/subs/ru.srt"), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/vtt") {
		t.Fatalf("status %d ct %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(rr.Body.String(), "WEBVTT\n\n1\n00:00:01.000") {
		t.Fatalf("body: %q", rr.Body.String())
	}
}
