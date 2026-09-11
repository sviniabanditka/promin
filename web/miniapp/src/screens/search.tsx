import { useEffect, useRef, useState } from 'preact/hooks';
import { search, type Card } from '../api';
import * as history from '../history';
import { t } from '../i18n';
import { navigate } from '../router';
import { useStore } from '../store';
import { Empty, PosterCard, Skeleton } from '../ui';

export function Search({ query }: { query: URLSearchParams }) {
  const s = useStore();
  const [q, setQ] = useState(query.get('q') || '');
  const [res, setRes] = useState<Card[] | null>(null);
  const [state, setState] = useState<'idle' | 'loading' | 'error'>('idle');
  const recent = history.parse(s.settings[history.KEY]); // synced with the TVs
  const input = useRef<HTMLInputElement>(null);
  // A query counts as "searched" when the user commits it: Enter, or opening a
  // result. Not on every debounced keystroke — that would store "ba", "bat", …
  const remember = () => {
    if (q.trim().length >= 2) history.add(q);
  };

  useEffect(() => {
    input.current?.focus();
  }, []);

  useEffect(() => {
    const term = q.trim();
    navigate('/search' + (term ? '?q=' + encodeURIComponent(term) : ''), true);
    if (term.length < 2) {
      setRes(null);
      setState('idle');
      return;
    }
    setState('loading');
    const id = setTimeout(() => {
      search(term)
        .then((r) => {
          setRes(r.items);
          setState('idle');
        })
        .catch(() => setState('error'));
    }, 350);
    return () => clearTimeout(id);
  }, [q, s.lang]);

  return (
    <div class="screen">
      <div class="searchbar searchbar-live">
        <span>🔍</span>
        <input
          ref={input}
          type="search"
          value={q}
          placeholder={t('home.search_placeholder')}
          enterKeyHint="search"
          autocomplete="off"
          onInput={(e) => setQ(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              remember();
              input.current?.blur();
            }
          }}
        />
        {q && (
          <button class="clear" aria-label="clear" onClick={() => setQ('')}>
            ✕
          </button>
        )}
      </div>
      {state === 'error' ? (
        <Empty icon="⚠️" title={t('common.error')} />
      ) : state === 'loading' && !res ? (
        <div class="grid">
          {[0, 1, 2, 3, 4, 5].map((i) => (
            <div key={i} class="pcard">
              <Skeleton cls="poster" />
              <Skeleton w="80%" h="14px" />
            </div>
          ))}
        </div>
      ) : res ? (
        res.length ? (
          <div class="grid" onClickCapture={remember}>
            {res.map((c) => (
              <PosterCard key={c.type + c.tmdb_id} card={c} />
            ))}
          </div>
        ) : (
          <Empty icon="🔍" title={t('search.empty')} />
        )
      ) : recent.length ? (
        <>
          <h2 class="rail-title">{t('search.recent')}</h2>
          <div class="chips chips-wrap">
            {recent.map((h) => (
              <button key={h} class="chip" onClick={() => setQ(h)}>
                {h}
              </button>
            ))}
            <button
              class="chip chip-clear"
              onClick={() => history.clear()}
            >
              ✕ {t('search.clear_recent')}
            </button>
          </div>
        </>
      ) : (
        <Empty icon="🔍" title={t('search.hint')} />
      )}
    </div>
  );
}
