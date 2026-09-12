// Device-local resume points for YouTube videos (exact seconds). The account
// side is coarser — YouTube's history tile carries whole percents — so the
// video page prefers this when the same TV played the video. Not synced:
// the account's own history is the cross-device source (docs/youtube.md).

const KEY = 'promin:yt:resume';
const MAX = 200;

interface Rec {
  p: number; // position_sec
  d: number; // duration_sec
  at: number;
}

function load(): { [id: string]: Rec } {
  try {
    const raw = localStorage.getItem(KEY);
    const v = raw ? JSON.parse(raw) : null;
    return v && typeof v === 'object' ? v : {};
  } catch (e) {
    return {};
  }
}

function save(all: { [id: string]: Rec }): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(all));
  } catch (e) {
    /* quota / private mode: resume just isn't remembered */
  }
}

export function getYtResume(id: string): { position_sec: number; duration_sec: number } | null {
  const r = load()[id];
  return r ? { position_sec: r.p, duration_sec: r.d } : null;
}

export function setYtResume(id: string, positionSec: number, durationSec: number): void {
  const all = load();
  // Finished (or nearly): forget it, the next open starts over.
  if (durationSec > 0 && positionSec >= durationSec - 30) {
    if (all[id]) {
      delete all[id];
      save(all);
    }
    return;
  }
  all[id] = { p: Math.floor(positionSec), d: Math.floor(durationSec), at: Date.now() };
  const ids = Object.keys(all);
  if (ids.length > MAX) {
    ids.sort(function (a, b) {
      return all[a].at - all[b].at;
    });
    for (let i = 0; i < ids.length - MAX; i++) delete all[ids[i]];
  }
  save(all);
}
