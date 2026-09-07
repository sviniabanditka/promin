import { useState } from 'preact/hooks';
import { getPlaylistItems, type PlaylistItem, type TimecodeItem } from '../api';
import { fmtDate, seLabel, t } from '../i18n';
import { loadBootstrap, useStore } from '../store';
import { haptic } from '../tg';
import { cardKey, Empty, MediaRow, Segmented, SkeletonRow, useCards } from '../ui';
import { Queue } from './queue';

type Tab = 'bookmarks' | 'playlists' | 'history' | 'queue';

// Watch history = the resume positions the TV saves (the separate `history`
// table is never written). Latest row per title, newest first.
function historyFromTimecodes(tc: TimecodeItem[]): TimecodeItem[] {
  const latest: Record<string, TimecodeItem> = {};
  for (const x of tc) {
    const k = x.media_type + ':' + x.tmdb_id;
    if (!latest[k] || latest[k].updated_at < x.updated_at) latest[k] = x;
  }
  return Object.keys(latest)
    .map((k) => latest[k])
    .sort((a, b) => b.updated_at - a.updated_at);
}
const LIMIT = 40; // ponytail: no paging; add "load more" if libraries outgrow it

function Rows({ items, lang, subtitle }: { items: { tmdb_id: number; media_type: 'movie' | 'tv' }[]; lang: string; subtitle?: (i: any) => string | undefined }) {
  const cards = useCards(items, lang);
  if (!items.length) return <Empty icon="📚" title={t('lib.empty')} />;
  return (
    <div class="list">
      {items.map((i) => (
        <MediaRow key={cardKey(i)} card={cards[cardKey(i)]} subtitle={subtitle?.(i)} />
      ))}
    </div>
  );
}

function Playlists({ lang }: { lang: string }) {
  const s = useStore();
  const [open, setOpen] = useState<number | null>(null);
  const [items, setItems] = useState<Record<number, PlaylistItem[] | null | undefined>>({});
  const pls = s.playlists ?? [];
  if (!pls.length) return <Empty icon="📚" title={t('lib.empty')} />;
  const toggle = (id: number) => {
    haptic('select');
    if (open === id) return setOpen(null);
    setOpen(id);
    if (items[id] === undefined) {
      setItems((m) => ({ ...m, [id]: undefined }));
      getPlaylistItems(id)
        .then((r) => setItems((m) => ({ ...m, [id]: r.items })))
        .catch(() => setItems((m) => ({ ...m, [id]: null })));
    }
  };
  return (
    <div class="list">
      {pls.map((p) => (
        <div key={p.id} class="pl">
          <button class={'pl-head' + (open === p.id ? ' on' : '')} onClick={() => toggle(p.id)}>
            <span class="pl-name">{p.name}</span>
            <span class="pl-count">{t('lib.items', { n: p.items_count })}</span>
            <span class="pl-arrow">›</span>
          </button>
          {open === p.id &&
            (items[p.id] === undefined ? (
              <>
                <SkeletonRow />
                <SkeletonRow />
              </>
            ) : items[p.id] === null ? (
              <Empty icon="⚠️" title={t('common.error')} />
            ) : (
              <Rows items={items[p.id]!.slice(0, LIMIT)} lang={lang} />
            ))}
        </div>
      ))}
    </div>
  );
}

export function Library() {
  const s = useStore();
  const [tab, setTab] = useState<Tab>('bookmarks');
  const loaded = s.bookmarks && s.history && s.playlists;
  return (
    <div class="screen">
      <div class="sub-head">
        <Segmented
          value={tab}
          onChange={setTab}
          options={[
            { value: 'bookmarks', label: t('lib.bookmarks') },
            { value: 'playlists', label: t('lib.playlists') },
            { value: 'history', label: t('lib.history') },
            { value: 'queue', label: t('lib.queue') + (s.queue?.length ? ' ' + s.queue.length : '') },
          ]}
        />
      </div>
      {!loaded ? (
        <Empty icon="⚠️" title={t('common.error')}>
          <button class="btn" onClick={() => loadBootstrap()}>
            {t('common.retry')}
          </button>
        </Empty>
      ) : tab === 'bookmarks' ? (
        <Rows items={s.bookmarks!.slice(0, LIMIT)} lang={s.lang} />
      ) : tab === 'playlists' ? (
        <Playlists lang={s.lang} />
      ) : tab === 'queue' ? (
        <Queue />
      ) : (
        <Rows
          items={historyFromTimecodes(s.timecodes ?? []).slice(0, LIMIT)}
          lang={s.lang}
          subtitle={(h) => {
            const pct = h.duration_sec > 0 ? h.position_sec / h.duration_sec : 0;
            const progress = pct >= 0.9 ? t('lib.watched') : pct > 0 ? Math.round(pct * 100) + '%' : '';
            return [seLabel(h.season, h.episode), progress, fmtDate(h.updated_at)].filter(Boolean).join(' · ');
          }}
        />
      )}
    </div>
  );
}
