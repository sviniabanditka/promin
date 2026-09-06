import { useEffect, useState } from 'preact/hooks';
import { ApiError, getTitle, type Card, type Episode, type MediaType, type Timecode } from '../api';
import { fmtTime, t } from '../i18n';
import { isBookmarked, sendOpen, targetDevice, toggleBookmark, useStore, type State } from '../store';
import { haptic, tg } from '../tg';
import { Empty, Poster, Progress, Skeleton } from '../ui';

// Latest timecode for this title: live store copy (WS-updated) wins over the card's.
function latestTimecode(s: State, card: Card): Timecode | null {
  const mine = (s.timecodes ?? []).filter((x) => x.tmdb_id === card.tmdb_id && x.media_type === card.type);
  if (mine.length) {
    const best = mine.reduce((a, b) => (b.updated_at > a.updated_at ? b : a));
    return best.duration_sec > 0 && best.position_sec > 0 ? best : null;
  }
  return card.timecode && card.timecode.duration_sec > 0 ? card.timecode : null;
}

export function Title({ type, id }: { type: MediaType; id: number }) {
  const s = useStore();
  const [card, setCard] = useState<Card | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [season, setSeason] = useState<number | null>(null);
  const [eps, setEps] = useState<Record<number, Episode[] | null>>({});

  useEffect(() => {
    setCard(null);
    setErr(null);
    setEps({});
    getTitle(type, id)
      .then((c) => {
        setCard(c);
        const seasons = c.seasons ?? [];
        const fromTc = c.timecode?.season;
        setSeason(seasons.some((x) => x.season === fromTc) ? fromTc! : (seasons.find((x) => x.season > 0) ?? seasons[0])?.season ?? null);
      })
      .catch((e) => setErr(e instanceof ApiError && e.code === 'title_not_found' ? t('title.not_found') : t('common.error')));
  }, [type, id, s.lang]);

  // Episodes for the selected season: from the card when present, else lazy-load.
  useEffect(() => {
    if (!card || season == null || eps[season] !== undefined) return;
    const inline = card.seasons?.find((x) => x.season === season)?.episodes;
    if (inline?.length) {
      setEps((m) => ({ ...m, [season]: inline }));
      return;
    }
    getTitle(type, id, season)
      .then((r) => setEps((m) => ({ ...m, [season]: r.episodes ?? r.seasons?.find((x) => x.season === season)?.episodes ?? [] })))
      .catch(() => setEps((m) => ({ ...m, [season]: null })));
  }, [card, season]);

  const dev = targetDevice(s);
  const canSend = !!dev?.online;
  const tc = card ? latestTimecode(s, card) : null;
  const bookmarked = card ? isBookmarked(s, card.tmdb_id, card.type, card.in_bookmarks) : false;

  const doContinue = () =>
    card && sendOpen({ tmdb_id: card.tmdb_id, media_type: card.type, resume: true, season: tc?.season ?? undefined, episode: tc?.episode ?? undefined });
  const doWatch = () => card && sendOpen({ tmdb_id: card.tmdb_id, media_type: card.type });
  const primaryText = tc ? t('title.continue') : type === 'movie' ? t('title.watch') : t('title.on_tv');
  const primary = tc ? doContinue : doWatch;

  useEffect(() => {
    const mb = tg?.MainButton;
    if (!mb || !card) return;
    const onClick = () => {
      haptic('tap');
      primary();
    };
    mb.setParams({ text: primaryText, is_visible: true, is_active: canSend });
    mb.onClick(onClick);
    return () => {
      mb.offClick(onClick);
      mb.hide();
    };
  }, [card, primaryText, canSend, tc?.position_sec, dev?.id]);

  if (err) return <Empty icon="😕" title={err} />;
  if (!card)
    return (
      <div class="screen title">
        <div class="hero">
          <Skeleton cls="poster poster-lg" />
          <div class="hero-meta">
            <Skeleton w="80%" h="22px" />
            <Skeleton w="50%" h="14px" />
            <Skeleton w="60%" h="14px" />
          </div>
        </div>
        <Skeleton w="100%" h="60px" />
      </div>
    );

  const seasons = card.seasons ?? [];
  const list = season == null ? undefined : eps[season];
  const meta = [card.year, ...(card.genres ?? []).slice(0, 3)].filter(Boolean).join(' · ');

  return (
    <div class="screen title">
      {card.backdrop && <div class="backdrop" style={{ backgroundImage: `url(${card.backdrop})` }} />}
      <div class="hero">
        <Poster path={card.poster} size="w342" cls="poster-lg" alt={card.title} />
        <div class="hero-meta">
          <h1 class="hero-title">{card.title}</h1>
          {card.original_title && card.original_title !== card.title && <div class="hero-orig">{card.original_title}</div>}
          <div class="hero-sub">{meta}</div>
          <div class="pills">
            {!!card.rating && <span class="pill">★ {card.rating.toFixed(1)}</span>}
            {!!card.imdb_rating && <span class="pill">IMDb {card.imdb_rating.toFixed(1)}</span>}
            {!!card.runtime_minutes && (
              <span class="pill">
                {card.runtime_minutes} {t('common.min')}
              </span>
            )}
            {card.content_rating && <span class="pill">{card.content_rating}</span>}
          </div>
          <button class={'btn btn-ghost bm' + (bookmarked ? ' on' : '')} onClick={() => toggleBookmark(card.tmdb_id, card.type, bookmarked)}>
            {bookmarked ? '★ ' + t('title.bookmarked') : '☆ ' + t('title.bookmark')}
          </button>
        </div>
      </div>

      <div class="actions">
        {tc && (
          <button class="btn" disabled={!canSend} onClick={doContinue}>
            {t('title.continue')}
            <small>
              {tc.season != null ? `S${tc.season} E${tc.episode} · ` : ''}
              {fmtTime(tc.position_sec)}
            </small>
          </button>
        )}
        <button class={'btn' + (tc ? ' btn-ghost' : '')} disabled={!canSend} onClick={doWatch}>
          {type === 'movie' ? t('title.watch') : t('title.on_tv')}
        </button>
      </div>
      {!canSend && <div class="note">{t(dev ? 'remote.offline' : 'remote.no_device')}</div>}

      {card.overview && <p class="overview">{card.overview}</p>}

      {seasons.length > 0 && (
        <>
          <div class="chips">
            {seasons.map((x) => (
              <button
                key={x.season}
                class={'chip' + (x.season === season ? ' on' : '')}
                onClick={() => {
                  haptic('select');
                  setSeason(x.season);
                }}
              >
                {x.name || t('common.season') + ' ' + x.season}
              </button>
            ))}
          </div>
          <div class="eps">
            {list === undefined ? (
              [0, 1, 2].map((i) => <Skeleton key={i} w="100%" h="64px" />)
            ) : list === null ? (
              <Empty icon="⚠️" title={t('common.error')} />
            ) : list.length === 0 ? (
              <Empty icon="📺" title={t('title.no_episodes')} />
            ) : (
              list.map((e) => (
                <div key={e.episode} class="ep">
                  <div class="ep-still">
                    <Poster path={e.still} size="w300" cls="poster-still" />
                    {e.timecode && e.timecode.duration_sec > 0 && <Progress value={e.timecode.position_sec / e.timecode.duration_sec} cls="ep-prog" />}
                  </div>
                  <div class="ep-text">
                    <div class="ep-title">
                      {e.episode}. {e.name}
                    </div>
                    <div class="ep-sub">
                      {[e.runtime_minutes ? e.runtime_minutes + ' ' + t('common.min') : '', e.rating ? '★ ' + e.rating.toFixed(1) : ''].filter(Boolean).join(' · ')}
                    </div>
                  </div>
                  <button
                    class="ep-play"
                    disabled={!canSend}
                    aria-label="play"
                    onClick={() => {
                      haptic('tap');
                      sendOpen({ tmdb_id: card.tmdb_id, media_type: card.type, season: season!, episode: e.episode });
                    }}
                  >
                    ▶
                  </button>
                </div>
              ))
            )}
          </div>
        </>
      )}
    </div>
  );
}
