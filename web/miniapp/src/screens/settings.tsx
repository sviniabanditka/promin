import { useEffect, useState } from 'preact/hooks';
import {
  clearHistory,
  createTelegramLink,
  deleteAllData,
  getAuthDevices,
  getTelegramLinks,
  putSetting,
  revokeDevice,
  revokeOtherDevices,
  sessionLost,
  unlinkTelegram,
  unlinkTelegramChat,
  type AuthDevice,
  type LocalKey,
  type TelegramLink,
} from '../api';
import { fmtDate, LANGS, t, type Lang } from '../i18n';
import { chooseDevice, sendLocal, setLang, setSetting, setState, targetDevice, toast, useStore, visibleDevices } from '../store';
import { confirmDialog, haptic, openTelegramLink } from '../tg';
import { Segmented, Sheet, SkeletonRow } from '../ui';

const LANG_LABEL: Record<Lang, string> = { uk: 'Українська', ru: 'Русский', en: 'English' };
const TYPE_ICON: Record<string, string> = { telegram: '💬', tv: '📺', tizen: '📺', webos: '📺', android: '📺', browser: '🖥' };

// TV value sets (docs/miniapp.md) with the TV's defaults for keys the profile has never set.
const QUALITY = ['auto', '2160', '1080', '720', '480'];
const ENGINE = ['auto', 'hlsjs', 'native'];
const SUBS = ['small', 'medium', 'large'];
const SPEED = ['0.5', '0.75', '1', '1.25', '1.5', '1.75', '2'];
const SAVER = ['0', '3', '5', '10'];
const DEFAULTS: Record<string, string> = { default_quality: 'auto', player_engine: 'auto', subtitle_size: 'medium', player_speed: '1', night_mode: 'false', night_dim: '85', screensaver_min: '5' };
const LOCAL: [LocalKey, string][] = [
  ['legacy_tv_mode', 'set.legacy'],
  ['reduce_motion', 'set.reduce_motion'],
  ['debug_mode', 'set.debug'],
];

function Row({ label, value, onClick }: { label: string; value?: string; onClick?: () => void }) {
  const inner = (
    <>
      <span class="row-label">{label}</span>
      {value !== undefined && <span class="row-val">{value}</span>}
    </>
  );
  return onClick ? (
    <button class="row row-pick" onClick={onClick}>
      {inner}
    </button>
  ) : (
    <div class="row">{inner}</div>
  );
}

function Switch({ on, disabled, onChange }: { on: boolean; disabled?: boolean; onChange: (on: boolean) => void }) {
  return <button role="switch" aria-checked={on} class="sw" disabled={disabled} onClick={() => onChange(!on)} />;
}

