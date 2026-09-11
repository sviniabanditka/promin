package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/sviniabanditka/promin/server/internal/remux"
	"github.com/sviniabanditka/promin/server/internal/sources"
)

// remuxHandlers wires GET /remux and GET /remux/{job}/{file} to a
// remux.Queue, replacing the Phase-2b remuxStub, per docs/api.md
// section 5 and docs/backend.md
type remuxHandlers struct {
	queue  *remux.Queue
	logger *slog.Logger
}

// create implements GET /remux?u=<base64url-source>&audio=<n>&kind=hls|mkv.
// u is the same base64url encoding /relay uses (sources.EncodeRelayURL),
// so callers can pass either a raw upstream master m3u8 or one already
// wrapped through /relay (e.g. an online source's stream_url) — either
// way ffmpeg ends up fetching a plain http(s) URL.
//
// It finds-or-creates a job (dedup on kind+source+audio, see
// remux.Queue.Submit) and returns its id/playlist URL immediately; the
// job runs in the background and the playlist starts empty/202 until
// ffmpeg produces its first segment (docs/streaming.md,
// "Задержка старта").
func (h *remuxHandlers) create(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw, err := sources.DecodeRelayParam(q.Get("u"))
	if err != nil {
		writeBadRequest(w, "невірний параметр u")
		return
	}
	upstream, err := url.Parse(raw)
	if err != nil || (upstream.Scheme != "http" && upstream.Scheme != "https") {
		writeBadRequest(w, "невірний upstream URL")
		return
	}
	// SSRF guard (parity with /relay): block literal loopback/link-local/
	// unspecified targets (127.0.0.1, 169.254 metadata, ::1) before ffmpeg fetches.
	if err := validateUpstream(upstream); err != nil {
		writeBadRequest(w, "невірний upstream URL")
		return
	}
	// ffmpeg dials the upstream itself: resolve first so a name pointing at a
	// cluster service or the node cannot be handed to it.
	if resolvesToBlocked(r.Context(), upstream.Hostname()) {
		writeBadRequest(w, "u: relay: blocked host")
		return
	}

	audioIndex := 0
	if a := q.Get("audio"); a != "" {
		n, err := strconv.Atoi(a)
		if err != nil || n < 0 {
			writeBadRequest(w, "параметр audio має бути невід'ємним цілим числом")
			return
		}
		audioIndex = n
	}

	kind := remux.Kind(q.Get("kind"))
	switch kind {
	case "":
		kind = remux.DetectKind(raw)
	case remux.KindCopyHLS, remux.KindCopyMKV, remux.KindTranscodeHEVC:
		// explicit override accepted as-is
	default:
		writeBadRequest(w, "невірний параметр kind (hls|mkv)")
		return
	}

	// caps=... (device capabilities) may be attached by the caller for
	// future source-selection logic (remux.Capabilities/Decide); this
	// endpoint always does what it's asked, so it's accepted but unused
	// here — the decision of *whether* to call /remux at all is made by
	// the sources module, not this handler.

	startSec, _ := strconv.ParseFloat(q.Get("start"), 64)
	if startSec < 0 || startSec != startSec {
		startSec = 0
	}
	job, err := h.queue.SubmitFrom(kind, raw, audioIndex, startSec)
	if err != nil {
		h.logger.Error("remux: submit failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "не вдалося запустити remux")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"job_id":       job.ID,
		"playlist_url": "/remux/" + job.ID + "/playlist.m3u8",
	})
}

// serveFile implements GET /remux/{job}/playlist.m3u8 and
// GET /remux/{job}/seg-*.ts, per docs/api.md
func (h *remuxHandlers) serveFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("job")
	file := r.PathValue("file")

	job, ok := h.queue.Get(id)
	if !ok {
		writeNotFound(w, "job_not_found", "remux job не знайдено")
		return
	}

	if strings.HasSuffix(file, ".m3u8") {
		// hls=1 (torrent path): the player's HLS engine loads the manifest
		// directly, so a not-ready state must be a VALID (empty live) m3u8, not a
		// 202-JSON body — hls.js parses a 2xx body as a playlist and a JSON body
		// is a fatal parse error ("network error"). The online path omits hls=1
		// and keeps the 202 (its prepareStream polls the JSON until ready). Any
		// .m3u8 name is allowed (master.m3u8 + stream-*.m3u8 for multi-audio).
		h.servePlaylist(w, job, file, r.URL.Query().Get("hls") == "1", tokenFromRequest(r, true))
		return
	}
	h.serveSegment(w, r, job, file)
}

// notReadyPlaylist is a minimal valid live HLS playlist (no segments, no
// ENDLIST) served while a transcode's first segment is still being produced, so
// hls.js waits/reloads instead of erroring on a 202. It switches to the real
// growing playlist file the moment ffmpeg writes it.
const notReadyPlaylist = "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n"

