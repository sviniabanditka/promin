import { useRef, useState } from 'preact/hooks';
import { fmtTime, seLabel, t } from '../i18n';
import { navigate, titlePath } from '../router';
import { livePos, sendOpen, sendRemote, targetDevice, toast, useStore, type LiveState } from '../store';
import { haptic } from '../tg';
import { Empty, MediaRow, useNow } from '../ui';

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

      <div class="pad pad-aux">
        <button class="key" onClick={() => sendRemote('mute')} aria-label="mute">
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
