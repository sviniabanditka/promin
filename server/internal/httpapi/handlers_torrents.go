package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sviniabanditka/promin/server/internal/remux"
	"github.com/sviniabanditka/promin/server/internal/sources"
	torrentpkg "github.com/sviniabanditka/promin/server/internal/torrent"
)

// torrentHandlers wires the JacRed-facade listing (GET
// /api/v1/sources/torrents) and the torrent engine endpoints (add/stream/
// active/remove), per docs/api.md/5 and
// docs/backend.md
type torrentHandlers struct {
	sourcesSvc  *sources.Service
	mgr         *torrentpkg.Manager
	remuxQueue  *remux.Queue
	selfBaseURL string // loopback base for internal /stream reads (ffmpeg/ffprobe)
	logger      *slog.Logger
}

// selfStreamURL builds the internal loopback URL ffmpeg/ffprobe use to read a
// torrent file through our own /stream endpoint (RAW — no mkv/hevc params, so it
// hits the direct ServeContent branch, not another remux/transcode). Reading via
// /stream means the anacrolix Reader (blocks until pieces arrive, valid bytes,
// Range/seek) instead of the raw on-disk .part (scattered/incomplete → garbage).
// The hard media gate requires ?t= on /stream, so the internal read must carry
// the caller's already-validated token. It only ever addresses 127.0.0.1 and
// the raw branch serves via ServeContent with no cross-host redirect, so the
// token can't leak to a foreign upstream.
func (h *torrentHandlers) selfStreamURL(infoHash string, fileIdx int, token string) string {
	return withMediaToken(h.selfBaseURL+"/stream/"+infoHash+"/"+strconv.Itoa(fileIdx), token)
}

// addMagnetTimeout bounds AddMagnet's metadata-fetch wait as seen by the
// HTTP layer; internal/torrent.Manager applies its own (equal or shorter)
// Config.MetadataTimeout, this is just the outer HTTP request budget.
const addMagnetTimeout = 35 * time.Second

// list implements GET /api/v1/sources/torrents, per docs/api.md
// section 3 (extended with title/original_title/year/season/episode, per
// task brief item 3 — the doc's tmdb_id/type/season/episode alone aren't
// enough to build a JacRed text query).
func (h *torrentHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mediaType := q.Get("type")
	if mediaType == "" {
		mediaType = "movie"
	}

	// No title to search on → an empty JacRed text query returns garbage/errors.
	// Answer with a clean empty list instead of querying.
	if q.Get("title") == "" && q.Get("original_title") == "" {
		writeJSON(w, http.StatusOK, sources.TorrentsResponse{Torrents: []sources.TorrentResult{}})
		return
	}

	resp := h.sourcesSvc.Torrents(r.Context(), sources.TorrentsRequest{
		TMDBID:        atoiDefault(q.Get("tmdb_id"), 0),
		Type:          mediaType,
		Title:         q.Get("title"),
		OriginalTitle: q.Get("original_title"),
		Year:          atoiDefault(q.Get("year"), 0),
		Season:        atoiDefault(q.Get("season"), 0),
		Episode:       atoiDefault(q.Get("episode"), 0),
	})
	writeJSON(w, http.StatusOK, resp)
}

// addResponse is the body of POST/GET /api/v1/torrents/add.
type addResponse struct {
	InfoHash string                `json:"infohash"`
	Files    []torrentpkg.FileInfo `json:"files"`
}

// add implements POST/GET /api/v1/torrents/add?id=<magnet_id> (preferred,
// magnet_id from GET /api/v1/sources/torrents) or ?magnet=<magnet-uri, raw
// or base64url> for direct testing.
func (h *torrentHandlers) add(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	magnet, err := resolveMagnet(q.Get("id"), q.Get("magnet"))
	if err != nil {
		writeBadRequest(w, "потрібен параметр id (magnet_id) або magnet")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), addMagnetTimeout)
	defer cancel()

	infoHash, err := h.mgr.AddMagnet(ctx, magnet)
	if err != nil {
		writeTorrentError(w, err)
		return
	}

	files, err := h.mgr.ListFiles(infoHash)
	if err != nil {
		writeTorrentError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, addResponse{InfoHash: infoHash, Files: files})
}

func resolveMagnet(id, magnet string) (string, error) {
	if id != "" {
		return sources.DecodeMagnetID(id)
	}
	if magnet == "" {
		return "", errors.New("empty")
	}
	if strings.HasPrefix(magnet, "magnet:") {
		return magnet, nil
	}
	return sources.DecodeMagnetID(magnet)
}

// active implements GET /api/v1/torrents/active.
func (h *torrentHandlers) active(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"torrents": h.mgr.Active()})
}

// audioTrack is one selectable audio track of a torrent file (index = ffmpeg
// -map 0:a:<index>). The client shows these in the player's track menu and
// switches by re-requesting /stream with ?audio=<index>.
type audioTrack struct {
	Index int    `json:"index"`
	Lang  string `json:"lang,omitempty"`
	Title string `json:"title,omitempty"`
}

