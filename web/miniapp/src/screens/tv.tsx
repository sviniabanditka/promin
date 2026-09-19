// Live TV from the phone (docs/tv.md): favourites, recently watched and a
// search over the catalogue, each row with what is on now. Tapping a row
// switches the chosen TV to that channel (open_tv over the sync socket) — the
// phone does not play the stream itself: half the channels need the relay's
// headers and hls.js, which this bundle deliberately does not carry.

import { useEffect, useRef, useState } from 'preact/hooks';
import { getTvChannels, getTvNow, tvFavorite, type TvChannel, type TvNowNext } from '../api';
import { t } from '../i18n';
import { sendOpenTv, toast, useStore } from '../store';
import { haptic } from '../tg';
import { Empty, Skeleton, Segmented } from '../ui';

type Tab = 'fav' | 'recent' | 'search';

function hhmm(sec: number): string {
  const d = new Date(sec * 1000);
  return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0');
}

function Row({ ch, nn, onFav }: { ch: TvChannel; nn?: TvNowNext; onFav: (next: boolean) => void }) {
  const now = nn?.now;
  const pct = now && now.stop > now.start ? Math.min(100, Math.max(0, ((Date.now() / 1000 - now.start) / (now.stop - now.start)) * 100)) : 0;
  return (
    <div class="tvrow">
      <button
        class="tvrow-main"
        onClick={() => {
          haptic('select');
          sendOpenTv(ch.id, ch.name);
        }}
      >
        <div class="tvrow-logo">{ch.logo ? <img src={ch.logo} alt="" loading="lazy" /> : <span>{ch.name.slice(0, 2).toUpperCase()}</span>}</div>
        <div class="tvrow-text">
          <div class="tvrow-title">
            {ch.name}
            {ch.quality ? <span class="tvrow-q">{ch.quality}</span> : null}
          </div>
          <div class="tvrow-sub">{now ? hhmm(now.start) + ' · ' + now.title : t('tv.no_epg')}</div>
          {pct > 0 ? (
            <div class="tvrow-prog">
              <div style={{ width: pct + '%' }} />
            </div>
          ) : null}
        </div>
      </button>
      <button class={'tvrow-fav' + (ch.favorite ? ' on' : '')} aria-label={t('tv.favorite')} onClick={() => onFav(!ch.favorite)}>
        {ch.favorite ? '★' : '☆'}
      </button>
    </div>
  );
}

export function Tv({ query }: { query: URLSearchParams }) {
  const s = useStore();
  const [tab, setTab] = useState<Tab>((query.get('tab') as Tab) || 'fav');
  const [q, setQ] = useState(query.get('q') || '');
  const [items, setItems] = useState<TvChannel[] | null>(null);
  const [now, setNow] = useState<Record<string, TvNowNext>>({});
  const [state, setState] = useState<'idle' | 'loading' | 'error'>('idle');
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let alive = true;
    setItems(null);
    if (tab === 'search') {
      const term = q.trim();
      if (term.length < 2) {
        setState('idle');
        return undefined;
      }
      setState('loading');
      const id = setTimeout(() => {
        getTvChannels({ q: term, limit: 60 })
          .then((r) => {
            if (!alive) return;
            setItems(r.items || []);
            setState('idle');
          })
          .catch(() => alive && setState('error'));
      }, 350);
      return () => {
        alive = false;
        clearTimeout(id);
      };
    }
    setState('loading');
    getTvChannels(tab === 'fav' ? { fav: '1' } : { recent: '1', limit: 40 })
      .then((r) => {
        if (!alive) return;
        setItems(r.items || []);
        setState('idle');
      })
      .catch(() => alive && setState('error'));
    return () => {
      alive = false;
    };
  }, [tab, q, s.lang]);

  // One guide fetch covers every row on screen.
  useEffect(() => {
    let alive = true;
    getTvNow()
      .then((r) => alive && setNow(r.items || {}))
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [tab]);

  useEffect(() => {
    if (tab === 'search') input.current?.focus();
  }, [tab]);

  function toggleFav(ch: TvChannel, next: boolean): void {
    haptic('select');
    tvFavorite(ch.id, next)
      .then(() => {
        setItems((list) => (list || []).map((c) => (c.id === ch.id ? { ...c, favorite: next } : c)));
        toast(t(next ? 'tv.fav_added' : 'tv.fav_removed'));
      })
      .catch(() => toast(t('common.error')));
  }

  return (
    <div class="screen">
      <Segmented
        value={tab}
        options={[
          { value: 'fav', label: t('tv.favorites') },
          { value: 'recent', label: t('tv.recent') },
          { value: 'search', label: t('tv.search') },
        ]}
        onChange={setTab}
      />
      {tab === 'search' && (
        <input
          ref={input}
          class="input"
          type="search"
          value={q}
          placeholder={t('tv.search_hint')}
          onInput={(e) => setQ((e.target as HTMLInputElement).value)}
        />
      )}
      {state === 'error' ? (
        <Empty icon="⚠️" title={t('common.error')} />
      ) : state === 'loading' && !items ? (
        <div class="tvlist">
          {[0, 1, 2, 3, 4].map((i) => (
            <div key={i} class="tvrow">
              <div class="tvrow-main">
                <Skeleton cls="tvrow-logo" />
                <div class="tvrow-text">
                  <Skeleton w="60%" h="14px" />
                  <Skeleton w="85%" h="12px" />
                </div>
              </div>
            </div>
          ))}
        </div>
      ) : !items || !items.length ? (
        <Empty icon="📺" title={t(tab === 'fav' ? 'tv.no_favorites' : tab === 'recent' ? 'tv.no_recent' : 'tv.nothing_found')} hint={tab === 'fav' ? t('tv.fav_hint') : undefined} />
      ) : (
        <div class="tvlist">
          {items.map((ch) => (
            <Row key={ch.id} ch={ch} nn={now[ch.id]} onFav={(next) => toggleFav(ch, next)} />
          ))}
        </div>
      )}
    </div>
  );
}
