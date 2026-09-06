package remux

import (
	"strings"
	"testing"
)

// TestBuildTranscodeHEVCArgs pins the two things easy to get wrong: the output
// is HLS+libx264 (not the old fMP4), always 8-bit and ≤1080p, and HDR tone-maps
// only when asked.
func TestBuildTranscodeHEVCArgs(t *testing.T) {
	sdr := strings.Join(buildTranscodeHEVCArgs("/v.mkv", 1, "/out", false), " ")
	for _, want := range []string{"-c:v libx264", "playlist.m3u8", "scale=-2:'min(1080,ih)'", "format=yuv420p", "0:a:1"} {
		if !strings.Contains(sdr, want) {
			t.Errorf("SDR args missing %q\n  got: %s", want, sdr)
		}
	}
	if strings.Contains(sdr, "zscale") || strings.Contains(sdr, "tonemap") {
		t.Errorf("SDR must not tone-map\n  got: %s", sdr)
	}
	if strings.Contains(sdr, "-f mp4") {
		t.Errorf("output must be HLS, not fMP4\n  got: %s", sdr)
	}

	hdr := strings.Join(buildTranscodeHEVCArgs("/v.mkv", 0, "/out", true), " ")
	for _, want := range []string{"scale=-2:'min(1080,ih)'", "zscale", "tonemap", "playlist.m3u8"} {
		if !strings.Contains(hdr, want) {
			t.Errorf("HDR args missing %q\n  got: %s", want, hdr)
		}
	}
}

func TestBuildAudioVarMap(t *testing.T) {
	got := buildAudioVarMap([]AudioMeta{{Lang: "rus"}, {Lang: "ukr"}, {}})
	want := "v:0,agroup:aud a:0,agroup:aud,default:yes,language:rus a:1,agroup:aud,language:ukr a:2,agroup:aud"
	if got != want {
		t.Errorf("var_stream_map:\n got %q\nwant %q", got, want)
	}
}