// audioTracks implements GET /api/v1/torrents/audio?infohash=&file= — ffprobes
// the torrent file's audio tracks so the player can offer track switching
// (server-side inline re-mux per index; the bundled hls.js can't play HLS
// alternate-audio renditions on the TV target — docs/questions-functional.md Q9).
func (h *torrentHandlers) audioTracks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	infoHash := q.Get("infohash")
	fileIdx := atoiDefault(q.Get("file"), -1)
	if infoHash == "" || fileIdx < 0 {
		writeBadRequest(w, "потрібні infohash та file")
		return
	}
	files, err := h.mgr.ListFiles(infoHash)
	if err != nil {
		writeTorrentError(w, err)
		return
	}
	if fileIdx >= len(files) {
		writeNotFound(w, "file_not_found", "файл не знайдено в торренті")
		return
	}
	metas := h.remuxQueue.ProbeAudio(r.Context(), h.selfStreamURL(infoHash, fileIdx, tokenFromRequest(r, true)))
	tracks := make([]audioTrack, 0, len(metas))
	for i, m := range metas {
		tracks = append(tracks, audioTrack{Index: i, Lang: m.Lang, Title: m.Title})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tracks": tracks})
}

// remove implements DELETE /api/v1/torrents/{infohash}.
func (h *torrentHandlers) remove(w http.ResponseWriter, r *http.Request) {
	infoHash := r.PathValue("infohash")
	if err := h.mgr.Remove(infoHash); err != nil {
		writeTorrentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stream implements GET /stream/{infohash}/{fileIdx}, per
// docs/api.md: full Range support over the torrent's
// sequential+readahead Reader via http.ServeContent.
//
// MKV-in-webview handling (docs/streaming.md, "MKV: отдать
// как есть или ремуксить"): a client that can't demux MKV itself sends
// ?mkv=false. For an .mkv file in that case, this handler does NOT stream
// the raw container — it kicks off a copy_mkv remux job (reusing
// internal/remux exactly as it does for online sources) and redirects to
// the job's HLS playlist. Pragmatic choice/caveat: the remux job's ffmpeg
// process reads the on-disk torrent file directly (not through our
// io.ReadSeeker), so this handler opens one Manager reader purely as a
// side effect to (a) pin the torrent against LRU/idle eviction and (b)
// kick sequential/readahead download from byte 0, then closes it once the
// remux job leaves the queued/running state. ffmpeg reading ahead of what
// the swarm has actually downloaded will simply block on that read — fine
// for a live copy-remux, since docs/streaming.md already
// treats copy-remux as inherently "growing as source data arrives".
func (h *torrentHandlers) stream(w http.ResponseWriter, r *http.Request) {
	infoHash := r.PathValue("infohash")
	fileIdxStr := r.PathValue("fileIdx")
	fileIdx, err := strconv.Atoi(fileIdxStr)
	if err != nil || fileIdx < 0 {
		writeBadRequest(w, "невірний fileIdx")
		return
	}

	files, err := h.mgr.ListFiles(infoHash)
	if err != nil {
		writeTorrentError(w, err)
		return
	}
	if fileIdx >= len(files) {
		writeNotFound(w, "file_not_found", "файл не знайдено в торренті")
		return
	}
	name := files[fileIdx].Name
	q := r.URL.Query()

	// transcode=1 (client decided from the release name that it's HEVC/AV1 and
	// its engine can't decode it): kick a transcode→H264 HLS job and redirect.
	// The decision is client-side ON PURPOSE — a server ffprobe here would read
	// the file through /stream and block until the torrent's header piece
	// downloads, racing/timing-out on a cold torrent. hdr=1 asks for tone-map.
	// audio=N picks which source audio track to mux inline (default 0). Track
	// switching re-muxes with a different index rather than relying on hls.js
	// alternate-audio renditions, which the bundled (Chromium-47) hls.js won't
	// load. See docs/questions-functional.md Q9.
	audioIndex := atoiDefault(q.Get("audio"), 0)
	if audioIndex < 0 {
		audioIndex = 0
	}

	// start=N (seconds): mux from that offset so a resume deep into the file
	// plays at once instead of waiting for ffmpeg to reach it (docs/streaming.md).
	startSec, _ := strconv.ParseFloat(q.Get("start"), 64)
	if startSec < 0 || startSec != startSec {
		startSec = 0
	}

	if q.Get("transcode") == "1" {
		h.streamViaTranscode(w, r, infoHash, fileIdx, name, q.Get("hdr") == "1", audioIndex, startSec)
		return
	}

	if strings.EqualFold(filepath.Ext(name), ".mkv") && q.Get("mkv") == "false" {
		h.streamViaRemux(w, r, infoHash, fileIdx, name, audioIndex, startSec)
		return
	}

	rsc, err := h.mgr.OpenFile(infoHash, fileIdx)
	if err != nil {
		writeTorrentError(w, err)
		return
	}
	defer rsc.Close()

	w.Header().Set("Content-Type", contentTypeFor(name))
	http.ServeContent(w, r, name, time.Time{}, rsc)
}

// streamViaTranscode starts an HEVC/AV1→H264 transcode job reading the torrent
// file through our own /stream (anacrolix Reader — blocks until pieces arrive)
// and redirects to its HLS playlist. No ffprobe on the request path: the client
// already decided from the release name, so we return the redirect immediately
// instead of blocking on a codec probe of a cold torrent.
// pinUntilDone holds a Manager reader on the file while the remux job is still
// queued/running, so a job that waits in the queue past the idle-drop window
// (MaxTranscodes=1 backlog) doesn't get its torrent evicted, which would 404 the
// internal /stream ffmpeg fetches from → StateFailed.
func (h *torrentHandlers) pinUntilDone(infoHash string, fileIdx int, job *remux.Job) {
	rc, err := h.mgr.OpenFile(infoHash, fileIdx)
	if err != nil {
		return // best-effort; Prefetch already primed download
	}
	go func() {
		defer rc.Close()
		for {
			s := job.State()
			if s != remux.StateQueued && s != remux.StateRunning {
				return
			}
			time.Sleep(2 * time.Second)
		}
	}()
}

func (h *torrentHandlers) streamViaTranscode(w http.ResponseWriter, r *http.Request, infoHash string, fileIdx int, name string, hdr bool, audioIndex int, startSec float64) {
	src := h.selfStreamURL(infoHash, fileIdx, tokenFromRequest(r, true))
	_ = h.mgr.Prefetch(infoHash, fileIdx) // mux the full runtime so seeking reaches the real end
	// No request-path ProbeAudio: multi-audio is disabled (single inline mux), so
	// probing here only added an ~8s cold-torrent stall for nothing. The track
	// list for the menu is fetched separately via /torrents/audio.
	job, err := h.remuxQueue.SubmitTranscodeFrom(src, audioIndex, hdr, nil, startSec)
	if err != nil {
		h.logger.Error("torrent: transcode submit failed", "infohash", infoHash, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "не вдалося запустити транскод")
		return
	}
	h.logger.Info("torrent: transcoding → H264", "infohash", infoHash, "file", name, "hdr", hdr)
	h.pinUntilDone(infoHash, fileIdx, job)
	h.redirectToPlaylist(w, r, job)
}

func (h *torrentHandlers) streamViaRemux(w http.ResponseWriter, r *http.Request, infoHash string, fileIdx int, name string, audioIndex int, startSec float64) {
	src := h.selfStreamURL(infoHash, fileIdx, tokenFromRequest(r, true))
	_ = h.mgr.Prefetch(infoHash, fileIdx) // mux the full runtime so seeking reaches the real end
	// No request-path ProbeAudio (see streamViaTranscode) — removes the cold stall.
	job, err := h.remuxQueue.SubmitCopyMKVFrom(src, audioIndex, nil, startSec)
	if err != nil {
		h.logger.Error("torrent: mkv remux submit failed", "infohash", infoHash, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "не вдалося запустити remux")
		return
	}
	h.logger.Info("torrent: copy-remux mkv", "infohash", infoHash, "file", name)
	h.pinUntilDone(infoHash, fileIdx, job)
	h.redirectToPlaylist(w, r, job)
}

// redirectToPlaylist 302s to the job's HLS playlist. The not-ready state is
// handled by the ?hls=1 empty-live-m3u8 path. start=<sec> repeats the job's
// real offset in the URL: the queue may answer a start=N request with an
// older job that already covers N (remux.Queue.submit), and the player fetches
// /stream itself and reads the final URL / X-Remux-Start to set its timeBase.
func (h *torrentHandlers) redirectToPlaylist(w http.ResponseWriter, r *http.Request, job *remux.Job) {
	// The redirect target is a gated media route, so it must carry the caller's
	// token — through the one helper (this site used to append it unescaped).
	dest := "/remux/" + job.ID + "/playlist.m3u8?hls=1"
	if job.StartSec > 0 {
		dest += "&start=" + strconv.FormatFloat(job.StartSec, 'f', 0, 64)
	}
	http.Redirect(w, r, withMediaToken(dest, r.URL.Query().Get("t")), http.StatusFound)
}

func contentTypeFor(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".mkv":
		return "video/x-matroska"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".avi":
		return "video/x-msvideo"
	case ".webm":
		return "video/webm"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func writeTorrentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, torrentpkg.ErrNotFound), errors.Is(err, torrentpkg.ErrFileNotFound):
		writeNotFound(w, "not_found", "торрент або файл не знайдено")
	case errors.Is(err, torrentpkg.ErrMetadataTimeout):
		writeError(w, http.StatusGatewayTimeout, "metadata_timeout", "не вдалося отримати метадані торрента вчасно")
	case errors.Is(err, torrentpkg.ErrTooManyActive):
		writeError(w, http.StatusConflict, "too_many_active_torrents", "забагато активних торрентів, спочатку зупиніть інший")
	default:
		writeInternal(w, err)
	}
}