export function Settings() {
  const s = useStore();
  const [devices, setDevices] = useState<AuthDevice[] | null | undefined>(undefined);
  const [speedOpen, setSpeedOpen] = useState(false);
  const val = (k: string) => s.settings[k] ?? DEFAULTS[k];
  const tvs = visibleDevices(s);
  const tv = targetDevice(s);

  const load = () =>
    getAuthDevices()
      .then((r) => {
        setDevices(r.devices);
        setState({ hiddenIds: r.devices.filter((d) => d.current || d.device_type === 'telegram').map((d) => d.token_id) });
      })
      .catch(() => setDevices(null));
  useEffect(() => {
    load();
  }, []);

  const changeLang = async (l: Lang) => {
    const prev = s.lang;
    setLang(l);
    setState({ home: null });
    try {
      await putSetting('lang', l);
      // Full reload: cached cards, home rows and episode lists were fetched in
      // the old language. The token is in localStorage and Telegram keeps the
      // launch URL, so the app comes straight back in the new language.
      location.reload();
    } catch {
      setLang(prev);
      toast(t('common.error'));
    }
  };

  const revokeOthers = async () => {
    if (!(await confirmDialog(t('set.revoke_others_confirm')))) return;
    try {
      const r = await revokeOtherDevices();
      haptic('ok');
      toast(t('set.revoke_others_done', { n: String(r.revoked) }));
      load();
    } catch {
      toast(t('common.error'));
    }
  };
  const revoke = async (d: AuthDevice) => {
    if (!(await confirmDialog(t('set.revoke_confirm', { name: d.device_name })))) return;
    try {
      await revokeDevice(d.token_id);
      haptic('ok');
      load();
    } catch {
      toast(t('common.error'));
    }
  };

  const [links, setLinks] = useState<TelegramLink[] | null>(null);
  const loadLinks = () => getTelegramLinks().then((r) => setLinks(r.links), () => setLinks([]));
  useEffect(() => {
    loadLinks();
  }, []);
  const invite = async () => {
    try {
      const l = await createTelegramLink();
      haptic('ok');
      const text = t('set.invite_text');
      openTelegramLink('https://t.me/share/url?url=' + encodeURIComponent(l.deep_link) + '&text=' + encodeURIComponent(text));
    } catch {
      toast(t('common.error'));
    }
  };
  const unlinkOne = async (l: TelegramLink) => {
    const name = l.first_name || (l.username ? '@' + l.username : String(l.chat_id));
    if (!(await confirmDialog(t('set.unlink_one_confirm', { name })))) return;
    try {
      await unlinkTelegramChat(l.chat_id);
      haptic('ok');
      loadLinks();
    } catch {
      toast(t('common.error'));
    }
  };
  const unlink = async () => {
    if (!(await confirmDialog(t('set.unlink_confirm')))) return;
    try {
      await unlinkTelegram();
      setState({ phase: 'not_linked' });
    } catch {
      toast(t('common.error'));
    }
  };

  const clearHist = async () => {
    if (!(await confirmDialog(t('set.clear_history_confirm')))) return;
    try {
      await clearHistory();
      setState({ timecodes: [] });
      haptic('ok');
      toast(t('set.history_cleared'));
    } catch {
      toast(t('common.error'));
    }
  };
  const deleteData = async () => {
    if (!(await confirmDialog(t('set.delete_data_confirm')))) return;
    try {
      await deleteAllData();
      haptic('ok');
      sessionLost(); // every session is revoked: re-auth through initData
      toast(t('set.data_deleted'));
    } catch {
      toast(t('common.error'));
    }
  };

  const speedLabel = (v: string) => v + '×';
  const saverLabel = (v: string) => (v === '0' ? t('common.off') : v + ' ' + t('common.min'));

  return (
    <div class="screen settings">
      <section class="group">
        <h2 class="group-title">{t('set.language')}</h2>
        <Segmented value={s.lang} onChange={changeLang} options={LANGS.map((l) => ({ value: l, label: LANG_LABEL[l] }))} />
      </section>

      <section class="group">
        <h2 class="group-title">{t('set.playback')}</h2>
        <Row label={t('set.quality')} />
        <div class="seg-dense">
          <Segmented value={val('default_quality')} onChange={(v) => setSetting('default_quality', v)} options={QUALITY.map((q) => ({ value: q, label: q === 'auto' ? t('common.auto') : q }))} />
        </div>
        <Row label={t('set.engine')} />
        <Segmented
          value={val('player_engine')}
          onChange={(v) => setSetting('player_engine', v)}
          options={ENGINE.map((e) => ({ value: e, label: e === 'auto' ? t('common.auto') : e === 'native' ? t('set.engine_native') : 'hls.js' }))}
        />
        <Row label={t('set.subs_size')} />
        <Segmented value={val('subtitle_size')} onChange={(v) => setSetting('subtitle_size', v)} options={SUBS.map((x) => ({ value: x, label: t('set.subs_' + x) }))} />
        <Row label={t('set.speed')} value={speedLabel(val('player_speed'))} onClick={() => setSpeedOpen(true)} />
        <Sheet open={speedOpen} title={t('set.speed')} onClose={() => setSpeedOpen(false)}>
          {SPEED.map((v) => (
            <button
              key={v}
              class={'sheet-row' + (v === val('player_speed') ? ' on' : '')}
              onClick={() => {
                haptic('select');
                setSetting('player_speed', v);
                setSpeedOpen(false);
              }}
            >
              <span class="sheet-row-name">{speedLabel(v)}</span>
            </button>
          ))}
        </Sheet>
      </section>

      <section class="group">
        <h2 class="group-title">{t('set.screen')}</h2>
        <div class="row">
          <span class="row-label">{t('set.night')}</span>
          <Switch on={val('night_mode') === 'true'} onChange={(on) => { haptic('select'); setSetting('night_mode', on ? 'true' : 'false'); }} />
        </div>
        <Row label={t('set.night_dim')} value={val('night_dim') + '%'} />
        <input
          class="range"
          type="range"
          min={50}
          max={90}
          step={5}
          value={Number(val('night_dim'))}
          onChange={(e) => {
            haptic('select');
            setSetting('night_dim', String((e.currentTarget as HTMLInputElement).value));
          }}
        />
        <Row label={t('set.screensaver')} value={saverLabel(val('screensaver_min'))} />
        <Segmented value={val('screensaver_min')} onChange={(v) => setSetting('screensaver_min', v)} options={SAVER.map((v) => ({ value: v, label: saverLabel(v) }))} />
      </section>

      <section class="group">
        <h2 class="group-title">{t('set.this_tv')}</h2>
        {tvs.length === 0 ? (
          <div class="note">{t('remote.no_device')}</div>
        ) : (
          <>
            {tvs.length > 1 && (
              <div class="seg-dense" style="margin-bottom: 8px">
                <Segmented value={tv?.id ?? ''} onChange={chooseDevice} options={tvs.map((d) => ({ value: d.id, label: d.name }))} />
              </div>
            )}
            {tv && !tv.settings && <div class="note">{t('set.not_reported')}</div>}
            {LOCAL.map(([key, label]) => (
              <div key={key} class="row">
                <span class="row-label">{t(label)}</span>
                <Switch on={tv?.settings?.[key] === 'true'} disabled={!tv?.settings} onChange={(on) => sendLocal(key, on)} />
              </div>
            ))}
          </>
        )}
      </section>

      <section class="group">
        <h2 class="group-title" style={{ color: 'var(--danger)' }}>
          {t('set.danger')}
        </h2>
        <button class="btn btn-danger" onClick={clearHist}>
          {t('set.clear_history')}
        </button>
        <button class="btn btn-danger" onClick={deleteData}>
          {t('set.delete_data')}
        </button>
      </section>

      <section class="group">
        <h2 class="group-title">{t('set.devices')}</h2>
        <button class="btn btn-small btn-ghost" style="margin-bottom: 8px" onClick={revokeOthers}>
          {t('set.revoke_others')}
        </button>
        <div class="list">
          {devices === undefined ? (
            <>
              <SkeletonRow />
              <SkeletonRow />
            </>
          ) : devices === null ? (
            <button class="btn btn-ghost" onClick={load}>
              {t('common.retry')}
            </button>
          ) : (
            devices.map((d) => (
              <div key={d.token_id} class="dev">
                <span class="dev-icon">{TYPE_ICON[d.device_type] ?? '📱'}</span>
                <div class="dev-text">
                  <div class="dev-name">
                    {d.device_name || d.device_type}
                    {d.current && <span class="tag">{t('set.this_device')}</span>}
                  </div>
                  <div class="dev-sub">
                    {d.device_type} · {fmtDate(d.last_seen || d.created_at)}
                  </div>
                </div>
                {!d.current && (
                  <button class="btn btn-small btn-ghost" onClick={() => revoke(d)}>
                    {t('set.revoke')}
                  </button>
                )}
              </div>
            ))
          )}
        </div>
      </section>

      <section class="group">
        <h2 class="group-title">{t('set.telegram')}</h2>
        {s.user && <div class="note">{t('set.signed_in_as', { login: s.user.login })}</div>}
        <div class="list">
          {(links ?? []).map((l) => (
            <div class="row" key={l.chat_id}>
              <div class="row-label">
                {l.first_name || t('set.no_name')}
                {l.username ? <span class="row-val"> @{l.username}</span> : null}
              </div>
              <button class="btn btn-small btn-ghost" onClick={() => unlinkOne(l)}>
                {t('set.unlink_one')}
              </button>
            </div>
          ))}
        </div>
        <button class="btn" onClick={invite}>
          {t('set.invite_phone')}
        </button>
        <div class="note">{t('set.invite_hint')}</div>
        <button class="btn btn-danger" onClick={unlink}>
          {t('set.unlink')}
        </button>
      </section>
    </div>
  );
}
