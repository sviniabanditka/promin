// Live TV from the phone (docs/tv.md): favourites, recently watched, a search
// over the catalogue and the profile's recordings. A channel row shows what is
// on now; tapping it switches the chosen TV to the channel (open_tv over the
// sync socket), ⏺ records the running programme (again = cancel), the star
// toggles favourite. The phone does not play the stream itself: half the
// channels need the relay's headers and hls.js, which this bundle deliberately
// does not carry.

import { useEffect, useRef, useState } from 'preact/hooks';
import { addTvRecord, deleteTvRecord, getTvChannels, getTvNow, getTvRecords, tvFavorite, type TvChannel, type TvNowNext, type TvRecord } from '../api';
import { t } from '../i18n';
import { sendOpenTv, toast, useStore } from '../store';
import { confirmDialog, haptic } from '../tg';
import { Empty, Skeleton, Segmented } from '../ui';

type Tab = 'fav' | 'recent' | 'search' | 'records';

function hhmm(sec: number): string {
  const d = new Date(sec * 1000);
  return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0');
}
function ddmm(sec: number): string {
  const d = new Date(sec * 1000);
  return String(d.getDate()).padStart(2, '0') + '.' + String(d.getMonth() + 1).padStart(2, '0');
}

function Row({ ch, nn, recording, onFav, onRec }: { ch: TvChannel; nn?: TvNowNext; recording: boolean; onFav: (next: boolean) => void; onRec: () => void }) {
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
      {now ? (
        <button class={'tvrow-rec' + (recording ? ' on' : '')} aria-label={t('tv.record')} onClick={onRec}>
          ⏺
        </button>
      ) : null}
      <button class={'tvrow-fav' + (ch.favorite ? ' on' : '')} aria-label={t('tv.favorite')} onClick={() => onFav(!ch.favorite)}>
        {ch.favorite ? '★' : '☆'}
      </button>
    </div>
  );
}

function recState(r: TvRecord): string {
  if (r.state === 'scheduled') return t('tv.rec_scheduled');
  if (r.state === 'recording') return t('tv.rec_running');
  if (r.state === 'failed') return t('tv.rec_failed');
  const mb = Math.round(r.bytes / (1024 * 1024));
  return mb >= 1024 ? (mb / 1024).toFixed(1) + ' GB' : mb + ' MB';
}

function RecordRow({ r, onTap }: { r: TvRecord; onTap: () => void }) {
  return (
    <button class={'tvrow tvrow-record' + (r.state === 'recording' ? ' is-live' : '')} onClick={onTap}>
      <div class="tvrow-text">
        <div class="tvrow-title">{r.title || r.channel_title}</div>
        <div class="tvrow-sub">{[r.channel_title, ddmm(r.start_at) + ' ' + hhmm(r.start_at), recState(r)].filter(Boolean).join(' · ')}</div>
      </div>
    </button>
  );
}

