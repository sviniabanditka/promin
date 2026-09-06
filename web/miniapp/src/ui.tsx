import type { ComponentChildren } from 'preact';
import { useEffect, useState } from 'preact/hooks';
import { getTitleCached, img, type Card, type MediaType } from './api';
import { t } from './i18n';
import { navigate, titlePath } from './router';
import { haptic } from './tg';

export function Poster({ path, size = 'w342', cls = '', alt = '' }: { path?: string | null; size?: string; cls?: string; alt?: string }) {
  return (
    <div class={'poster ' + cls}>
      {path ? <img src={img(path, size)} alt={alt} loading="lazy" decoding="async" /> : <span class="poster-ph">🎬</span>}
    </div>
  );
}

export function Progress({ value, cls = '' }: { value: number; cls?: string }) {
  const pct = Math.max(0, Math.min(1, value || 0)) * 100;
  return (
    <div class={'prog ' + cls}>
      <div class="prog-fill" style={{ width: pct + '%' }} />
    </div>
  );
}

export function PosterCard({ card }: { card: Card }) {
  const tc = card.timecode;
  return (
    <button class="pcard" onClick={() => navigate(titlePath(card.type, card.tmdb_id))}>
      <Poster path={card.poster} alt={card.title} />
      {tc && tc.duration_sec > 0 && <Progress value={tc.position_sec / tc.duration_sec} cls="pcard-prog" />}
      <div class="pcard-title">{card.title}</div>
      <div class="pcard-sub">{card.year || ''}</div>
    </button>
  );
}

export function Rail({ title, children }: { title: string; children: ComponentChildren }) {
  return (
    <section class="rail">
      <h2 class="rail-title">{title}</h2>
      <div class="rail-scroll">{children}</div>
    </section>
  );
}

export function MediaRow({
  card,
  subtitle,
  right,
  onClick,
}: {
  card: Card | null | undefined;
  subtitle?: string;
  right?: ComponentChildren;
  onClick?: () => void;
}) {
  if (card === undefined) return <SkeletonRow />;
  const go = onClick ?? (card ? () => navigate(titlePath(card.type, card.tmdb_id)) : undefined);
  return (
    <div class="mrow">
      <button class="mrow-main" onClick={go} disabled={!go}>
        <Poster path={card?.poster} size="w185" cls="poster-sm" />
        <div class="mrow-text">
          <div class="mrow-title">{card ? card.title : '—'}</div>
          <div class="mrow-sub">{subtitle ?? (card ? [card.year, card.type === 'tv' ? t('common.tv') : t('common.movie')].filter(Boolean).join(' · ') : '')}</div>
        </div>
      </button>
      {right && <div class="mrow-right">{right}</div>}
    </div>
  );
}

export function Skeleton({ w, h, cls = '' }: { w?: string; h?: string; cls?: string }) {
  return <div class={'sk ' + cls} style={{ width: w, height: h }} />;
}

export function SkeletonRail() {
  return (
    <section class="rail">
      <Skeleton w="40%" h="20px" cls="rail-title" />
      <div class="rail-scroll">
        {[0, 1, 2, 3].map((i) => (
          <div key={i} class="pcard">
            <Skeleton cls="poster" />
            <Skeleton w="80%" h="14px" />
          </div>
        ))}
      </div>
    </section>
  );
}

export function SkeletonRow() {
  return (
    <div class="mrow">
      <div class="mrow-main">
        <Skeleton cls="poster poster-sm" />
        <div class="mrow-text">
          <Skeleton w="70%" h="16px" />
          <Skeleton w="40%" h="12px" />
        </div>
      </div>
    </div>
  );
}

export function Empty({ icon, title, hint, children }: { icon: string; title: string; hint?: string; children?: ComponentChildren }) {
  return (
    <div class="empty">
      <div class="empty-icon">{icon}</div>
      <div class="empty-title">{title}</div>
      {hint && <div class="empty-hint">{hint}</div>}
      {children}
    </div>
  );
}

export function Segmented<T extends string>({ value, options, onChange }: { value: T; options: { value: T; label: string }[]; onChange: (v: T) => void }) {
  return (
    <div class="seg" role="tablist">
      {options.map((o) => (
        <button
          key={o.value}
          role="tab"
          class={'seg-item' + (o.value === value ? ' on' : '')}
          aria-selected={o.value === value}
          onClick={() => {
            haptic('select');
            onChange(o.value);
          }}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Sheet({ open, title, onClose, children }: { open: boolean; title: string; onClose: () => void; children: ComponentChildren }) {
  if (!open) return null;
  return (
    <div class="sheet-back" onClick={onClose}>
      <div class="sheet" onClick={(e) => e.stopPropagation()}>
        <div class="sheet-title">{title}</div>
        {children}
        <button class="btn btn-ghost" onClick={onClose}>
          {t('common.cancel')}
        </button>
      </div>
    </div>
  );
}

// Ticks while `active`; returns Date.now() for live progress interpolation.
export function useNow(active: boolean, ms = 250): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(id);
  }, [active, ms]);
  return active ? now : Date.now();
}

export const cardKey = (x: { tmdb_id: number; media_type: string }) => x.media_type + ':' + x.tmdb_id;

// Enrich bare {tmdb_id, media_type} rows with cards. undefined = loading, null = failed.
export function useCards(items: { tmdb_id: number; media_type: MediaType }[] | null | undefined, lang: string): Record<string, Card | null | undefined> {
  const [cards, setCards] = useState<Record<string, Card | null | undefined>>({});
  const keys = (items ?? []).map(cardKey).join(',');
  useEffect(() => {
    let alive = true;
    setCards({});
    for (const it of items ?? []) {
      getTitleCached(it.media_type, it.tmdb_id)
        .then((c) => alive && setCards((m) => ({ ...m, [cardKey(it)]: c })))
        .catch(() => alive && setCards((m) => ({ ...m, [cardKey(it)]: null })));
    }
    return () => {
      alive = false;
    };
  }, [keys, lang]);
  return cards;
}
