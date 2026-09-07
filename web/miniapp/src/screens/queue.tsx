import { seLabel, t } from '../i18n';
import { queueClear, queueMove, queuePlayHead, queueRemove, targetDevice, useStore } from '../store';
import { haptic } from '../tg';
import { cardKey, Empty, MediaRow, useCards } from '../ui';

// Watch queue tab (Library). Rows are bare ids enriched like every other library
// row; ▲▼ reorder, ✕ remove, the head gets "▶ Play on TV". Live via queue_updated.
export function Queue() {
  const s = useStore();
  const items = s.queue ?? [];
  const cards = useCards(items, s.lang);
  const canSend = !!targetDevice(s)?.online;
  if (!items.length) return <Empty icon="⏭" title={t('queue.empty')} hint={t('queue.empty_hint')} />;
  return (
    <>
      <div class="q-bar">
        <span class="q-count">{t('queue.count', { n: items.length })}</span>
        <button class="btn btn-small" disabled={!canSend} onClick={() => queuePlayHead()}>
          {t('queue.play')}
        </button>
        <button
          class="btn btn-small btn-ghost"
          onClick={() => {
            haptic('select');
            queueClear();
          }}
        >
          {t('queue.clear')}
        </button>
      </div>
      <div class="list">
        {items.map((q, i) => (
          <MediaRow
            key={q.id}
            card={cards[cardKey(q)]}
            subtitle={[seLabel(q.season, q.episode), i === 0 ? t('queue.play').replace('▶ ', '') : ''].filter(Boolean).join(' · ') || undefined}
            right={
              <div class="q-ctl">
                <button class="q-btn" disabled={i === 0} aria-label="up" onClick={() => queueMove(q.id, i - 1)}>
                  ▲
                </button>
                <button class="q-btn" disabled={i === items.length - 1} aria-label="down" onClick={() => queueMove(q.id, i + 1)}>
                  ▼
                </button>
                <button
                  class="q-btn q-del"
                  aria-label="remove"
                  onClick={() => {
                    haptic('select');
                    queueRemove(q.id).catch(() => {});
                  }}
                >
                  ✕
                </button>
              </div>
            }
          />
        ))}
      </div>
    </>
  );
}
