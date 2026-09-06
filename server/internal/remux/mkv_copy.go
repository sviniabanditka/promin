package remux

import "strings"

// IsMKV reports whether source (a URL or local path) points at an MKV
// container, per docs/streaming.md ("MKV: отдать как есть
// или ремуксить") — this is the signal DetectKind and the /remux handler
// use to route into buildCopyMKVHLSArgs instead of buildCopyHLSArgs.
func IsMKV(source string) bool {
	lower := strings.ToLower(source)
	if i := strings.IndexByte(lower, '?'); i >= 0 {
		lower = lower[:i]
	}
	return strings.HasSuffix(lower, ".mkv")
}

// The MKV->fMP4/HLS `-c copy` ffmpeg command itself lives in
// buildCopyMKVHLSArgs (ffmpeg.go) — it's built the same way as the HLS
// copy-remux command (both are "run ffmpeg, write an incrementally
// growing playlist to a job dir"), so Queue.run (queue.go) drives both
// through the same code path, just with a different argv per Job.Kind.
//
// This pairs with Phase 4 (torrents, internal/torrent): a torrent file
// selected for playback that turns out to be MKV is remuxed by handing
// its on-disk path (torrent cache file, not a URL) as Job.Source here —
// no separate torrent-specific remux code needed.
