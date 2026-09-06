import { useEffect, useState } from 'preact/hooks';
import { getAuthDevices, putSetting, revokeDevice, unlinkTelegram, type AuthDevice } from '../api';
import { fmtDate, LANGS, t, type Lang } from '../i18n';
import { setLang, setState, toast, useStore } from '../store';
import { confirmDialog, haptic } from '../tg';
import { Segmented, SkeletonRow } from '../ui';

const LANG_LABEL: Record<Lang, string> = { uk: 'Українська', ru: 'Русский', en: 'English' };
const TYPE_ICON: Record<string, string> = { telegram: '💬', tv: '📺', tizen: '📺', webos: '📺', android: '📺', browser: '🖥' };

export function Settings() {
  const s = useStore();
  const [devices, setDevices] = useState<AuthDevice[] | null | undefined>(undefined);

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
    } catch {
      setLang(prev);
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

  const unlink = async () => {
    if (!(await confirmDialog(t('set.unlink_confirm')))) return;
    try {
      await unlinkTelegram();
      setState({ phase: 'not_linked' });
    } catch {
      toast(t('common.error'));
    }
  };

  return (
    <div class="screen settings">
      <section class="group">
        <h2 class="group-title">{t('set.language')}</h2>
        <Segmented value={s.lang} onChange={changeLang} options={LANGS.map((l) => ({ value: l, label: LANG_LABEL[l] }))} />
      </section>

      <section class="group">
        <h2 class="group-title">{t('set.devices')}</h2>
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
        <button class="btn btn-danger" onClick={unlink}>
          {t('set.unlink')}
        </button>
      </section>
    </div>
  );
}
