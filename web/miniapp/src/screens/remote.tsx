import { useEffect, useRef, useState } from 'preact/hooks';
import type { NavAction } from '../api';
import { fmtTime, seLabel, t } from '../i18n';
import { navigate, titlePath } from '../router';
import { livePos, sendOpen, sendRemote, sendRemoteStr, targetDevice, toast, useStore, type LiveState } from '../store';
import { haptic } from '../tg';
import { Empty, MediaRow, Sheet, useNow } from '../ui';

const SLEEP = [15, 30, 45, 60, 90];

function Scrubber({ st }: { st: LiveState }) {
  const now = useNow(!st.paused);
  const [drag, setDrag] = useState<number | null>(null);
  const track = useRef<HTMLDivElement>(null);
  const dur = st.duration_sec || 0;
  const pos = drag ?? livePos(st, now);
  const ratio = dur ? Math.min(1, pos / dur) : 0;

  const at = (clientX: number) => {
    const r = track.current!.getBoundingClientRect();
    return Math.max(0, Math.min(1, (clientX - r.left) / r.width)) * dur;
  };
  return (
    <div class="scrub-wrap">
      <div
        ref={track}
        class={'scrub' + (drag != null ? ' dragging' : '')}
        onPointerDown={(e) => {
          if (!dur) return;
          e.currentTarget.setPointerCapture(e.pointerId);
          setDrag(at(e.clientX));
          haptic('select');
        }}
        onPointerMove={(e) => drag != null && setDrag(at(e.clientX))}
        onPointerUp={(e) => {
          if (drag == null) return;
          const v = at(e.clientX);
          setDrag(null);
          sendRemote('seek_to', Math.round(v * 10) / 10);
        }}
        onPointerCancel={() => setDrag(null)}
      >
        <div class="scrub-fill" style={{ width: ratio * 100 + '%' }} />
        <div class="scrub-knob" style={{ left: ratio * 100 + '%' }} />
      </div>
      <div class="scrub-times">
        <span>{fmtTime(pos)}</span>
        <span>-{fmtTime(Math.max(0, dur - pos))}</span>
      </div>
    </div>
  );
}

// Audio / subtitles / volume chips under the transport row; each opens a sheet.
function Tracks({ st }: { st: LiveState }) {
  const [open, setOpen] = useState<'voice' | 'subs' | 'vol' | null>(null);
  const voices = st.voices ?? [];
  const subs = st.subtitles ?? [];
  const voice = voices.find((v) => v.id === st.voice_id)?.name ?? st.voice ?? '';
  const sub = subs.find((x) => x.id === st.subtitle_id);
  const subLabel = !sub || sub.id === 'off' ? t('common.off') : sub.label;
  const vol = st.volume ?? 0;
  // Slider: local value while dragging, one `volume` send 150 ms after the last change.
  const [drag, setDrag] = useState<number | null>(null);
  const timer = useRef(0);
  useEffect(() => () => clearTimeout(timer.current), []);
  const onVol = (v: number) => {
    setDrag(v);
    clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      setDrag(null);
      sendRemote('volume', v);
    }, 150);
  };
  const chip = (kind: 'voice' | 'subs' | 'vol', icon: string, val: string, enabled: boolean, label: string) => (
    <button
      class="chip"
      disabled={!enabled}
      aria-label={label}
      onClick={() => {
        haptic('select');
        setOpen(kind);
      }}
    >
      {icon}
      <span class="chip-val">{val}</span>
    </button>
  );
  const rows = (list: { id: string; label: string }[], cur: string, action: 'set_voice' | 'set_subtitle') =>
    list.map((x) => (
      <button
        key={x.id}
        class={'sheet-row' + (x.id === cur ? ' on' : '')}
        onClick={() => {
          setOpen(null);
          if (x.id !== cur) sendRemoteStr(action, x.id);
        }}
      >
        <span class="sheet-row-name">{x.id === 'off' ? t('common.off') : x.label}</span>
      </button>
    ));
  return (
    <>
      <div class="chips tracks">
        {chip('voice', '🎙', voice || t('remote.voice'), voices.length > 0, t('remote.voice'))}
        {chip('subs', '💬', subLabel, subs.length > 0, t('remote.subs'))}
        {chip('vol', st.muted ? '🔇' : '🔊', (drag ?? vol) + '%', true, t('remote.volume'))}
      </div>
      <Sheet open={open === 'voice'} title={t('remote.voice')} onClose={() => setOpen(null)}>
        {rows(
          voices.map((v) => ({ id: v.id, label: v.name })),
          st.voice_id ?? '',
          'set_voice'
        )}
      </Sheet>
      <Sheet open={open === 'subs'} title={t('remote.subs')} onClose={() => setOpen(null)}>
        {rows(subs, st.subtitle_id ?? 'off', 'set_subtitle')}
      </Sheet>
      <Sheet open={open === 'vol'} title={t('remote.volume')} onClose={() => setOpen(null)}>
        <div class="vol">
          <button class="key" onClick={() => sendRemote('mute')} aria-label="mute">
            {st.muted ? '🔇' : '🔊'}
          </button>
          <input class="range" type="range" min={0} max={100} step={1} value={drag ?? vol} onInput={(e) => onVol(Number((e.currentTarget as HTMLInputElement).value))} />
          <span class="vol-num">{drag ?? vol}%</span>
        </div>
      </Sheet>
    </>
  );
}