export function Tv({ query }: { query: URLSearchParams }) {
  const s = useStore();
  const [tab, setTab] = useState<Tab>((query.get('tab') as Tab) || 'fav');
  const [q, setQ] = useState(query.get('q') || '');
  const [items, setItems] = useState<TvChannel[] | null>(null);
  const [records, setRecords] = useState<TvRecord[] | null>(null);
  const [now, setNow] = useState<Record<string, TvNowNext>>({});
  const [state, setState] = useState<'idle' | 'loading' | 'error' | 'off'>('idle');
  const input = useRef<HTMLInputElement>(null);

  // channel|program_end of every scheduled/running recording → ⏺ lit.
  const pending: Record<string, string> = {};
  for (const r of records || []) if ((r.state === 'scheduled' || r.state === 'recording') && r.program_end) pending[r.channel_id + '|' + r.program_end] = r.id;

  function loadRecords(): void {
    getTvRecords()
      .then((r) => setRecords(r.records || []))
      .catch((e) => {
        setRecords([]);
        if (tab === 'records') setState(e && e.code === 'dvr_disabled' ? 'off' : 'error');
      });
  }

  useEffect(() => {
    let alive = true;
    setItems(null);
    if (tab === 'records') {
      setState('loading');
      getTvRecords()
        .then((r) => {
          if (!alive) return;
          setRecords(r.records || []);
          setState('idle');
        })
        .catch((e) => alive && setState(e && e.code === 'dvr_disabled' ? 'off' : 'error'));
      return () => {
        alive = false;
      };
    }
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

  // One guide fetch covers every row on screen; the recordings light the ⏺.
  useEffect(() => {
    let alive = true;
    getTvNow()
      .then((r) => alive && setNow(r.items || {}))
      .catch(() => {});
    if (records === null) loadRecords();
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

  // ⏺ on a channel: record what is on now; the server toggles.
  function toggleRecord(ch: TvChannel): void {
    const p = now[ch.id]?.now;
    if (!p) return;
    haptic('select');
    addTvRecord({ channel_id: ch.id, channel_title: ch.name, title: p.title || ch.name, start_at: p.start, end_at: p.stop })
      .then((r) => {
        toast(t(r.cancelled ? 'tv.rec_cancelled' : 'tv.rec_added'));
        loadRecords();
      })
      .catch((e) => toast(t(e && e.code === 'dvr_disabled' ? 'tv.rec_disabled' : 'common.error')));
  }

  // A recording row: scheduled → cancel, anything else → delete (confirmed).
  function tapRecord(r: TvRecord): void {
    const scheduled = r.state === 'scheduled';
    confirmDialog(t(scheduled ? 'tv.rec_cancel_confirm' : 'tv.rec_delete_confirm', { title: r.title || r.channel_title })).then((ok) => {
      if (!ok) return;
      deleteTvRecord(r.id)
        .then(() => {
          setRecords((list) => (list || []).filter((x) => x.id !== r.id));
          toast(t(scheduled ? 'tv.rec_cancelled' : 'tv.rec_deleted'));
        })
        .catch(() => toast(t('common.error')));
    });
  }

  const body = (() => {
    if (state === 'off') return <Empty icon="⏺" title={t('tv.rec_disabled')} />;
    if (state === 'error') return <Empty icon="⚠️" title={t('common.error')} />;
    if (tab === 'records') {
      if (state === 'loading' && records === null) return <div class="tvlist">{[0, 1, 2].map((i) => <Skeleton key={i} h="48px" />)}</div>;
      if (!records || !records.length) return <Empty icon="⏺" title={t('tv.no_records')} hint={t('tv.rec_hint')} />;
      return (
        <div class="tvlist">
          {records.map((r) => (
            <RecordRow key={r.id} r={r} onTap={() => tapRecord(r)} />
          ))}
        </div>
      );
    }
    if (state === 'loading' && !items)
      return (
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
      );
    if (!items || !items.length)
      return <Empty icon="📺" title={t(tab === 'fav' ? 'tv.no_favorites' : tab === 'recent' ? 'tv.no_recent' : 'tv.nothing_found')} hint={tab === 'fav' ? t('tv.fav_hint') : undefined} />;
    return (
      <div class="tvlist">
        {items.map((ch) => {
          const p = now[ch.id]?.now;
          return (
            <Row
              key={ch.id}
              ch={ch}
              nn={now[ch.id]}
              recording={!!(p && pending[ch.id + '|' + p.stop])}
              onFav={(next) => toggleFav(ch, next)}
              onRec={() => toggleRecord(ch)}
            />
          );
        })}
      </div>
    );
  })();

  return (
    <div class="screen">
      <Segmented
        value={tab}
        options={[
          { value: 'fav', label: t('tv.favorites') },
          { value: 'recent', label: t('tv.recent') },
          { value: 'search', label: t('tv.search') },
          { value: 'records', label: t('tv.records') },
        ]}
        onChange={setTab}
      />
      {tab === 'search' && (
        <input ref={input} class="input" type="search" value={q} placeholder={t('tv.search_hint')} onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
      )}
      {body}
    </div>
  );
}
