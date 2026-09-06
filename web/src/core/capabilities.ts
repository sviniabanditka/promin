// Device capability detection (docs/streaming.md/§5, docs/frontend.md
// §6 "Device capabilities репорт"). The backend uses these flags to decide
// whether it can relay a stream 1:1 or must remux/transcode before handing
// it to our <video>. We compute them once at boot and ship them as flat query
// params on /sources/online and /sources/online/resolve.
//
// Detection policy (docs/frontend.md item 2): capability-detect everything we can
// (canPlayType / MediaSource.isTypeSupported); the ONLY User-Agent sniff is
// platform + old-Tizen, which is the single reliable signal for the two
// hard-coded limitations of the pre-2022 Samsung webview (Chromium ~47):
//   - demuxed_hls = false  (hls.js on Chromium 47 can't pick up separate
//     EXT-X-MEDIA audio groups — proven, docs/streaming.md table)
//   - max_http_version = 'h1'  (sustained h2 media dies with
//     PIPELINE_ERROR_NETWORK — proven, docs/streaming.md table)
//
// ES5 target (swc-transpiled): plain functions, no Array.find/includes,
// no Object.assign.

export type Platform = 'tizen' | 'webos' | 'androidtv' | 'browser';
export type HttpVersion = 'h1' | 'h2';

export interface Capabilities {
  platform: Platform;
  hevc: boolean;
  // false hard for old Tizen (known limitation); true elsewhere.
  demuxed_hls: boolean;
  // <video> can play application/vnd.apple.mpegurl directly (Safari/Tizen).
  hls_native: boolean;
  // <video> can demux Matroska containers itself (almost never true in a
  // TV webview — kept for the backend's MKV path decision).
  mkv: boolean;
  // Known statically from platform, never probed at runtime (docs/frontend.md).
  max_http_version: HttpVersion;
}

function ua(): string {
  try {
    return (window.navigator && window.navigator.userAgent ? window.navigator.userAgent : '') || '';
  } catch (e) {
    return '';
  }
}

function detectPlatform(userAgent: string): Platform {
  const s = userAgent.toLowerCase();
  // window.tizen is the reliable Tizen signal; fall back to the UA token.
  if ((window as unknown as { tizen?: unknown }).tizen !== undefined || s.indexOf('tizen') !== -1) {
    return 'tizen';
  }
  if (
    s.indexOf('webos') !== -1 ||
    s.indexOf('web0s') !== -1 ||
    (window as unknown as { webOS?: unknown }).webOS !== undefined ||
    (window as unknown as { PalmSystem?: unknown }).PalmSystem !== undefined
  ) {
    return 'webos';
  }
  if (s.indexOf('android') !== -1) {
    return 'androidtv';
  }
  return 'browser';
}

function canPlay(type: string): boolean {
  try {
    const v = document.createElement('video');
    if (!v.canPlayType) {
      return false;
    }
    const res = v.canPlayType(type);
    return res === 'probably' || res === 'maybe';
  } catch (e) {
    return false;
  }
}

function supportsHevc(): boolean {
  try {
    const MS = (window as unknown as { MediaSource?: { isTypeSupported?: (t: string) => boolean } }).MediaSource;
    if (MS && typeof MS.isTypeSupported === 'function') {
      return MS.isTypeSupported('video/mp4; codecs="hvc1.1.6.L93.B0"') || MS.isTypeSupported('video/mp4; codecs="hvc1"');
    }
  } catch (e) {
    /* fall through */
  }
  // Fall back to the <video> probe (some webviews expose it there only).
  return canPlay('video/mp4; codecs="hvc1"');
}

