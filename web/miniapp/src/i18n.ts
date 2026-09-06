import { getState } from './store';

export type Lang = 'uk' | 'ru' | 'en';
export const LANGS: Lang[] = ['uk', 'ru', 'en'];

export function isLang(v: unknown): v is Lang {
  return v === 'uk' || v === 'ru' || v === 'en';
}

// Telegram language_code -> UI language until the synced setting arrives.
export function pickLang(code: string | undefined): Lang {
  const c = (code || '').toLowerCase().slice(0, 2);
  if (c === 'uk') return 'uk';
  if (c === 'ru' || c === 'be' || c === 'kk') return 'ru';
  return 'en';
}

type Entry = [uk: string, ru: string, en: string];

const D: Record<string, Entry> = {
  'tab.home': ['Головна', 'Главная', 'Home'],
  'tab.search': ['Пошук', 'Поиск', 'Search'],
  'tab.remote': ['Пульт', 'Пульт', 'Remote'],
  'tab.library': ['Бібліотека', 'Библиотека', 'Library'],
  'tab.settings': ['Налаштування', 'Настройки', 'Settings'],

  'gate.not_tg_title': ['Відкрийте в Telegram', 'Откройте в Telegram', 'Open this in Telegram'],
  'gate.not_tg_body': [
    'Це міні-застосунок Promin. Він працює лише всередині Telegram — відкрийте його через кнопку меню в чаті з ботом.',
    'Это мини-приложение Promin. Оно работает только внутри Telegram — откройте его через кнопку меню в чате с ботом.',
    'This is the Promin Mini App. It only runs inside Telegram — open it from the menu button in the bot chat.',
  ],
  'gate.not_linked_title': ['Telegram не прив’язаний', 'Telegram не привязан', 'Telegram is not linked'],
  'gate.not_linked_body': [
    'Цей чат ще не прив’язаний до профілю Promin. На телевізорі відкрийте Налаштування → Telegram-бот і введіть код у чаті з ботом.',
    'Этот чат ещё не привязан к профилю Promin. На телевизоре откройте Настройки → Telegram-бот и введите код в чате с ботом.',
    'This chat is not linked to a Promin profile yet. On the TV open Settings → Telegram bot and enter the code in the bot chat.',
  ],
  'gate.error_title': ['Не вдалося увійти', 'Не удалось войти', 'Sign-in failed'],
  'gate.retry': ['Спробувати ще', 'Попробовать ещё', 'Retry'],
  'gate.loading': ['Підключення…', 'Подключение…', 'Connecting…'],

  'home.now_playing': ['Зараз грає', 'Сейчас играет', 'Now playing'],
  'home.search_placeholder': ['Фільми, серіали…', 'Фильмы, сериалы…', 'Movies, shows…'],
  'home.empty': ['Каталог порожній', 'Каталог пуст', 'Nothing here yet'],

  'search.empty': ['Нічого не знайдено', 'Ничего не найдено', 'Nothing found'],
  'search.hint': ['Введіть назву фільму або серіалу', 'Введите название фильма или сериала', 'Type a movie or show title'],

  'title.watch': ['▶ Дивитися', '▶ Смотреть', '▶ Watch'],
  'title.continue': ['▶ Продовжити', '▶ Продолжить', '▶ Continue'],
  'title.on_tv': ['▶ На ТБ', '▶ На ТВ', '▶ On TV'],
  'title.no_episodes': ['Епізодів немає', 'Эпизодов нет', 'No episodes'],
  'title.bookmark': ['У закладки', 'В закладки', 'Bookmark'],
  'title.bookmarked': ['У закладках', 'В закладках', 'Bookmarked'],
  'title.not_found': ['Тайтл не знайдено', 'Тайтл не найден', 'Title not found'],
  'title.sent': ['Надіслано на {name}', 'Отправлено на {name}', 'Sent to {name}'],

  'remote.nothing': ['Нічого не грає', 'Ничего не играет', 'Nothing is playing'],
  'remote.nothing_hint': [
    'Запустіть щось на телевізорі або оберіть тайтл нижче',
    'Запустите что-нибудь на телевизоре или выберите тайтл ниже',
    'Start something on the TV or pick a title below',
  ],
  'remote.continue': ['Продовжити перегляд', 'Продолжить просмотр', 'Continue watching'],
  'remote.sleep': ['Таймер сну', 'Таймер сна', 'Sleep timer'],
  'remote.sleep_set': ['Таймер сну: {n} хв', 'Таймер сна: {n} мин', 'Sleep timer: {n} min'],
  'remote.min': ['хв', 'мин', 'min'],
  'remote.no_device': ['Немає телевізора онлайн', 'Нет телевизора онлайн', 'No TV is online'],
  'remote.no_device_hint': [
    'Відкрийте Promin на телевізорі — він з’явиться тут автоматично',
    'Откройте Promin на телевизоре — он появится здесь автоматически',
    'Open Promin on the TV — it will show up here automatically',
  ],
  'remote.offline': ['Телевізор офлайн', 'Телевизор офлайн', 'The TV is offline'],
  'remote.send_failed': ['Не вдалося надіслати', 'Не удалось отправить', 'Could not send'],

  'device.pick': ['Оберіть телевізор', 'Выберите телевизор', 'Choose a TV'],
  'device.none': ['Немає ТБ', 'Нет ТВ', 'No TV'],
  'device.online': ['онлайн', 'онлайн', 'online'],
  'device.offline': ['офлайн', 'офлайн', 'offline'],

  'lib.bookmarks': ['Закладки', 'Закладки', 'Bookmarks'],
  'lib.playlists': ['Плейлисти', 'Плейлисты', 'Playlists'],
  'lib.history': ['Історія', 'История', 'History'],
  'lib.watched': ['Переглянуто', 'Просмотрено', 'Watched'],
  'lib.empty': ['Тут поки порожньо', 'Здесь пока пусто', 'Nothing here yet'],
  'lib.items': ['{n} тайтлів', '{n} тайтлов', '{n} titles'],

  'set.language': ['Мова', 'Язык', 'Language'],
  'set.devices': ['Пристрої', 'Устройства', 'Devices'],
  'set.this_device': ['цей пристрій', 'это устройство', 'this device'],
  'set.revoke': ['Вийти', 'Выйти', 'Sign out'],
  'set.revoke_confirm': ['Завершити сеанс «{name}»?', 'Завершить сеанс «{name}»?', 'Sign out “{name}”?'],
  'set.revoke_others': ['Вийти на всіх інших пристроях', 'Выйти на всех других устройствах', 'Sign out other devices'],
  'set.revoke_others_confirm': ['Завершити всі сеанси, крім цього телефона? ТВ попросять PIN знову.', 'Завершить все сеансы, кроме этого телефона? ТВ снова спросят PIN.', 'Sign out every device except this phone? TVs will ask for the PIN again.'],
  'set.revoke_others_done': ['Завершено сеансів: {n}', 'Завершено сеансов: {n}', 'Signed out: {n}'],
  'set.telegram': ['Telegram', 'Telegram', 'Telegram'],
  'set.unlink': ['Відв’язати Telegram', 'Отвязать Telegram', 'Unlink Telegram'],
  'set.unlink_confirm': [
    'Відв’язати цей Telegram від профілю? Міні-застосунок і бот перестануть працювати до повторної прив’язки.',
    'Отвязать этот Telegram от профиля? Мини-приложение и бот перестанут работать до повторной привязки.',
    'Unlink this Telegram from the profile? The Mini App and the bot stop working until you link again.',
  ],
  'set.signed_in_as': ['Профіль: {login}', 'Профиль: {login}', 'Profile: {login}'],

  'common.error': ['Щось пішло не так', 'Что-то пошло не так', 'Something went wrong'],
  'common.retry': ['Повторити', 'Повторить', 'Retry'],
  'common.cancel': ['Скасувати', 'Отмена', 'Cancel'],
  'common.movie': ['Фільм', 'Фильм', 'Movie'],
  'common.tv': ['Серіал', 'Сериал', 'Series'],
  'common.min': ['хв', 'мин', 'min'],
  'common.season': ['Сезон', 'Сезон', 'Season'],
};

const IDX: Record<Lang, number> = { uk: 0, ru: 1, en: 2 };

export function t(key: string, vars?: Record<string, string | number>): string {
  const e = D[key];
  let s = e ? e[IDX[getState().lang]] : key;
  if (vars) for (const k in vars) s = s.split('{' + k + '}').join(String(vars[k]));
  return s;
}

export function fmtTime(sec: number): string {
  sec = Math.max(0, Math.floor(sec || 0));
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  const mm = h ? String(m).padStart(2, '0') : String(m);
  return (h ? h + ':' : '') + mm + ':' + String(s).padStart(2, '0');
}

export function fmtDate(unix: number): string {
  const l = getState().lang;
  return new Date(unix * 1000).toLocaleDateString(l === 'uk' ? 'uk-UA' : l === 'ru' ? 'ru-RU' : 'en-GB', {
    day: 'numeric',
    month: 'short',
  });
}

// "S2 E5" style label shared by cards, remote and history rows.
export function seLabel(season?: number | null, episode?: number | null): string {
  if (season == null && episode == null) return '';
  return (season != null ? 'S' + season : '') + (episode != null ? ' E' + episode : '');
}
