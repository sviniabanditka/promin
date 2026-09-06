package remux

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MasterPlaylist is the file ffmpeg writes when producing multi-audio HLS
// (one video variant + one audio rendition per source track). The client loads
// this and hls.js switches audio natively.
const MasterPlaylist = "master.m3u8"

// buildAudioVarMap builds ffmpeg's -var_stream_map for a single video variant
// plus one audio rendition per source audio track, all in group "aud" and each
// tagged with its language so the client's track menu reads rus/ukr/eng instead
// of "audio_1". The first track is DEFAULT.
func buildAudioVarMap(audio []AudioMeta) string {
	parts := []string{"v:0,agroup:aud"}
	for i, a := range audio {
		seg := "a:" + strconv.Itoa(i) + ",agroup:aud"
		if i == 0 {
			seg += ",default:yes"
		}
		if a.Lang != "" {
			seg += ",language:" + a.Lang
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, " ")
}

// CheckFFmpeg verifies the configured ffmpeg binary is runnable
// (`ffmpeg -version`), per docs/backend.md
// (PROMIN_FFMPEG_PATH). Called once at startup; callers should log a
// warning (not fail startup) on error, since Promin is still usable
// without remux (direct/relay passthrough continues to work).
func CheckFFmpeg(ffmpegPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegPath, "-version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("remux: ffmpeg not usable at %q: %w", ffmpegPath, err)
	}
	return nil
}

// buildCopyHLSArgs builds ffmpeg command #1 from docs/streaming.md
// section 4: demuxed HLS -> muxed HLS, `-c copy`.
//
// We take the single-master-URL form ffmpeg itself resolves
// (`-i master.m3u8 -map 0:v:0 -map 0:a:<n>`), per the alternate note in
// that section: our source is already promin's own relay-wrapped master
// (same-origin, CORS-safe), and ffmpeg's HLS demuxer follows the
// EXT-X-STREAM-INF/EXT-X-MEDIA references in it itself — no need to
// pre-parse the manifest in Go and pass two separate -i inputs.
//
// The output is an EVENT playlist: it grows while ffmpeg consumes the source,
// the player keeps polling for new segments, and an ENDLIST lands once the mux
// finishes. See the flag comments below for why the earlier omit_endlist form
// made hls.js mistake it for a live stream.
func buildCopyHLSArgs(masterURL string, audioIndex int, outputDir string) []string {
	return []string{
		"-y",
		"-nostdin",
		"-nostats",
		"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5",
		"-i", masterURL,
		"-map", "0:v:0",
		"-map", fmt.Sprintf("0:a:%d?", audioIndex),
		"-c", "copy",
		"-f", "hls",
		"-hls_time", "6",
		"-hls_list_size", "0",
		// EXT-X-PLAYLIST-TYPE:EVENT — the playlist grows while ffmpeg muxes but
		// is append-only and starts at segment 0. Without it (the previous
		// omit_endlist + no type) hls.js reads a never-ending playlist as LIVE:
		// it starts playback at the live edge (episode "opened near the end"),
		// seeks against a sliding window (scrub landed on the wrong spot) and
		// never fires 'ended' (no autoplay-next). EVENT also writes ENDLIST when
		// the mux completes, so end-of-media is signalled properly.
		"-hls_playlist_type", "event",
		// NB: no append_list here — combined with an explicit playlist type some
		// ffmpeg builds abort the run outright ("remux завершився помилкою").
		// It only mattered for resuming into an existing playlist, and every job
		// gets a fresh output dir anyway.
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(outputDir, "seg-%06d.ts"),
		filepath.Join(outputDir, "playlist.m3u8"),
	}
}

// buildCopyMKVHLSArgs builds ffmpeg command #3 from docs/streaming.md
// section 4: MKV -> muxed HLS. Video is copied (H264 plays as-is), but AUDIO is
// re-encoded to stereo AAC: torrent MKVs almost always carry AC3/EAC3/DTS, which
// hls.js/MSE can't decode (silent audio); AAC stereo plays everywhere and the
// audio-only encode is cheap (~0.1 core). Source is a finite local file, so the
// EVENT playlist grows while muxing and gets an #EXT-X-ENDLIST at completion.
func buildCopyMKVHLSArgs(sourcePath string, audioIndex int, outputDir string) []string {
	return []string{
		"-y",
		"-nostdin",
		"-nostats",
		"-i", sourcePath,
		"-map", "0:v:0",
		"-map", fmt.Sprintf("0:a:%d?", audioIndex),
		"-c:v", "copy",
		"-c:a", "aac", "-ac", "2", "-b:a", "160k",
		"-f", "hls",
		"-hls_time", "6",
		"-hls_list_size", "0",
		// EVENT, not live: a growing playlist with no playlist_type/ENDLIST is
		// read as LIVE by hls.js, which clamps seeks to the sliding live window —
		// "+10 min" lands at the muxed edge (~+2 min), the flagship torrent-seek
		// bug. EVENT is seekable-from-0 while still growing. No append_list: it
		// aborts some ffmpeg builds when combined with an explicit playlist_type.
		"-hls_playlist_type", "event",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(outputDir, "seg-%06d.ts"),
		filepath.Join(outputDir, "playlist.m3u8"),
	}
}

