// Night audio: the picture already dims at night (settings night_mode), this
// does the same for the sound — explosions stop waking the house and whispers
// stay audible. A WebAudio DynamicsCompressor on the <video>, NOT a server-side
// loudnorm: no re-encode, no second remux job per stream, works on progressive
// mp4 and live TV alike, and it can be switched mid-film.
//
// Device-local on purpose (localStorage, not a synced setting): routing a TV's
// audio through WebAudio is the kind of thing an old panel can get wrong, and a
// synced flag would then break every set in the house at once.

const STORE_KEY = 'promin:night_audio';

type AudioCtor = new () => AudioContext;

function ctor(): AudioCtor | null {
  const w = window as unknown as { AudioContext?: AudioCtor; webkitAudioContext?: AudioCtor };
  return w.AudioContext || w.webkitAudioContext || null;
}

export function nightAudioSupported(): boolean {
  return !!ctor();
}

export function isNightAudio(): boolean {
  try {
    return window.localStorage.getItem(STORE_KEY) === 'true';
  } catch (e) {
    return false;
  }
}

export function setNightAudioStored(on: boolean): void {
  try {
    window.localStorage.setItem(STORE_KEY, on ? 'true' : 'false');
  } catch (e) {
    /* private mode / quota — the toggle just won't be remembered */
  }
}

export interface NightAudio {
  // Build or bypass the chain to match the stored flag. Returns false when the
  // device could not do it, so the caller can turn the toggle back off.
  apply: (on: boolean) => boolean;
  destroy: () => void;
}

// Squash the peaks, then lift the whole thing back up: quiet dialogue ends up
// closer to the loud scenes instead of everything just being quieter.
const THRESHOLD_DB = -28;
const KNEE_DB = 24;
const RATIO = 6;
const ATTACK_S = 0.005;
const RELEASE_S = 0.3;
const MAKEUP_GAIN = 1.5;

export function attachNightAudio(video: HTMLVideoElement): NightAudio {
  const Ctor = ctor();
  let ctx: AudioContext | null = null;
  let source: MediaElementAudioSourceNode | null = null;
  let comp: DynamicsCompressorNode | null = null;
  let gain: GainNode | null = null;
  let broken = false;

  // createMediaElementSource is one-way: from then on the element's audio only
  // reaches the speakers through this graph, so "off" means wiring the source
  // straight to the destination rather than tearing anything down.
  function build(): boolean {
    if (!Ctor || broken) return false;
    try {
      if (!ctx) ctx = new Ctor();
      // A suspended context would silence the film, not just leave it uncompressed.
      if (ctx.state === 'suspended' && ctx.resume) ctx.resume();
      if (ctx.state === 'closed') return false;
      if (!source) source = ctx.createMediaElementSource(video);
      if (!comp) {
        comp = ctx.createDynamicsCompressor();
        comp.threshold.value = THRESHOLD_DB;
        comp.knee.value = KNEE_DB;
        comp.ratio.value = RATIO;
        comp.attack.value = ATTACK_S;
        comp.release.value = RELEASE_S;
      }
      if (!gain) {
        gain = ctx.createGain();
        gain.gain.value = MAKEUP_GAIN;
      }
      return true;
    } catch (e) {
      broken = true;
      return false;
    }
  }

  function apply(on: boolean): boolean {
    if (!on && !source) return true; // never touched the audio: nothing to undo
    if (!build()) return false;
    const src = source as MediaElementAudioSourceNode;
    const c = ctx as AudioContext;
    try {
      src.disconnect();
      if (comp) comp.disconnect();
      if (gain) gain.disconnect();
      if (on && comp && gain) {
        src.connect(comp);
        comp.connect(gain);
        gain.connect(c.destination);
      } else {
        src.connect(c.destination);
      }
      return true;
    } catch (e) {
      // Leave the sound working even if the chain failed halfway.
      try {
        src.connect(c.destination);
      } catch (e2) {
        /* nothing else to try */
      }
      broken = true;
      return false;
    }
  }

  return {
    apply: apply,
    destroy: function () {
      try {
        if (source) source.disconnect();
        if (comp) comp.disconnect();
        if (gain) gain.disconnect();
        if (ctx && ctx.close) ctx.close();
      } catch (e) {
        /* ignore */
      }
      ctx = null;
      source = null;
      comp = null;
      gain = null;
    },
  };
}
