import { useEffect, useState } from 'preact/hooks';
import { fmtTime, seLabel, t } from '../i18n';
import { navigate } from '../router';
import { livePos, loadHome, sendRemote, targetDevice, useStore, type LiveState } from '../store';
import { Empty, PosterCard, Progress, Rail, SkeletonRail, useNow } from '../ui';

export function NowPlaying({ st }: { st: LiveState }) {
  const now = useNow(!st.paused);
  const pos = livePos(st, now);
  return (
    <section class="np" onClick={() => navigate('/remote')}>
      <div class="np-text">
        <div class="np-label">{t('home.now_playing')}</div>
        <div class="np-title">{st.title}</div>
        <div class="np-sub">
          {seLabel(st.season, st.episode)} {seLabel(st.season, st.episode) && '· '}
          {fmtTime(pos)} / {fmtTime(st.duration_sec)}
        </div>
        <Progress value={st.duration_sec ? pos / st.duration_sec : 0} cls="np-prog" />
      </div>
      <button
        class="np-play"
        aria-label="play/pause"
        onClick={(e) => {
          e.stopPropagation();
          sendRemote('toggle_play');
        }}
      >
        {st.paused ? '▶' : '⏸'}
      </button>
    </section>
  );
}

export function Home() {
  const s = useStore();
  const [err, setErr] = useState(false);
  useEffect(() => {
    if (s.home) return;
    setErr(false);
    loadHome().catch(() => setErr(true));
  }, [s.home]);
  const dev = targetDevice(s);
  const st = dev ? s.states[dev.id] : undefined;
  const rows = s.home?.rows.filter((r) => r.items?.length) ?? [];
  return (
    <div class="screen">
      <button class="searchbar" onClick={() => navigate('/search')}>
        <span>🔍</span>
        <span class="hint">{t('home.search_placeholder')}</span>
      </button>
      {st && <NowPlaying st={st} />}
      {!s.home ? (
        err ? (
          <Empty icon="⚠️" title={t('common.error')}>
            <button class="btn" onClick={() => loadHome().catch(() => setErr(true))}>
              {t('common.retry')}
            </button>
          </Empty>
        ) : (
          <>
            <SkeletonRail />
            <SkeletonRail />
            <SkeletonRail />
          </>
        )
      ) : rows.length === 0 ? (
        <Empty icon="🎬" title={t('home.empty')} />
      ) : (
        rows.map((r) => (
          <Rail key={r.id} title={r.title}>
            {r.items.map((c) => (
              <PosterCard key={c.type + c.tmdb_id} card={c} />
            ))}
          </Rail>
        ))
      )}
    </div>
  );
}
