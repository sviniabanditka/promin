// Inline SVG icons for the remote pad. Emoji glyphs differ per phone font
// (some are colour bitmaps, some are missing) and the "⏪ + 30" stack shifted
// the button content; these draw in currentColor at one size.

const P = { width: 26, height: 26, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': 2, 'stroke-linecap': 'round', 'stroke-linejoin': 'round' } as const;

export function IconPlay() {
  return (
    <svg {...P} fill="currentColor" stroke="none">
      <path d="M7 4.5v15l12-7.5z" />
    </svg>
  );
}

export function IconPause() {
  return (
    <svg {...P} fill="currentColor" stroke="none">
      <rect x="6" y="4.5" width="4" height="15" rx="1" />
      <rect x="14" y="4.5" width="4" height="15" rx="1" />
    </svg>
  );
}

export function IconPrev() {
  return (
    <svg {...P} fill="currentColor" stroke="none">
      <rect x="4" y="5" width="2.5" height="14" rx="1" />
      <path d="M19 5.5v13l-10-6.5z" />
    </svg>
  );
}

export function IconNext() {
  return (
    <svg {...P} fill="currentColor" stroke="none">
      <rect x="17.5" y="5" width="2.5" height="14" rx="1" />
      <path d="M5 5.5v13l10-6.5z" />
    </svg>
  );
}

// A circular arrow with the step size inside: ↺30 / ↻30.
function Skip({ back, n }: { back: boolean; n: number }) {
  return (
    <svg {...P}>
      {back ? (
        <>
          <path d="M4 12a8 8 0 1 0 2.3-5.7" />
          <path d="M6 3v4h4" />
        </>
      ) : (
        <>
          <path d="M20 12a8 8 0 1 1-2.3-5.7" />
          <path d="M18 3v4h-4" />
        </>
      )}
      <text x="12" y="15.5" text-anchor="middle" font-size="8" font-weight="700" fill="currentColor" stroke="none" font-family="inherit">
        {n}
      </text>
    </svg>
  );
}

export function IconBack30() {
  return <Skip back n={30} />;
}

export function IconFwd30() {
  return <Skip back={false} n={30} />;
}

export function IconMute({ on }: { on: boolean }) {
  return (
    <svg {...P}>
      <path d="M4 10v4h3l4 3.5V6.5L7 10z" fill="currentColor" stroke="none" />
      {on ? (
        <>
          <path d="M16 9.5l4 5" />
          <path d="M20 9.5l-4 5" />
        </>
      ) : (
        <>
          <path d="M15.5 9a4 4 0 0 1 0 6" />
          <path d="M18 6.5a7.5 7.5 0 0 1 0 11" />
        </>
      )}
    </svg>
  );
}