// hdrToSDRFilter tone-maps HDR10/HLG (BT.2020 PQ/HLG) down to SDR BT.709 so
// the H264 output looks right on an SDR panel instead of washed-out/grey. Needs
// an ffmpeg built with libzimg (the zscale filter) — gated by tonemap=true,
// which the caller only sets when both the source is HDR and zscale is present.
const hdrToSDRFilter = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"

// buildTranscodeHEVCArgs builds ffmpeg command #4 from docs/streaming.md
// section 4: full HEVC/AV1 → H264 transcode, output as a VOD HLS playlist (same
// serve/poll path as the copy jobs, so the player needs no new code — unlike
// the old fMP4 form). Source is a finite local file (torrent cache), so the
// EVENT playlist gets an #EXT-X-ENDLIST on ffmpeg's clean exit — seekable VOD.
//
// -preset veryfast + -threads 2 caps per-job CPU, consistent with the ~3 vCPU
// stack budget and the 1-2 concurrent transcode limit
// (PROMIN_REMUX_MAX_TRANSCODES). tonemap=true prepends hdrToSDRFilter for HDR
// sources (requires libzimg).
func buildTranscodeHEVCArgs(sourcePath string, audioIndex int, outputDir string, tonemap bool) []string {
	// -vf chain, in order:
	//   1. downscale — cap output at 1080p (old TV panels are ≤1080p; a 4K→4K
	//      transcode is infeasible on ~2-3 vCPU, but 4K→1080p is realtime-ish).
	//      -2 keeps aspect + an even width; the min() never upscales smaller
	//      sources. Its comma is single-quoted so the filtergraph parser doesn't
	//      read it as a filter separator (Т5).
	//   2. HDR→SDR tone-map (Т4) when the source is HDR and zscale is present;
	//      hdrToSDRFilter already ends in format=yuv420p (8-bit).
	//   3. else format=yuv420p — force 8-bit even for 10-bit SDR HEVC, since old
	//      Tizen H264 decoders can't do High10.
	filters := []string{"scale=-2:'min(1080,ih)'"}
	if tonemap {
		filters = append(filters, hdrToSDRFilter)
	} else {
		filters = append(filters, "format=yuv420p")
	}

	return []string{
		"-y",
		"-nostdin",
		"-nostats",
		"-i", sourcePath,
		"-map", "0:v:0",
		"-map", fmt.Sprintf("0:a:%d?", audioIndex),
		"-vf", strings.Join(filters, ","),
		// ultrafast + 4 threads: 4K HEVC software DECODE is the bottleneck
		// (~2-3 cores), so make the H264 ENCODE as cheap as possible to reach
		// realtime; capped at 1 concurrent (MaxTranscodes) so 4 threads is safe
		// on the ~6 vCPU node. -g 48 bounds the GOP so segment cuts align.
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "23", "-g", "48",
		"-threads", "4",
		// Downmix to stereo AAC: the source is often 5.1 AC3/EAC3/DTS, and
		// hls.js/MSE frequently can't decode multichannel AAC-in-TS → silent
		// audio. Stereo AAC plays everywhere.
		"-c:a", "aac", "-ac", "2", "-b:a", "160k",
		"-f", "hls",
		// Short segments so the FIRST one is ready fast — a 6s 4K segment took
		// ~7s to appear and hls.js gave up ("network error" until a retry).
		"-hls_time", "2",
		"-hls_list_size", "0",
		// EVENT, not live — see buildCopyMKVHLSArgs. Without it hls.js reads the
		// growing playlist as LIVE and clamps seeks to the live edge.
		"-hls_playlist_type", "event",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(outputDir, "seg-%06d.ts"),
		filepath.Join(outputDir, "playlist.m3u8"),
	}
}

// CheckFFprobe verifies the ffprobe binary is runnable (needed for codec/HDR
// detection before a transcode). Non-fatal: without it, HEVC auto-transcode
// simply won't trigger and playback falls back to direct/copy.
func CheckFFprobe(ffprobePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, ffprobePath, "-version").Run(); err != nil {
		return fmt.Errorf("remux: ffprobe not usable at %q: %w", ffprobePath, err)
	}
	return nil
}

// HasZscale reports whether the ffmpeg build includes the zscale filter
// (libzimg), required for HDR→SDR tone-mapping. Logged at startup so the
// transcode path knows whether it can tone-map HDR or must pass it through
// (washed colours) — see hdrToSDRFilter.
func HasZscale(ffmpegPath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-filters").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), " zscale ")
}

// withInputSeek inserts "-ss <sec>" right before the first "-i": INPUT seeking,
// so ffmpeg jumps to the nearest keyframe at/before the offset instead of
// decoding from the start. Output timestamps restart at 0 — the client adds the
// offset back (player timeBase), which is why the playlist is served with
// X-Remux-Start.
func withInputSeek(args []string, startSec float64) []string {
	for i, a := range args {
		if a == "-i" {
			out := make([]string, 0, len(args)+2)
			out = append(out, args[:i]...)
			out = append(out, "-ss", fmt.Sprintf("%.3f", startSec))
			out = append(out, args[i:]...)
			return out
		}
	}
	return args
}
