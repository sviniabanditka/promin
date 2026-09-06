import { render } from 'preact';
import { useEffect, useState } from 'preact/hooks';
import * as api from './api';
import { pickLang, t } from './i18n';
import { back, navigate, TABS, useRoute, type Screen } from './router';
import { Home } from './screens/home';
import { Library } from './screens/library';
import { Remote } from './screens/remote';
import { Search } from './screens/search';
import { Settings } from './screens/settings';
import { Title } from './screens/title';
import { chooseDevice, getState, loadBootstrap, loadHiddenIds, refreshDevices, setLang, setState, targetDevice, useStore, visibleDevices } from './store';
import { applyTheme, haptic, inTelegram, tg } from './tg';
import { Empty, Sheet } from './ui';
import { startWs, stopWs } from './ws';

const TAB_ICON: Record<Screen, string> = { home: '🏠', search: '🔍', remote: '🎛', library: '📚', settings: '⚙️', title: '' };

// ---- boot ---------------------------------------------------------------------

let reauthTried = false;

async function boot(): Promise<void> {
  if (!inTelegram || !tg) {
    setState({ phase: 'not_tg' });
    return;
  }
  setState({ phase: 'boot', error: null });
  try {
    if (!api.getToken()) {
      const r = await api.tgAuth(tg.initData);
      api.setToken(r.token);
      setState({ user: r.user });
    }
    setState({ phase: 'ready' });
    await Promise.all([refreshDevices(), loadHiddenIds(), loadBootstrap()]);
    startWs();
  } catch (e) {
    const err = e instanceof api.ApiError ? e : null;
    if (err?.code === 'tg_not_linked') setState({ phase: 'not_linked' });
    else setState({ phase: 'error', error: err?.message || err?.code || String(e) });
  }
}

// Stored token died (401): drop it and re-auth once with fresh initData.
api.onUnauthorized(() => {
  if (reauthTried) return;
  reauthTried = true;
  stopWs();
  api.setToken(null);
  boot();
});

if (tg) {
  applyTheme();
  tg.onEvent('themeChanged', applyTheme);
  tg.expand();
  tg.ready();
  setLang(pickLang(tg.initDataUnsafe?.user?.language_code));
} else {
  setLang(pickLang(navigator.language));
}

// Presence is not pushed as an event: poll the device list while visible.
setInterval(() => {
  if (document.visibilityState === 'visible' && getState().phase === 'ready') refreshDevices();
}, 15000);

// ---- shell ------------------------------------------------------------------------

function DeviceChip() {
  const s = useStore();
  const [open, setOpen] = useState(false);
  const vis = visibleDevices(s);
  if (vis.length <= 1) return null;
  const dev = targetDevice(s);
  return (
    <>
      <button class={'chip chip-dev' + (dev?.online ? '' : ' off')} onClick={() => setOpen(true)}>
        📺 {dev ? dev.name : t('device.none')} ▾
      </button>
      <Sheet open={open} title={t('device.pick')} onClose={() => setOpen(false)}>
        {vis.map((d) => (
          <button
            key={d.id}
            class={'sheet-row' + (d.id === dev?.id ? ' on' : '')}
            onClick={() => {
              haptic('select');
              chooseDevice(d.id);
              setOpen(false);
            }}
          >
            <span class={'dot' + (d.online ? ' online' : '')} />
            <span class="sheet-row-name">{d.name}</span>
            <span class="sheet-row-sub">{d.online ? (s.states[d.id]?.title ?? t('device.online')) : t('device.offline')}</span>
          </button>
        ))}
      </Sheet>
    </>
  );
}

function App() {
  const s = useStore();
  const route = useRoute();

  useEffect(() => {
    const bb = tg?.BackButton;
    if (!bb) return;
    const onBack = () => back();
    if (route.screen === 'title') {
      bb.show();
      bb.onClick(onBack);
      return () => bb.offClick(onBack);
    }
    bb.hide();
    return undefined;
  }, [route.screen]);

  useEffect(() => {
    document.getElementById('main')?.scrollTo(0, 0);
  }, [route.screen, route.params.join('/')]);

  if (s.phase === 'not_tg') return <Gate icon="✈️" title={t('gate.not_tg_title')} body={t('gate.not_tg_body')} />;
  if (s.phase === 'not_linked') return <Gate icon="🔗" title={t('gate.not_linked_title')} body={t('gate.not_linked_body')} />;
  if (s.phase === 'error')
    return (
      <Gate icon="⚠️" title={t('gate.error_title')} body={s.error || ''}>
        <button class="btn" onClick={() => boot()}>
          {t('gate.retry')}
        </button>
      </Gate>
    );
  if (s.phase === 'boot') return <Gate icon="📡" title={t('gate.loading')} body="" />;

  let screen;
  switch (route.screen) {
    case 'search':
      screen = <Search query={route.query} />;
      break;
    case 'title': {
      const type = route.params[0] === 'tv' ? 'tv' : 'movie';
      const id = Number(route.params[1]);
      screen = id ? <Title key={type + id} type={type} id={id} /> : <Empty icon="😕" title={t('title.not_found')} />;
      break;
    }
    case 'remote':
      screen = <Remote />;
      break;
    case 'library':
      screen = <Library />;
      break;
    case 'settings':
      screen = <Settings />;
      break;
    default:
      screen = <Home />;
  }

  return (
    <div class="app">
      <header class="hdr">
        <div class="hdr-title">{route.screen === 'home' || route.screen === 'title' ? 'Promin' : t('tab.' + route.screen)}</div>
        <DeviceChip />
      </header>
      <main id="main" class="main">
        {screen}
      </main>
      <nav class="tabs">
        {TABS.map((tab) => (
          <button
            key={tab}
            class={'tab' + (route.screen === tab ? ' on' : '')}
            onClick={() => {
              haptic('select');
              navigate(tab === 'home' ? '/' : '/' + tab, true);
            }}
          >
            <span class="tab-icon">{TAB_ICON[tab]}</span>
            <span class="tab-label">{t('tab.' + tab)}</span>
          </button>
        ))}
      </nav>
      {s.toast && <div class="toast">{s.toast}</div>}
    </div>
  );
}

function Gate({ icon, title, body, children }: { icon: string; title: string; body: string; children?: preact.ComponentChildren }) {
  return (
    <div class="gate">
      <div class="empty-icon">{icon}</div>
      <h1>{title}</h1>
      <p>{body}</p>
      {children}
    </div>
  );
}

render(<App />, document.getElementById('app')!);
boot();