// Arrows + OK + Back for the TV UI itself (title page, source picker, player
// menus). Lives in the Remote tab and in the sheet that pops after "open on TV".
export function DPad() {
  const key = (action: NavAction, label: preact.ComponentChildren, cls = '') => (
    <button class={'key ' + cls} onClick={() => sendRemote(action)} aria-label={action}>
      {label}
    </button>
  );
  return (
    <div class="dpad">
      <span />
      {key('nav_up', '▲')}
      <span />
      {key('nav_left', '◀')}
      {key('nav_ok', 'OK', 'key-ok')}
      {key('nav_right', '▶')}
      {key(
        'nav_back',
        <span class="key-stack">
          ↩<small>{t('remote.back')}</small>
        </span>,
        'key-back'
      )}
      {key('nav_down', '▼')}
      <span />
    </div>
  );
}

export function Remote() {
  const s = useStore();
  const [sleepOpen, setSleepOpen] = useState(false);
  const dev = targetDevice(s);
  const st = dev ? s.states[dev.id] : undefined;

  if (!dev || !dev.online)
    return (
      <div class="screen remote">
        <Empty icon="📺" title={t(dev ? 'remote.offline' : 'remote.no_device')} hint={t('remote.no_device_hint')} />
      </div>
    );

  if (!st) {
    const cont = s.home?.rows.find((r) => r.id === 'continue_watching')?.items.slice(0, 6) ?? [];
    return (
      <div class="screen remote">
        <DPad />
        <Empty icon="💤" title={t('remote.nothing')} hint={t('remote.nothing_hint')} />
        {cont.length > 0 && (
          <>
            <h2 class="rail-title">{t('remote.continue')}</h2>
            {cont.map((c) => (
              <MediaRow
                key={c.type + c.tmdb_id}
                card={c}
                subtitle={c.timecode ? [seLabel(c.timecode.season, c.timecode.episode), fmtTime(c.timecode.position_sec)].filter(Boolean).join(' · ') : undefined}
                right={
                  <button
                    class="ep-play"
                    aria-label="continue"
                    onClick={() =>
                      sendOpen({
                        tmdb_id: c.tmdb_id,
                        media_type: c.type,
                        resume: true,
                        season: c.timecode?.season ?? undefined,
                        episode: c.timecode?.episode ?? undefined,
                      })
                    }
                  >
                    ▶
                  </button>
                }
              />
            ))}
          </>
        )}
      </div>
    );
  }

  const se = seLabel(st.season, st.episode);
  return (
    <div class="screen remote">
      <button class="remote-head" onClick={() => navigate(titlePath(st.media_type, st.tmdb_id))}>
        <div class="remote-title">{st.title}</div>
        <div class="remote-sub">{[se, st.voice, st.source].filter(Boolean).join(' · ')}</div>
      </button>

      <Scrubber st={st} />

      <div class="pad">
        <button class="key" onClick={() => sendRemote('prev')} aria-label="previous">
          ⏮
        </button>
        <button class="key" onClick={() => sendRemote('seek', -30)} aria-label="back 30 s">
          <span class="key-stack">
            ⏪<small>30</small>
          </span>
        </button>
        <button class="key key-main" onClick={() => sendRemote('toggle_play')} aria-label="play/pause">
          {st.paused ? '▶' : '⏸'}
        </button>
        <button class="key" onClick={() => sendRemote('seek', 30)} aria-label="forward 30 s">
          <span class="key-stack">
            ⏩<small>30</small>
          </span>
        </button>
        <button class="key" onClick={() => sendRemote('next')} aria-label="next">
          ⏭
        </button>
      </div>

      <Tracks st={st} />

      <div class="pad pad-aux">
        <button class={'key' + (st.muted ? ' on' : '')} onClick={() => sendRemote('mute')} aria-label="mute">
          🔇
        </button>
        <button class="key" onClick={() => sendRemote('night')} aria-label="night mode">
          🌙
        </button>
        <button
          class={'key' + (sleepOpen ? ' on' : '')}
          onClick={() => {
            haptic('select');
            setSleepOpen(!sleepOpen);
          }}
          aria-label="sleep timer"
        >
          😴
        </button>
      </div>

      <DPad />

      {sleepOpen && (
        <div class="sleep">
          <div class="sleep-title">{t('remote.sleep')}</div>
          <div class="chips">
            {SLEEP.map((m) => (
              <button
                key={m}
                class="chip"
                onClick={async () => {
                  setSleepOpen(false);
                  if (await sendRemote('sleep', m)) toast(t('remote.sleep_set', { n: m }));
                }}
              >
                {m} {t('remote.min')}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
