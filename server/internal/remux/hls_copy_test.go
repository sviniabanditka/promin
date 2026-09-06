package remux

import "testing"

func TestParseFFmpegDuration(t *testing.T) {
	cases := []struct {
		line string
		want float64
	}{
		{"  Duration: 01:06:05.00, start: 0.000000, bitrate: 2500 kb/s", 3965},
		{"  Duration: 00:05:30.50, start: 0.0", 330.5},
		{"Duration: 00:00:10.00,", 10},
		{"  Duration: N/A, start: 0.000000, bitrate: N/A", 0},
		{"frame= 100 fps= 25 q=-1.0 size=1024kB time=00:00:04.00", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseFFmpegDuration(c.line); got != c.want {
			t.Errorf("parseFFmpegDuration(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestParseFFmpegAudioStream(t *testing.T) {
	// ffmpeg lists the input stream table once; audioIdx is the caller's running
	// count of audio streams, which is what -map 0:a:<n> addresses.
	lines := []string{
		"  Stream #0:0: Video: h264 (High), yuv420p, 1920x1080",
		"  Stream #0:1(rus): Audio: aac (LC), 48000 Hz, stereo, fltp",
		"  Stream #0:2(ukr): Audio: aac (LC), 48000 Hz, stereo, fltp",
		"  Stream #0:3: Audio: aac (LC), 48000 Hz, stereo",
		"  Stream #0:0 -> #0:0 (copy)", // output mapping — must be skipped
	}
	var got []AudioTrack
	idx := 0
	for _, l := range lines {
		if tr, ok := parseFFmpegAudioStream(l, idx); ok {
			got = append(got, tr)
			idx++
		}
	}
	want := []AudioTrack{{Index: 0, Lang: "rus"}, {Index: 1, Lang: "ukr"}, {Index: 2}}
	if len(got) != len(want) {
		t.Fatalf("got %d tracks (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("track %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAddAudioTrackDedupesByLanguage(t *testing.T) {
	// An HLS master repeats its audio group per video variant, so ffmpeg lists
	// the same dubs many times; only the first index per language is useful.
	j := newJob("t", KindCopyHLS, "src", 0, "", false, nil, 0)
	for i, lang := range []string{"ru", "ru", "ru", "uk", "en", "en", "uk", "ru"} {
		j.addAudioTrack(AudioTrack{Index: i, Lang: lang})
	}
	got := j.AudioTracks()
	want := []AudioTrack{{Index: 0, Lang: "ru"}, {Index: 3, Lang: "uk"}, {Index: 4, Lang: "en"}}
	if len(got) != len(want) {
		t.Fatalf("got %d tracks (%+v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("track %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAddAudioTrackCapsUnlabelled(t *testing.T) {
	j := newJob("t", KindCopyHLS, "src", 0, "", false, nil, 0)
	for i := 0; i < 40; i++ {
		j.addAudioTrack(AudioTrack{Index: i})
	}
	if n := len(j.AudioTracks()); n != maxAudioTracks {
		t.Errorf("kept %d unlabelled tracks, want cap %d", n, maxAudioTracks)
	}
}

func TestWithInputSeek(t *testing.T) {
	args := withInputSeek(buildCopyMKVHLSArgs("/x.mkv", 0, "/out"), 1830)
	for i, a := range args {
		if a == "-i" {
			if i < 2 || args[i-2] != "-ss" || args[i-1] != "1830.000" {
				t.Fatalf("-ss must directly precede -i: %v", args[:i+1])
			}
			return
		}
	}
	t.Fatal("no -i")
}
