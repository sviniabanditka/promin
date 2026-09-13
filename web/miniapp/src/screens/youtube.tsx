// YouTube from the phone: what the account is in the middle of, new videos
// from subscriptions, and a search — each row opens the video page on the TV
// (open_yt over the sync socket, docs/youtube.md). The account itself is
// linked on the TV (device code); this screen only says so when it is not.

import { useEffect, useRef, useState } from 'preact/hooks';
import { ApiError, ytBrowse, ytSearch, type YtItem } from '../api';
import { t } from '../i18n';
import { sendOpenYt, useStore } from '../store';
import { Empty, Segmented, Skeleton } from '../ui';

type Tab = 'continue' | 'subs' | 'search';

function Row({ item }: { item: YtItem }) {
  return (
    <button class="ytrow" onClick={() => sendOpenYt(item.id)}>
      <div class="ytrow-thumb">
        {item.thumbnail ? <img src={item.thumbnail} alt="" loading="lazy" /> : null}
        {item.live ? <span class="ytrow-badge ytrow-live">LIVE</span> : item.duration_text ? <span class="ytrow-badge">{item.duration_text}</span> : null}
        {item.progress_pct > 0 ? (
          <span class="ytrow-progress">
            <span style={{ width: Math.min(100, item.progress_pct) + '%' }} />
          </span>
        ) : null}
      </div>
      <div class="ytrow-text">
        <div class="ytrow-title">{item.title}</div>
        <div class="ytrow-sub">{[item.channel?.name, item.meta?.[0]].filter(Boolean).join(' · ')}</div>
      </div>
    </button>
  );
}

function List({ items, state, emptyText }: { items: YtItem[] | null; state: 'idle' | 'loading' | 'error' | 'not_linked'; emptyText: string }) {
  if (state === 'not_linked') return <Empty icon="📺" title={t('yt.not_linked')} hint={t('yt.not_linked_hint')} />;
  if (state === 'error') return <Empty icon="⚠️" title={t('common.error')} />;
  if (state === 'loading' && !items)
    return (
      <div class="ytlist">
        {[0, 1, 2, 3, 4].map((i) => (
          <div key={i} class="ytrow">
            <Skeleton cls="ytrow-thumb" />
            <div class="ytrow-text">
              <Skeleton w="90%" h="14px" />
              <Skeleton w="50%" h="12px" />
            </div>
          </div>
        ))}
      </div>
    );
  if (!items || !items.length) return <Empty icon="▶" title={emptyText} />;
  return (
    <div class="ytlist">
      {items.map((it) => (
        <Row key={it.id} item={it} />
      ))}
    </div>
  );
}

function videosOf(feed: { shelves: { items: YtItem[] }[] } | null, filter?: (it: YtItem) => boolean): YtItem[] {
  const out: YtItem[] = [];
  const seen: Record<string, true> = {};
  for (const sh of feed?.shelves || []) {
    for (const it of sh.items) {
      if (it.kind !== 'video' || seen[it.id] || (filter && !filter(it))) continue;
      seen[it.id] = true;
      out.push(it);
    }
  }
  return out;
}

export function YouTube({ query }: { query: URLSearchParams }) {
  const s = useStore();
  const [tab, setTab] = useState<Tab>((query.get('tab') as Tab) || 'continue');
  const [q, setQ] = useState(query.get('q') || '');
  const [items, setItems] = useState<YtItem[] | null>(null);
  const [state, setState] = useState<'idle' | 'loading' | 'error' | 'not_linked'>('idle');
  const input = useRef<HTMLInputElement>(null);

  const fail = (e: unknown) => setState(e instanceof ApiError && e.code === 'not_linked' ? 'not_linked' : 'error');

  useEffect(() => {
    setItems(null);
    if (tab === 'search') {
      const term = q.trim();
      if (term.length < 2) {
        setState('idle');
        return;
      }
      setState('loading');
      const id = setTimeout(() => {
        ytSearch(term)
          .then((r) => {
            setItems(videosOf(r));
            setState('idle');
          })
          .catch(fail);
      }, 350);
      return () => clearTimeout(id);
    }
    setState('loading');
    const page = tab === 'continue' ? 'history' : 'subscriptions';
    ytBrowse(page)
      .then((r) => {
        setItems(tab === 'continue' ? videosOf(r, (it) => it.progress_pct >= 3 && it.progress_pct <= 95) : videosOf(r));
        setState('idle');
      })
      .catch(fail);
    return undefined;
  }, [tab, q, s.lang]);

  useEffect(() => {
    if (tab === 'search') input.current?.focus();
  }, [tab]);

  return (
    <div class="screen">
      <Segmented
        value={tab}
        options={[
          { value: 'continue', label: t('yt.continue') },
          { value: 'subs', label: t('yt.subs') },
          { value: 'search', label: t('yt.search') },
        ]}
        onChange={setTab}
      />
      {tab === 'search' ? (
        <div class="searchbar searchbar-live">
          <span>🔍</span>
          <input
            ref={input}
            type="search"
            value={q}
            placeholder={t('yt.search_placeholder')}
            enterKeyHint="search"
            autocomplete="off"
            onInput={(e) => setQ(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') input.current?.blur();
            }}
          />
          {q && (
            <button class="clear" aria-label="clear" onClick={() => setQ('')}>
              ✕
            </button>
          )}
        </div>
      ) : null}
      <List items={items} state={state} emptyText={tab === 'search' ? (q.trim().length < 2 ? t('yt.search_hint') : t('search.empty')) : t('yt.empty')} />
    </div>
  );
}
