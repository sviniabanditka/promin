// Person screen — an actor / director opened from a search "People" row.
// Header: photo, name, department, dates, a few lines of biography; below, the
// filmography as the usual card grid (most popular first, from
// GET /api/v1/catalog/person/{id}). Mode 'content' = the grid; Left from the
// leftmost card → rail, Back → previous screen.

import Controller, { on } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t } from '../core/i18n';
import * as router from '../core/router';
import { ScreenInstance } from '../core/activity';
import { getPerson, PersonDetail, imgSize } from '../core/api';
import { el, empty } from '../ui/dom';
import { Background } from '../ui/background';
import { buildMenu, Menu } from '../ui/menu';
import { buildHead, Head } from '../ui/head';
import { buildFooter } from '../ui/shell';
import { buildCard } from '../ui/card';
import { buildState } from '../ui/state';
import { openTitle } from './nav';

export interface PersonParams {
  id: number;
}

// TMDB departments come in English regardless of `language`; label the common
// ones in the UI language, pass anything else through.
export function deptLabel(d: string | undefined): string {
  const k = (d || '').toLowerCase();
  if (k === 'acting') return t('dept.acting');
  if (k === 'directing') return t('dept.directing');
  if (k === 'writing') return t('dept.writing');
  if (k === 'production') return t('dept.production');
  if (k === 'sound') return t('dept.sound');
  return d || '';
}

export function mountPerson(container: HTMLElement, params: PersonParams): ScreenInstance {
  container.className += ' person-screen';

  const background = new Background();
  container.appendChild(background.render());
  const menu: Menu = buildMenu('none', 'content');
  container.appendChild(menu.el);
  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  // Header stays put above the grid (like the catalog's filter bar); only the
  // filmography scrolls, so the name never slides under the top bar.
  const wrap = el('div', 'person-wrap');
  const hero = el('div', 'person-hero');
  wrap.appendChild(hero);
  const section = el('div', 'person-section hide');
  wrap.appendChild(section);
  const gridWrap = el('div', 'person-grid-wrap');
  const scroll = new Scroll({ mask: true, over: true });
  gridWrap.appendChild(scroll.render());
  wrap.appendChild(gridWrap);
  container.appendChild(wrap);
  const body = scroll.body();
  body.classList.add('person-body');

  let destroyed = false;
  let paused = false;
  let lastCard: HTMLElement | false = false;

  const controller = {
    toggle: function () {
      Controller.collectionSet(scroll.render());
      Controller.collectionFocus(lastCard || false, scroll.render());
    },
    left: function () {
      Controller.moveOr('left', function () {
        Controller.toggle('menu');
      });
    },
    right: function () {
      Controller.moveOr('right');
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    back: function () {
      router.back();
    },
  };

  function activate(): void {
    Controller.add('content', controller);
    if (!paused) Controller.toggle('content');
  }

  function render(p: PersonDetail): void {
    empty(body);
    empty(hero);
    if (p.photo) {
      const img = document.createElement('img');
      img.className = 'person-hero__photo';
      img.src = imgSize(p.photo, 'w342');
      img.alt = p.name;
      hero.appendChild(img);
    } else {
      hero.appendChild(el('div', 'person-hero__photo person-hero__photo--empty', p.name.charAt(0)));
    }
    const info = el('div', 'person-hero__info');
    info.appendChild(el('div', 'person-hero__name', p.name));
    const meta: string[] = [];
    if (p.department) meta.push(deptLabel(p.department));
    if (p.birthday) meta.push(t('person.born') + ': ' + p.birthday + (p.place_of_birth ? ', ' + p.place_of_birth : ''));
    if (p.deathday) meta.push(t('person.died') + ': ' + p.deathday);
    if (meta.length) info.appendChild(el('div', 'person-hero__meta', meta.join(' · ')));
    if (p.biography) info.appendChild(el('div', 'person-hero__bio', p.biography));
    hero.appendChild(info);

    section.textContent = t('person.credits');
    section.classList.remove('hide');
    const grid = el('div', 'person-grid');
    const credits = p.credits || [];
    if (!credits.length) {
      grid.appendChild(buildState({ kind: 'empty', text: t('person.empty') }));
    }
    for (let i = 0; i < credits.length; i++) {
      const card = buildCard(credits[i], true);
      on(card, 'hover:focus', function () {
        lastCard = card;
      });
      on(card, 'hover:enter', function () {
        const tmdb = parseInt(card.getAttribute('data-tmdb') || '0', 10);
        const type = card.getAttribute('data-type') || 'movie';
        if (tmdb) openTitle(type as 'movie' | 'tv', tmdb);
      });
      grid.appendChild(card);
    }
    body.appendChild(grid);
    // Posters were deferred (lazy) so the header paints first; reveal them now.
    const imgs = grid.querySelectorAll('img[data-src]');
    for (let i = 0; i < imgs.length; i++) {
      const im = imgs[i] as HTMLImageElement;
      im.src = im.getAttribute('data-src') || '';
      im.removeAttribute('data-src');
    }
    activate();
  }

  function showError(): void {
    empty(hero);
    section.classList.add('hide');
    empty(body);
    body.appendChild(
      buildState({
        kind: 'error',
        text: t('error.load'),
        onRetry: load,
      })
    );
    activate();
  }

  function load(): void {
    empty(body);
    body.appendChild(buildState({ kind: 'loading' }));
    getPerson(params.id).then(
      function (p) {
        if (destroyed) return;
        if (p && p.id) render(p);
        else showError();
      },
      function () {
        if (destroyed) return;
        showError();
      }
    );
  }

  load();

  return {
    destroy: function () {
      destroyed = true;
      head.destroy();
    },
    pause: function () {
      paused = true;
    },
    resume: function () {
      paused = false;
      menu.activate();
      activate();
    },
  };
}
