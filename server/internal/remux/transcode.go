package remux

// KindTranscodeHEVC is now a live pipeline (docs/streaming.md, phases Т1–Т2):
//
//   - Detection: ProbeVideo (probe.go) ffprobes a source's video codec + HDR;
//     the torrent stream handler triggers a transcode when the client reports
//     no HEVC decoder (hevc=false) and ffprobe sees HEVC/AV1.
//   - Execution: buildTranscodeHEVCArgs (ffmpeg.go) transcodes to H264 in a VOD
//     HLS playlist (same serve/poll path as copy jobs); Queue.run bounds it with
//     a dedicated transcodeSem (cfg.MaxTranscodes), separate from the cheap copy
//     limiter, since transcode is the ~2-3 vCPU path.
//
// Т4 HDR→SDR tone-map: run() passes tonemap = job.HDR && q.zscale; the ffmpeg
// build is probed once (HasZscale) so an HDR source maps to SDR when zscale is
// present, else transcodes washed (still plays). Т5 realtime: buildTranscode
// downscales to ≤1080p (4K→4K is infeasible on the budget) and forces 8-bit.
// Т6: TranscodeQueueDepth feeds queue_position into the /remux 202 body.
//
// Still open (see docs/streaming.md):
//   - Т6 client UI (queue_position only surfaces on the /remux JSON-poll path;
//     the torrent path plays via a native <video> 302-redirect, no poll) and
//     "сейчас смотрят" priority preemption of a running job.
