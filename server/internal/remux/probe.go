package remux

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// probeTimeout bounds an ffprobe call. It reads only the container/stream
// header, so it's fast on a local file once byte 0 is present — but a torrent
// file whose first piece hasn't downloaded yet makes ffprobe block on the read,
// hence a real (not tiny) ceiling before we give up and fall back to no-transcode.
const probeTimeout = 8 * time.Second

// VideoInfo is the subset of a source's first video stream Promin needs to
// decide an HEVC/AV1 → H264 transcode: the codec and whether it's HDR
// (BT.2020 primaries + PQ/HLG transfer), the latter driving tone-mapping.
type VideoInfo struct {
	Codec string // lowercased ffprobe codec_name, e.g. "hevc", "h264", "av1"
	HDR   bool
}

// NeedsTranscode reports codecs old Tizen/webOS webviews typically can't
// hardware-decode (HEVC/H.265, AV1) — the trigger for a full transcode when
// the client also reports no decoder (hevc=false).
func (v VideoInfo) NeedsTranscode() bool {
	switch v.Codec {
	case "hevc", "h265", "av1":
		return true
	}
	return false
}

// ProbeVideo runs ffprobe on src (a local file path or an http(s) URL) and
// returns the first video stream's codec + HDR flag. Returns a zero VideoInfo
// (Codec=="") with no error when ffprobe read the source but it has no video
// stream; returns an error when ffprobe itself failed (unreadable/timeout) —
// callers treat that as "don't transcode" and fall back to normal streaming.
func ProbeVideo(ctx context.Context, ffprobePath, src string) (VideoInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,color_transfer,color_primaries",
		"-of", "json",
		src,
	)
	out, err := cmd.Output()
	if err != nil {
		return VideoInfo{}, err
	}
	var parsed struct {
		Streams []struct {
			CodecName      string `json:"codec_name"`
			ColorTransfer  string `json:"color_transfer"`
			ColorPrimaries string `json:"color_primaries"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return VideoInfo{}, err
	}
	if len(parsed.Streams) == 0 {
		return VideoInfo{}, nil
	}
	s := parsed.Streams[0]
	return VideoInfo{
		Codec: strings.ToLower(s.CodecName),
		HDR:   isHDRTransfer(s.ColorTransfer) || strings.EqualFold(s.ColorPrimaries, "bt2020"),
	}, nil
}

// AudioMeta describes one source audio track (ffmpeg map order): its language
// tag and its human title (e.g. "Dub - KinoPoisk HD"). Title is what the track
// menu shows — a movie can carry several dubs in the SAME language from
// different studios, so language alone isn't a usable label.
type AudioMeta struct {
	Lang  string
	Title string
}

// ProbeAudio lists the source's audio streams (in ffmpeg map order) with their
// language + title, so the remux can emit one HLS audio rendition per track and
// label it properly. Returns nil on ffprobe failure (caller falls back to a
// single muxed track).
func ProbeAudio(ctx context.Context, ffprobePath, src string) []AudioMeta {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-select_streams", "a",
		"-show_entries", "stream_tags=language,title",
		"-of", "json",
		src,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var parsed struct {
		Streams []struct {
			Tags struct {
				Language string `json:"language"`
				Title    string `json:"title"`
			} `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil
	}
	out2 := make([]AudioMeta, len(parsed.Streams))
	for i, s := range parsed.Streams {
		out2[i] = AudioMeta{
			Lang:  strings.ToLower(strings.TrimSpace(s.Tags.Language)),
			Title: strings.TrimSpace(s.Tags.Title),
		}
	}
	return out2
}

// ProbeAudio via the queue's configured ffprobe binary.
func (q *Queue) ProbeAudio(ctx context.Context, src string) []AudioMeta {
	return ProbeAudio(ctx, q.cfg.FFprobePath, src)
}

// isHDRTransfer recognises the two HDR transfer functions in the wild: PQ
// (HDR10 / Dolby Vision base layer, smpte2084) and HLG (arib-std-b67).
func isHDRTransfer(t string) bool {
	switch strings.ToLower(t) {
	case "smpte2084", "arib-std-b67":
		return true
	}
	return false
}

// ProbeVideo runs ffprobe with the queue's configured ffprobe binary.
func (q *Queue) ProbeVideo(ctx context.Context, src string) (VideoInfo, error) {
	return ProbeVideo(ctx, q.cfg.FFprobePath, src)
}
