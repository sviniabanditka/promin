package dvr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Timeshift buffers: while a viewer watches a channel, the same recorder keeps
// a rolling window of it on disk (docs/tv.md). That is what makes pause not
// lose the broadcast and "from the beginning" possible for the programme that
// is already running. The buffer belongs to the channel, not to the viewer —
// two TVs on the same channel share one ffmpeg.
const (
	// Segments kept in the sliding playlist: 600 × 6 s = 60 minutes — long
	// enough to go back to the start of a film or a match that is on now.
	// Disk per watched channel: ~1 GB at SD bitrates, 2–3 GB for HD.
	bufferSegments = 600
	// A buffer with no heartbeat for this long is nobody's: stop and delete.
	bufferIdle = 2 * time.Minute
	// Nothing buffers for longer than this in one run (a TV left on all night
	// still heartbeats; the cap bounds one ffmpeg, the sweep restarts it).
	bufferMaxSec = 6 * 3600
	// BufferWindowSec is what the client may seek back into, reported by the
	// API so the UI can say how far back it goes.
	BufferWindowSec = bufferSegments * 6
)

type buffer struct {
	cancel  context.CancelFunc
	touched time.Time
}

// BufferDir is where a channel's rolling window lives.
func (s *Service) BufferDir(channelID string) string {
	return filepath.Join(s.dir, "live", channelID)
}

// EnsureBuffer starts the rolling recorder for a channel if it is not running
// and marks it alive. Called on tune-in and then as a heartbeat; returns the
// window the client may seek back into. The recorder hangs off the service's
// own context — NOT the caller's request, which ends the moment this returns.
func (s *Service) EnsureBuffer(channelID string) (int, error) {
	s.mu.Lock()
	if b := s.buffers[channelID]; b != nil {
		b.touched = time.Now()
		s.mu.Unlock()
		return BufferWindowSec, nil
	}
	base := s.baseCtx
	s.mu.Unlock()
	if base == nil {
		base = context.Background()
	}

	free, err := freeBytes(s.dir)
	if err == nil && free < minFreeBytes {
		return 0, errNoDisk
	}
	url, ua, ref, err := s.source(channelID)
	if err != nil || url == "" {
		return 0, errNoStream
	}
	dir := s.BufferDir(channelID)
	if err := os.RemoveAll(dir); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	bctx, cancel := context.WithCancel(base)
	cmd := exec.CommandContext(bctx, s.ffmpeg, bufferArgs(url, ua, ref, dir)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return 0, err
	}
	s.mu.Lock()
	s.buffers[channelID] = &buffer{cancel: cancel, touched: time.Now()}
	s.mu.Unlock()
	s.logger.Info("dvr: timeshift buffer", "channel", channelID, "window_sec", BufferWindowSec)
	go func() {
		err := cmd.Wait()
		cancel()
		if err != nil {
			s.logger.Info("dvr: timeshift buffer ended", "channel", channelID, "error", err)
		}
		s.dropBuffer(channelID)
	}()
	return BufferWindowSec, nil
}

// bufferSweep stops the buffers nobody has asked about for a while.
func (s *Service) bufferSweep() {
	now := time.Now()
	s.mu.Lock()
	var stale []string
	for id, b := range s.buffers {
		if now.Sub(b.touched) > bufferIdle {
			stale = append(stale, id)
		}
	}
	s.mu.Unlock()
	for _, id := range stale {
		s.logger.Info("dvr: timeshift buffer idle, stopping", "channel", id)
		s.StopBuffer(id)
	}
}

// StopBuffer kills the recorder and removes the window.
func (s *Service) StopBuffer(channelID string) {
	s.mu.Lock()
	b := s.buffers[channelID]
	delete(s.buffers, channelID)
	s.mu.Unlock()
	if b != nil {
		b.cancel()
	}
	_ = os.RemoveAll(s.BufferDir(channelID))
}

// dropBuffer forgets a recorder that exited on its own (upstream died, cap hit)
// without deleting what it managed to buffer — the viewer may still be inside
// the window; the idle sweep cleans up later.
func (s *Service) dropBuffer(channelID string) {
	s.mu.Lock()
	delete(s.buffers, channelID)
	s.mu.Unlock()
}

// bufferArgs: a sliding HLS window, old segments deleted as they fall out.
func bufferArgs(url, ua, ref, dir string) []string {
	args := []string{"-y", "-nostdin", "-nostats", "-loglevel", "warning"}
	if ua != "" {
		args = append(args, "-user_agent", ua)
	}
	if ref != "" {
		args = append(args, "-headers", "Referer: "+ref+"\r\n")
	}
	args = append(args,
		"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5",
		"-i", url,
		"-t", strconv.Itoa(bufferMaxSec),
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c", "copy",
		"-f", "hls",
		"-hls_time", "6",
		"-hls_list_size", strconv.Itoa(bufferSegments),
		"-hls_flags", "delete_segments+omit_endlist",
		"-hls_segment_filename", filepath.Join(dir, "seg-%05d.ts"),
		filepath.Join(dir, PlaylistFile),
	)
	return args
}
