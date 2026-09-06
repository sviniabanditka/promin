// DOM templates, mirroring Lampa's src/templates/*.js. Only the ones the
// Home screen needs. Built with createElement (no innerHTML string parsing)
// so this stays cheap on old webviews.

function scroll(): HTMLElement {
  // <div class="scroll"><div class="scroll__content"><div class="scroll__body"></div></div></div>
  const wrap = document.createElement('div');
  wrap.className = 'scroll';
  const content = document.createElement('div');
  content.className = 'scroll__content';
  const body = document.createElement('div');
  body.className = 'scroll__body';
  content.appendChild(body);
  wrap.appendChild(content);
  return wrap;
}

export default { scroll: scroll };
