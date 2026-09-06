// Package remux orchestrates ffmpeg-based remux/transcode jobs, per
// docs/backend.md and docs/streaming.md
//
// Three pipeline kinds:
//   - KindCopyHLS: demuxed HLS (separate #EXT-X-MEDIA:TYPE=AUDIO track) ->
//     single muxed HLS, `-c copy` (~0.1 CPU core, pure container remux).
//     This is what old pre-2022 Tizen needs (docs/streaming.md table).
//   - KindCopyMKV: MKV container -> muxed HLS, `-c copy`. Needed by
//     webview clients that can't demux MKV themselves (webOS/Tizen).
//   - KindTranscodeHEVC: full decode+encode HEVC->H264. Stub only in this
//     phase — see transcode.go.
package remux

import (
	"context"
	"sync"
	"time"
)

// Kind identifies which ffmpeg pipeline a Job runs.
type Kind string

const (
	KindCopyHLS       Kind = "copy_hls"
	KindCopyMKV       Kind = "copy_mkv"
	KindTranscodeHEVC Kind = "transcode_hevc"
)

// State is the job lifecycle, per docs/backend.md
type State string

const (
	StateQueued  State = "queued"
	StateRunning State = "running"
	StateReady   State = "ready"
	StateFailed  State = "failed"
)

// Job is one ffmpeg remux/transcode task. Mirrors the struct sketched in
// docs/backend.md, with the fields needed to actually drive
// and serve it.
type Job struct {
	ID     string
	Kind   Kind
	Source string // upstream m3u8 URL (copy_hls) or local file path (copy_mkv/transcode)
	// StartSec > 0: ffmpeg starts muxing at this source offset (-ss) so a resume
	// deep into a file plays immediately instead of waiting for the mux to
	// reach it; the client keeps time as playlist time + StartSec.
	StartSec float64
	// AudioIndex selects which audio track ffmpeg maps (-map 0:a:<n>), per
	// docs/streaming.md ("ровно одну аудиодорожку"). Changing
	// it means a new Job (new dedup key), not mutating this one.
	AudioIndex int
	OutputDir  string // PROMIN_DATA_DIR/remux/{id}/
	// HDR marks the source as HDR10/HLG (transcode_hevc only) so run() applies
	// the zscale tone-map to SDR. Ignored when the ffmpeg build lacks zscale.
	HDR bool
	// Audio is the source's audio tracks in order (torrent copy_mkv/transcode).
	// len>1 → run() emits multi-audio HLS (master.m3u8, one rendition per track,
	// labelled from the title) for native switching; len≤1 → single playlist.m3u8.
	Audio     []AudioMeta
	CreatedAt time.Time

	mu          sync.Mutex
	state       State
	err         error
	lastAccess  time.Time
	cancel      context.CancelFunc
	durationSec float64      // source total duration, parsed from ffmpeg stderr
	audioTracks []AudioTrack // source audio renditions, parsed from ffmpeg stderr
	tailLines   []string     // last few ffmpeg stderr lines, for failure diagnosis
}

// AudioTrack is one selectable audio rendition of the SOURCE (not of the muxed
// output — ffmpeg maps exactly one into the output). Index is the ffmpeg
// audio-relative index, i.e. what `-map 0:a:<Index>` selects, so the client can
// ask for a different track by re-requesting /remux with audio=<Index>.
type AudioTrack struct {
	Index int    `json:"index"`
	Lang  string `json:"lang,omitempty"`
	Title string `json:"title,omitempty"`
}

func newJob(id string, kind Kind, source string, audioIndex int, outputDir string, hdr bool, audio []AudioMeta, startSec float64) *Job {
	now := time.Now()
	return &Job{
		ID:         id,
		Kind:       kind,
		Source:     source,
		AudioIndex: audioIndex,
		StartSec:   startSec,
		OutputDir:  outputDir,
		HDR:        hdr,
		Audio:      audio,
		CreatedAt:  now,
		state:      StateQueued,
		lastAccess: now,
	}
}

// State returns the job's current lifecycle state.
func (j *Job) State() State {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

// Err returns the failure reason, if State() == StateFailed.
func (j *Job) Err() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.err
}

func (j *Job) setState(s State) {
	j.mu.Lock()
	j.state = s
	j.mu.Unlock()
}

func (j *Job) setFailed(err error) {
	j.mu.Lock()
	j.state = StateFailed
	j.err = err
	j.mu.Unlock()
}

// DurationSec is the source's total duration in seconds (0 until ffmpeg has
// logged its "Duration:" line). Served as X-Remux-Duration so the player can
// show the real length instead of the growing remux playlist's partial one.
func (j *Job) DurationSec() float64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.durationSec
}

// noteStderr keeps a short ring of the most recent ffmpeg stderr lines. On
// failure cmd.Wait only yields "exit status N", which says nothing about WHY —
// these lines carry the actual ffmpeg complaint.
func (j *Job) noteStderr(line string) {
	j.mu.Lock()
	j.tailLines = append(j.tailLines, line)
	if len(j.tailLines) > 6 {
		j.tailLines = j.tailLines[len(j.tailLines)-6:]
	}
	j.mu.Unlock()
}

// StderrTail returns the retained ffmpeg stderr lines, oldest first.
func (j *Job) StderrTail() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]string, len(j.tailLines))
	copy(out, j.tailLines)
	return out
}

// AudioTracks lists the source's audio renditions (empty until ffmpeg has
// logged its input stream table). Served as X-Remux-Audio so the player can
// offer a track menu even though the muxed output carries only one track.
func (j *Job) AudioTracks() []AudioTrack {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]AudioTrack, len(j.audioTracks))
	copy(out, j.audioTracks)
	return out
}

// maxAudioTracks caps the menu for sources whose renditions carry no language
// at all (nothing to dedupe on) — a 90-entry list is noise, not a choice.
const maxAudioTracks = 12

// addAudioTrack appends one parsed rendition, keeping the FIRST index seen per
// language. An HLS master repeats its audio group under every video variant, so
// ffmpeg's stream table lists the same dub once per quality — collaps yields 97
// entries that are really ~3 languages. Deduping by language turns that into a
// usable menu; the retained index still addresses a valid `-map 0:a:<n>`.
func (j *Job) addAudioTrack(tr AudioTrack) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := range j.audioTracks {
		if j.audioTracks[i].Index == tr.Index {
			return
		}
		if tr.Lang != "" && j.audioTracks[i].Lang == tr.Lang {
			return
		}
	}
	if len(j.audioTracks) >= maxAudioTracks {
		return
	}
	j.audioTracks = append(j.audioTracks, tr)
}

func (j *Job) setDurationSec(d float64) {
	j.mu.Lock()
	if j.durationSec == 0 && d > 0 {
		j.durationSec = d
	}
	j.mu.Unlock()
}

// Touch marks the job as accessed just now, resetting its idle TTL clock
// (docs/backend.md, "чистятся через TTL после последнего
// обращения").
func (j *Job) Touch() {
	j.mu.Lock()
	j.lastAccess = time.Now()
	j.mu.Unlock()
}

func (j *Job) idleSince() time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.lastAccess
}