function detect(): Capabilities {
  const userAgent = ua();
  const platform = detectPlatform(userAgent);

  return {
    platform: platform,
    hevc: supportsHevc(),
    // Old Samsung Tizen (pre-2022) cannot play demuxed-audio HLS
    // (#EXT-X-MEDIA:TYPE=AUDIO): native → "формат не поддерживается", and even
    // hls.js there hangs "loading" forever (MSE/codec + h1 segment starvation).
    // Verified on a real TV: veoveo (demuxed) never plays, collaps (muxed)
    // does. So old Tizen gets demuxed_hls=false → the facade routes such HLS
    // through /remux (ffmpeg muxes video+one audio into a single stream the
    // player handles like collaps). Modern targets keep true (hls.js, no remux).
    // No User-Agent autodetect any more: the old-Samsung limitations are
    // driven solely by the device-local "Режим старого ТВ" setting
    // (legacyOverride below), per the owner's decision — docs/streaming.md
    demuxed_hls: true,
    hls_native: canPlay('application/vnd.apple.mpegurl') || canPlay('application/x-mpegURL'),
    mkv: canPlay('video/x-matroska'),
    max_http_version: 'h2',
  };
}

// Computed once — cheap, and the answer never changes within a session.
export const caps: Capabilities = detect();

// "Режим старого ТВ" override (core/settings.ts). When the user turns it on we
// force demuxed_hls=false regardless of autodetect, so the backend takes the
// remux path on a problematic firmware the UA-sniff didn't catch. Kept as a
// one-way setter (settings → capabilities) to avoid an import cycle; capsQuery
// reads it at request time. Autodetect still forces false on old Tizen — this
// can only make the flag stricter, never looser.
let legacyOverride = false;

export function setLegacyOverride(on: boolean): void {
  legacyOverride = on;
}
export function isLegacyMode(): boolean {
  return legacyOverride;
}


// Convenience: is this the platform where we prefer native <video> HLS over
// hls.js (docs/frontend.md pickEngine)?
//
// Only a MODERN Tizen with native HLS. The pre-2022 Samsung webview
// (demuxed_hls=false) is explicitly EXCLUDED: its native player can't play
// demuxed-audio HLS ("формат не поддерживается", MEDIA_ERR code 4), whereas
// hls.js there works fine (it has MSE) — the only remaining issue on old
// Tizen is HTTP/2 breaking sustained media, which the :8443 h1-only front
// handles at the transport layer. So old Tizen → hls.js, not native.
export function preferNativeHls(): boolean {
  // Modern Tizen only (max_http_version h2). Old Samsung (h1) → hls.js: its
  // native player can't play demuxed HLS ("формат не поддерживается"), while
  // hls.js can.
  return caps.platform === 'tizen' && caps.hls_native && !legacyOverride;
}

// canDecodeHevc reports whether this device can ACTUALLY play HEVC in the engine
// it'll use, not just whether MediaSource.isTypeSupported('hvc1') returned true.
// The catch: our HLS playback goes through hls.js (MSE) on every target except a
// modern native-HLS Tizen, and hls.js/MSE can't decode HEVC in practice (the
// isTypeSupported probe over-promises). So HEVC is only really playable on the
// native-HLS path; everywhere else it must be transcoded to H264. Used to decide
// the torrent-stream hevc=false flag (→ server transcodes HEVC→H264).
export function canDecodeHevc(): boolean {
  return caps.hevc && preferNativeHls();
}

// Flatten capabilities into query params for /sources/online and
// /sources/online/resolve (and later /remux). Booleans as "true"/"false"
// strings, which the Go facade parses. i18n `lang` is added by core/api.
export function capsQuery(): { [k: string]: string } {
  return {
    platform: caps.platform,
    hevc: caps.hevc ? 'true' : 'false',
    // false on old Tizen (autodetect) or when the user forces "старый ТВ"
    // mode → facade routes demuxed HLS through /remux (see detect() above).
    demuxed_hls: legacyOverride || !caps.demuxed_hls ? 'false' : 'true',
    hls_native: caps.hls_native ? 'true' : 'false',
    mkv: caps.mkv ? 'true' : 'false',
    max_http_version: legacyOverride ? 'h1' : caps.max_http_version,
  };
}
