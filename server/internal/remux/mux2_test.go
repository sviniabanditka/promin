package remux

import (
	"strings"
	"testing"
)

// mux2 copy-muxes two elementary inputs: video from the first, audio from the
// second, no re-encode, the same HLS event playlist shape as the other kinds.
func TestBuildMux2HLSArgs(t *testing.T) {
	args := buildMux2HLSArgs("http://ytx/v", "http://ytx/a", "/out")
	joined := strings.Join(args, " ")
	for _, want := range []string{"-i http://ytx/v -i http://ytx/a", "-map 0:v:0", "-map 1:a:0", "-c copy", "-hls_playlist_type event", "/out/playlist.m3u8"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "reconnect") {
		t.Errorf("mux2 inputs are one-shot chunked responses; reconnect flags would replay from zero: %s", joined)
	}
}
