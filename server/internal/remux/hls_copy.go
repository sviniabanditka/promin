package remux

import (
	"bufio"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
)

// PlaylistFile is the fixed filename ffmpeg writes the growing/finished
// HLS playlist to, per the -hls_segment_filename/output path in
// buildCopyHLSArgs and buildCopyMKVHLSArgs.
const PlaylistFile = "playlist.m3u8"

// PlaylistPath returns the on-disk path to a job's HLS playlist.
func (j *Job) PlaylistPath() string {
	return filepath.Join(j.OutputDir, PlaylistFile)
}

// PlaylistFilePath returns the on-disk path to a job's .m3u8 (master or a
// variant like stream-rus.m3u8), guarding against path traversal from the HTTP
// layer — only a bare "*.m3u8" filename in the job dir is allowed.
func (j *Job) PlaylistFilePath(name string) (string, bool) {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		return "", false
	}
	if !strings.HasSuffix(name, ".m3u8") {
		return "", false
	}
	return filepath.Join(j.OutputDir, name), true
}

// SegmentPath returns the on-disk path to one of a job's .ts segments,
// after validating name looks like a segment ffmpeg would have written
// (defends against path traversal from the HTTP layer).
func (j *Job) SegmentPath(name string) (string, bool) {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		return "", false
	}
	if !strings.HasPrefix(name, "seg-") || !strings.HasSuffix(name, ".ts") {
		return "", false
	}
	return filepath.Join(j.OutputDir, name), true
}

// DetectKind guesses which remux pipeline a source needs from its URL/path
// extension, per docs/streaming.md (container/manifest ->
// pipeline decision). Callers with an explicit "kind" request param should
// prefer that over guessing.
func DetectKind(source string) Kind {
	lower := strings.ToLower(source)
	// Strip query string before checking extension (source may be a full
	// URL like ".../master.m3u8?token=...").
	if i := strings.IndexByte(lower, '?'); i >= 0 {
		lower = lower[:i]
	}
	switch {
	case IsMKV(lower):
		return KindCopyMKV
	default:
		// .m3u8, or anything else HLS-ish — copy_hls is the default and
		// by far the common case (old Tizen + demuxed online sources).
		return KindCopyHLS
	}
}

// IsDemuxedHLS reports whether a master HLS playlist declares audio as a
// separate EXT-X-MEDIA:TYPE=AUDIO group (as opposed to muxed into the
// video stream) — the condition from docs/streaming.md
// ("Когда включается remux") that, combined with an old-Tizen client,
// triggers a copy_hls job. It's a plain text scan, not a full HLS parser:
// good enough to answer "does this master need copy-remux at all."
func IsDemuxedHLS(manifest []byte) bool {
	for _, line := range strings.Split(string(manifest), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-MEDIA:") && strings.Contains(line, "TYPE=AUDIO") && strings.Contains(line, "URI=") {
			return true
		}
	}
	return false
}

// logFFmpegStderr drains an ffmpeg process's stderr (where it logs
// everything, including progress) line by line into the app logger at
// debug level, so a hung/failing job is diagnosable without attaching a
// debugger, but normal operation doesn't spam info/warn.
// Returns a channel closed once the scanner reaches EOF. The caller MUST wait
// on it before cmd.Wait() — Wait closes the read end of the pipe, which would
// otherwise cut the scanner off mid-stream and drop the ffmpeg error tail
// (exactly the StderrTail we surface on failure). See os/exec Wait docs.
func logFFmpegStderr(logger *slog.Logger, job *Job, r io.Reader) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 4096), 1<<20)
		audioSeen := 0
		for scanner.Scan() {
			line := scanner.Text()
			logger.Debug("remux: ffmpeg", "job_id", job.ID, "line", line)
			job.noteStderr(line)
			if d := parseFFmpegDuration(line); d > 0 {
				job.setDurationSec(d)
			}
			if tr, ok := parseFFmpegAudioStream(line, audioSeen); ok {
				job.addAudioTrack(tr)
				audioSeen++
			}
		}
	}()
	return done
}

// parseFFmpegAudioStream recognises one audio line of ffmpeg's input stream
// table, e.g.
//
//	Stream #0:1(rus): Audio: aac (LC), 48000 Hz, stereo, fltp
//
// and returns it as the audioIdx-th audio rendition — audioIdx being the
// caller's running count, which is exactly what `-map 0:a:<n>` addresses.
// Output-side lines ("Stream #0:0 -> #0:0") are skipped: they describe the
// mapping we already chose, not the selectable sources.
func parseFFmpegAudioStream(line string, audioIdx int) (AudioTrack, bool) {
	i := strings.Index(line, "Stream #")
	if i < 0 || strings.Contains(line, "->") {
		return AudioTrack{}, false
	}
	rest := line[i+len("Stream #"):]
	colon := strings.Index(rest, ": ")
	if colon < 0 {
		return AudioTrack{}, false
	}
	head := rest[:colon]   // e.g. "0:1(rus)"
	tail := rest[colon+2:] // e.g. "Audio: aac (LC), ..."
	if !strings.HasPrefix(tail, "Audio:") {
		return AudioTrack{}, false
	}
	lang := ""
	if o := strings.IndexByte(head, '('); o >= 0 {
		if c := strings.IndexByte(head[o:], ')'); c > 0 {
			lang = head[o+1 : o+c]
		}
	}
	return AudioTrack{Index: audioIdx, Lang: lang}, true
}

// parseFFmpegDuration extracts seconds from an ffmpeg "  Duration: HH:MM:SS.ss,
// start: ..., bitrate: ..." stderr line. Returns 0 if the line isn't a
// (parseable) Duration line — including ffmpeg's "N/A" for unknown length.
func parseFFmpegDuration(line string) float64 {
	i := strings.Index(line, "Duration:")
	if i < 0 {
		return 0
	}
	rest := strings.TrimSpace(line[i+len("Duration:"):])
	if c := strings.IndexByte(rest, ','); c >= 0 {
		rest = rest[:c]
	}
	parts := strings.Split(strings.TrimSpace(rest), ":")
	if len(parts) != 3 {
		return 0
	}
	h, e1 := strconv.ParseFloat(parts[0], 64)
	m, e2 := strconv.ParseFloat(parts[1], 64)
	s, e3 := strconv.ParseFloat(parts[2], 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0
	}
	return h*3600 + m*60 + s
}
