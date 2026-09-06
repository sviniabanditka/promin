// Top bar with logo + clock. Manual date formatting (no Intl — unreliable on
// old Tizen/webOS, see docs/frontend.md/8).

import { el, pad2 } from './dom';
import { t, getLang } from '../core/i18n';

const WEEKDAYS: { [lang: string]: string[] } = {
  uk: ['Нд', 'Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб'],
  ru: ['Вс', 'Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб'],
  en: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'],
};

const MONTHS: { [lang: string]: string[] } = {
  uk: ['січ', 'лют', 'бер', 'кві', 'тра', 'чер', 'лип', 'сер', 'вер', 'жов', 'лис', 'гру'],
  ru: ['янв', 'фев', 'мар', 'апр', 'май', 'июн', 'июл', 'авг', 'сен', 'окт', 'ноя', 'дек'],
  en: ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'],
};

export interface Head {
  el: HTMLElement;
  destroy(): void;
}

export function buildHead(): Head {
  const head = el('div', 'head');
  head.appendChild(el('div', 'head__logo', t('home.title')));

  const time = el('div', 'head__time');
  const timeNow = el('div', 'head__time-now', '--:--');
  const timeDate = el('div', 'head__time-date', '');
  time.appendChild(timeNow);
  time.appendChild(timeDate);
  head.appendChild(time);

  function update(): void {
    const now = new Date();
    timeNow.textContent = pad2(now.getHours()) + ':' + pad2(now.getMinutes());
    const lang = getLang();
    const weekdays = WEEKDAYS[lang] || WEEKDAYS.uk;
    const months = MONTHS[lang] || MONTHS.uk;
    timeDate.textContent = weekdays[now.getDay()] + ', ' + now.getDate() + ' ' + months[now.getMonth()];
  }

  // Re-arm on the next minute boundary so the clock never lags real time by
  // more than the tick that just fired (a fixed 30s interval could show a
  // minute up to ~30s stale).
  let timer = 0;
  function tick(): void {
    update();
    const now = new Date();
    const msToNextMinute = (60 - now.getSeconds()) * 1000 - now.getMilliseconds();
    timer = window.setTimeout(tick, msToNextMinute);
  }
  tick();

  return {
    el: head,
    destroy: function () {
      window.clearTimeout(timer);
    },
  };
}
