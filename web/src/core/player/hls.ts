// Lazy loader for hls.js (docs/frontend.md + §6). hls.js (FULL build —
// the light build has no AudioStreamController, so demuxed-audio HLS played
// silently; see docs/streaming.md) is ~415 KB and
// is NOT in the main bundle — it's injected as a <script> tag from our own
// origin only the first time a stream actually needs it, and never on Tizen
// (which uses native <video> HLS). We use a manual script tag + onload
// Promise rather than dynamic import(): code-splitting via import() is
// unreliable on ES5 targets and old Tizen webviews (docs/frontend.md).
//
// ES5 target: plain functions, no async/await.

// Minimal shape of the hls.js constructor we rely on. The real thing carries
// far more; we only touch these members.
export interface HlsInstance {
  loadSource(url: string): void;
  attachMedia(video: HTMLMediaElement): void;
  destroy(): void;
  on(event: string, cb: (event: string, data: unknown) => void): void;
  currentLevel: number;
  // True while ABR picks the level (currentLevel = -1 re-enables it).
  autoLevelEnabled?: boolean;
  levels: Array<{ height?: number; bitrate?: number; name?: string }>;
  // Rolling channel-throughput estimate (bits/s) — used by the info bar.
  bandwidthEstimate?: number;
  // In-manifest audio tracks (for the ☰ track picker). get/set index.
  audioTracks?: Array<{ id?: number; name?: string; lang?: string; label?: string }>;
  audioTrack?: number;
  // In-manifest WebVTT subtitle renditions. subtitleTrack -1 = none;
  // subtitleDisplay gates whether hls.js flips the TextTrack to "showing".
  subtitleTracks?: Array<{ id?: number; name?: string; lang?: string }>;
  subtitleTrack?: number;
  subtitleDisplay?: boolean;
}

export interface HlsCtor {
  new (config?: unknown): HlsInstance;
  isSupported(): boolean;
  Events: {
    MANIFEST_PARSED: string;
    ERROR: string;
    AUDIO_TRACKS_UPDATED: string;
    AUDIO_TRACK_SWITCHED: string;
    SUBTITLE_TRACKS_UPDATED: string;
    LEVEL_SWITCHED: string;
    [k: string]: string;
  };
  ErrorTypes: { [k: string]: string };
  [k: string]: unknown;
}

const SRC = '/vendor/hls.min.js';

let loading: Promise<HlsCtor | null> | null = null;

function getGlobalHls(): HlsCtor | null {
  const w = window as unknown as { Hls?: HlsCtor };
  return w.Hls && typeof w.Hls.isSupported === 'function' ? w.Hls : null;
}

// Resolves with the Hls constructor, or null if the script fails to load or
// MSE is unsupported on this device (caller falls back / shows an error).
export function ensureHls(): Promise<HlsCtor | null> {
  const existing = getGlobalHls();
  if (existing) {
    return Promise.resolve(existing);
  }
  if (loading) {
    return loading;
  }

  loading = new Promise<HlsCtor | null>(function (resolve) {
    const script = document.createElement('script');
    script.src = SRC;
    script.async = true;

    let settled = false;
    const timer = window.setTimeout(function () {
      if (settled) return;
      settled = true;
      // Drop the cached promise so a slow first load doesn't poison HLS for the
      // whole session — the next source retries the script instead of getting
      // this resolved-null back forever.
      loading = null;
      resolve(null);
    }, 15000);

    script.onload = function () {
      if (settled) return;
      settled = true;
      window.clearTimeout(timer);
      resolve(getGlobalHls());
    };
    script.onerror = function () {
      if (settled) return;
      settled = true;
      window.clearTimeout(timer);
      loading = null; // allow a retry on the next open
      resolve(null);
    };

    (document.head || document.body || document.documentElement).appendChild(script);
  });

  return loading;
}