func (h *remuxHandlers) servePlaylist(w http.ResponseWriter, job *remux.Job, file string, hlsWait bool, token string) {
	path, ok := job.PlaylistFilePath(file)
	if !ok {
		writeBadRequest(w, "невірне ім'я плейлиста")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if job.State() == remux.StateFailed {
			// Full ffmpeg error (incl. the stderr tail) goes to the server log
			// only — the client gets a short fixed reason, not a wall of ffmpeg
			// output rendered in the player.
			h.logger.Warn("remux: job failed", "job_id", job.ID, "error", job.Err())
			writeError(w, http.StatusBadGateway, "upstream_unavailable", "remux завершився помилкою")
			return
		}
		// A master playlist can't be faked with an empty media playlist — if it
		// isn't written yet (rare: the handler waits for it before redirecting),
		// 503 so hls.js retries the manifest load.
		if file == remux.MasterPlaylist {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, "not_ready", "готуємо потік")
			return
		}
		// Variant media playlists (stream-rus.m3u8, …) are referenced from the
		// master by RELATIVE URI, so hls.js requests them WITHOUT ?hls=1 — but a
		// not-ready one must still be a valid empty-live m3u8, not a 202-JSON
		// body (which hls.js can't parse → the audio rendition fails silently).
		if hlsWait || strings.HasPrefix(file, "stream-") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "no-cache")
			// The player fetches this URL once before handing it to the engine and
			// reads the offset/total from it — expose them even before the first
			// segment exists (duration is parsed from ffmpeg's banner early).
			if file == remux.PlaylistFile {
				setRemuxHeaders(w, job)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(notReadyPlaylist))
			return
		}
		// Not ready yet: ffmpeg hasn't flushed a playlist/first segment,
		// per docs/api.md ("202 Accepted ... якщо job
		// ще не віддав жодного сегмента"). For a transcode still waiting on a
		// free slot (state=queued), also report how many transcodes are ahead
		// so the client can show "N-й у черзі" instead of a bare spinner (Т6).
		body := map[string]any{
			"state":    string(job.State()),
			"progress": 0.0,
		}
		if job.Kind == remux.KindTranscodeHEVC && job.State() == remux.StateQueued {
			body["queue_position"] = h.queue.TranscodeQueueDepth()
		}
		writeJSON(w, http.StatusAccepted, body)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	// X-Remux-Duration/Start/Audio only apply to the single-playlist path — the
	// client reads them from its own fetch of the playlist (prepareStream).
	if file == remux.PlaylistFile {
		setRemuxHeaders(w, job)
	}
	// Child URIs (seg-*.ts, stream-*.m3u8 variants, #EXT-X-MEDIA audio) are
	// RELATIVE — hls.js/native drop the master's ?t= query when resolving them
	// (RFC 3986), so after the hard gate each child 401s. Append the caller's
	// token to every same-origin child so it authenticates; a variant refetched
	// as stream-*.m3u8?t= re-runs this and tokenizes its own segments.
	if token != "" {
		data = appendRemuxToken(data, token)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// setRemuxHeaders exposes what the player needs to place the playlist on the
// source timeline: X-Remux-Duration (source total, once ffmpeg logged it),
// X-Remux-Start (playlist time 0 == this source second — the job the queue
// returned may not be the offset the client asked for), X-Remux-Audio (source
// track list). All listed in Access-Control-Expose-Headers.
func setRemuxHeaders(w http.ResponseWriter, job *remux.Job) {
	expose := ""
	add := func(h string) {
		if expose != "" {
			expose += ", "
		}
		expose += h
	}
	if d := job.DurationSec(); d > 0 {
		w.Header().Set("X-Remux-Duration", strconv.FormatFloat(d, 'f', 3, 64))
		add("X-Remux-Duration")
	}
	if job.StartSec > 0 {
		w.Header().Set("X-Remux-Start", strconv.FormatFloat(job.StartSec, 'f', 0, 64))
		add("X-Remux-Start")
	}
	if tracks := job.AudioTracks(); len(tracks) > 1 {
		if b, err := json.Marshal(tracks); err == nil {
			w.Header().Set("X-Remux-Audio", string(b))
			add("X-Remux-Audio")
		}
	}
	if expose != "" {
		w.Header().Set("Access-Control-Expose-Headers", expose)
	}
}

// appendRemuxToken adds ?t=/&t=<token> to every relative child URI in an HLS
// playlist (segment + variant lines, and #EXT-X-MEDIA URI="..." attrs). Skips
// absolute refs ("://") and lines already carrying the token (no double-stamp).
func appendRemuxToken(data []byte, token string) []byte {
	esc := url.QueryEscape(token)
	needle := "t=" + esc
	lines := strings.Split(string(data), "\n")
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "#") {
			if strings.HasPrefix(ln, "#EXT-X-MEDIA:") {
				lines[i] = addTokenToURIAttr(ln, needle, token)
			}
			continue
		}
		if strings.Contains(ln, "://") || strings.Contains(ln, needle) {
			continue
		}
		lines[i] = withMediaToken(ln, token)
	}
	return []byte(strings.Join(lines, "\n"))
}

// addTokenToURIAttr appends the token inside a tag's URI="..." value.
func addTokenToURIAttr(line, needle, token string) string {
	start, end, ok := m3u8URIAttr(line)
	if !ok {
		return line
	}
	uri := line[start:end]
	if strings.Contains(uri, "://") || strings.Contains(uri, needle) {
		return line
	}
	return line[:start] + withMediaToken(uri, token) + line[end:]
}

func (h *remuxHandlers) serveSegment(w http.ResponseWriter, r *http.Request, job *remux.Job, file string) {
	path, ok := job.SegmentPath(file)
	if !ok {
		writeBadRequest(w, "невірне ім'я сегмента")
		return
	}
	if _, err := os.Stat(path); err != nil {
		writeNotFound(w, "segment_not_found", "сегмент не знайдено")
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	// Stream from disk: a copy_mkv 4K segment is 20–40 MB, and os.ReadFile per
	// request × hls.js prefetch × several TVs was 100+ MB of RSS on a small pod.
	// ServeFile also gives Content-Length and Range for free.
	http.ServeFile(w, r, path)
}
