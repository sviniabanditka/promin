// Typed facade over window.Telegram.WebApp (loaded by index.html before the bundle).

export interface TgUser {
  id: number;
  first_name: string;
  last_name?: string;
  username?: string;
  language_code?: string;
}

interface TgButton {
  isVisible: boolean;
  show(): void;
  hide(): void;
  onClick(fn: () => void): void;
  offClick(fn: () => void): void;
}

interface TgMainButton extends TgButton {
  setText(text: string): void;
  setParams(p: { text?: string; is_active?: boolean; is_visible?: boolean; color?: string; text_color?: string }): void;
  showProgress(leaveActive?: boolean): void;
  hideProgress(): void;
  enable(): void;
  disable(): void;
}

export interface TgWebApp {
  initData: string;
  initDataUnsafe: { user?: TgUser };
  themeParams: Record<string, string>;
  colorScheme: 'light' | 'dark';
  platform: string;
  version: string;
  ready(): void;
  expand(): void;
  close(): void;
  BackButton: TgButton;
  MainButton: TgMainButton;
  HapticFeedback?: {
    impactOccurred(style: 'light' | 'medium' | 'heavy' | 'rigid' | 'soft'): void;
    notificationOccurred(type: 'error' | 'success' | 'warning'): void;
    selectionChanged(): void;
  };
  onEvent(event: string, fn: () => void): void;
  offEvent(event: string, fn: () => void): void;
  showConfirm?(message: string, cb: (ok: boolean) => void): void;
  openTelegramLink?(url: string): void;
  setHeaderColor?(color: string): void;
  setBackgroundColor?(color: string): void;
}

declare global {
  interface Window {
    Telegram?: { WebApp?: TgWebApp };
  }
}

export const tg: TgWebApp | null = window.Telegram?.WebApp ?? null;
// telegram-web-app.js defines WebApp even in a plain browser; initData is empty there.
export const inTelegram = !!(tg && tg.initData);

export function applyTheme(): void {
  if (!tg) return;
  const root = document.documentElement;
  for (const [k, v] of Object.entries(tg.themeParams || {})) {
    root.style.setProperty('--tg-theme-' + k.replace(/_/g, '-'), v);
  }
  root.dataset.theme = tg.colorScheme;
  const bg = tg.themeParams?.bg_color;
  if (bg) {
    try {
      tg.setHeaderColor?.(bg);
      tg.setBackgroundColor?.(bg);
    } catch {
      /* older clients throw on unsupported methods */
    }
  }
}

export function haptic(kind: 'tap' | 'select' | 'ok' | 'err' = 'tap'): void {
  const h = tg?.HapticFeedback;
  if (!h) return;
  try {
    if (kind === 'tap') h.impactOccurred('light');
    else if (kind === 'select') h.selectionChanged();
    else h.notificationOccurred(kind === 'ok' ? 'success' : 'error');
  } catch {
    /* unsupported version */
  }
}

export function confirmDialog(message: string): Promise<boolean> {
  if (tg?.showConfirm) {
    return new Promise((resolve) => {
      try {
        tg.showConfirm!(message, resolve);
      } catch {
        resolve(window.confirm(message));
      }
    });
  }
  return Promise.resolve(window.confirm(message));
}


// Open a t.me link natively (share sheet, bot deep link); falls back to a tab.
export function openTelegramLink(url: string): void {
  if (tg && tg.openTelegramLink) tg.openTelegramLink(url);
  else window.open(url, '_blank');
}
