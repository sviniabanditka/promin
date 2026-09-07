package httpapi

import "net/http"

// onboardingPage serves the public /onboarding help page: how to install
// Media Station X on Samsung / LG / other TVs, point it at promin.club and log
// in with a PIN. Open pre-gate (informational, no session needed). Self-contained
// modern HTML — phone + desktop, no ES5 constraint, no external assets.
//
// Screenshot slots are marked `.shot` figures with a data-shot key; drop an
// <img> inside each later (search the key to find where each screenshot goes).
func onboardingPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(onboardingHTML))
}

const onboardingHTML = `<!doctype html>
<html lang="uk">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>Promin · Інструкція</title>
<link rel="icon" href="/logo.svg" type="image/svg+xml">
<style>
  :root {
    color-scheme: dark;
    --bg:#0a0c10; --bg2:#0e121a; --panel:#141821; --panel2:#171c26;
    --line:#232a36; --ink:#f3f5f9; --muted:#98a2b3; --faint:#6b7480;
    --accent:#7ca8ff; --accent2:#a48bff; --good:#57c66a; --danger:#ff6b6b;
    --radius:16px; --radius-sm:10px;
  }
  * { box-sizing:border-box; -webkit-tap-highlight-color:transparent; }
  html { scroll-behavior:smooth; }
  body {
    margin:0; background:var(--bg); color:var(--ink);
    font:16px/1.6 -apple-system,system-ui,Segoe UI,Roboto,Helvetica,Arial,sans-serif;
    -webkit-font-smoothing:antialiased;
  }
  a { color:var(--accent); text-decoration:none; }
  .wrap { max-width:760px; margin:0 auto; padding:0 18px calc(56px + env(safe-area-inset-bottom)); }

  /* hero */
  .hero {
    position:relative; overflow:hidden; margin:0 -18px 8px;
    padding:56px 18px 40px; text-align:center;
    background:
      radial-gradient(1200px 400px at 50% -120px, rgba(124,168,255,.22), transparent 70%),
      radial-gradient(700px 300px at 90% 0, rgba(164,139,255,.16), transparent 70%),
      var(--bg);
    border-bottom:1px solid var(--line);
  }
  .logo {
    display:inline-flex; align-items:center; gap:12px; font-weight:800;
    font-size:30px; letter-spacing:.5px;
  }
  .logo img { width:44px; height:44px; }
  .hero p { max-width:520px; margin:14px auto 0; color:var(--muted); font-size:17px; }
  .hero .badge { display:inline-block; margin-bottom:18px; font-size:12.5px;
    letter-spacing:1.5px; text-transform:uppercase; color:var(--accent);
    padding:5px 12px; border:1px solid var(--line); border-radius:999px;
    background:var(--panel); }

  /* quick nav */
  .steps-map { display:flex; gap:10px; flex-wrap:wrap; justify-content:center;
    margin:22px 0 4px; }
  .steps-map a { font-size:13.5px; color:var(--muted); padding:7px 13px;
    border:1px solid var(--line); border-radius:999px; background:var(--panel); }
  .steps-map a:hover { color:var(--ink); border-color:var(--accent); }

  h2.step {
    display:flex; align-items:center; gap:14px;
    font-size:22px; margin:40px 0 16px; scroll-margin-top:16px;
  }
  h2.step .n {
    flex:none; width:34px; height:34px; border-radius:50%;
    display:grid; place-items:center; font-size:16px; font-weight:700;
    color:#0a0c10; background:linear-gradient(135deg,var(--accent),var(--accent2));
  }
  h3 { font-size:18px; margin:22px 0 10px; }
  p.lead { color:var(--muted); margin:0 0 14px; }

  /* accordion */
  .acc { border:1px solid var(--line); border-radius:var(--radius);
    background:var(--panel); margin-bottom:14px; overflow:hidden; }
  .acc > summary {
    list-style:none; cursor:pointer; padding:18px 20px;
    display:flex; align-items:center; justify-content:space-between; gap:12px;
    font-weight:600; font-size:17px;
  }
  .acc > summary::-webkit-details-marker { display:none; }
  .acc > summary .chev { flex:none; transition:transform .22s ease; color:var(--muted); }
  .acc[open] > summary .chev { transform:rotate(180deg); }
  .acc[open] > summary { border-bottom:1px solid var(--line); }
  .acc .body { padding:6px 20px 20px; }

  /* tabs */
  .tabs { display:flex; gap:8px; margin:16px 0 18px; flex-wrap:wrap; }
  .tabs button {
    font:inherit; font-weight:600; font-size:14.5px; cursor:pointer;
    padding:9px 16px; border-radius:999px; border:1px solid var(--line);
    background:var(--bg2); color:var(--muted); transition:.15s;
  }
  .tabs button:hover { color:var(--ink); }
  .tabs button.on { background:linear-gradient(135deg,var(--accent),var(--accent2));
    color:#0a0c10; border-color:transparent; }
  .pane { display:none; }
  .pane.on { display:block; animation:fade .25s ease; }
  @keyframes fade { from { opacity:0; transform:translateY(4px); } to { opacity:1; transform:none; } }

  /* ordered how-to list */
  ol.how { list-style:none; counter-reset:s; margin:0; padding:0; }
  ol.how > li { counter-increment:s; position:relative; padding:0 0 18px 44px; }
  ol.how > li::before {
    content:counter(s); position:absolute; left:0; top:-2px;
    width:28px; height:28px; border-radius:50%; display:grid; place-items:center;
    font-size:14px; font-weight:700; color:var(--accent);
    border:1px solid var(--line); background:var(--bg2);
  }
  ol.how > li:not(:last-child)::after {
    content:""; position:absolute; left:14px; top:28px; bottom:2px; width:1px;
    background:var(--line);
  }
  ol.how b { color:var(--ink); }

  code, .kbd {
    font:14px ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
    background:var(--bg2); border:1px solid var(--line); border-radius:7px;
    padding:2px 7px; color:#cfe0ff;
  }
  .url { display:flex; align-items:center; gap:10px; flex-wrap:wrap;
    background:var(--bg2); border:1px solid var(--line); border-radius:var(--radius-sm);
    padding:12px 14px; margin:12px 0; }
  .url code { background:transparent; border:none; padding:0; font-size:15px; color:#cfe0ff; word-break:break-all; }
  .copy { margin-left:auto; font:inherit; font-size:13px; font-weight:600; cursor:pointer;
    padding:7px 13px; border-radius:8px; border:1px solid var(--line);
    background:var(--panel); color:var(--ink); }
  .copy.done { color:var(--good); border-color:var(--good); }

  /* screenshot slot */
  figure.shot { margin:12px 0; }
  figure.shot .box {
    border:1px dashed var(--line); border-radius:var(--radius-sm);
    background:repeating-linear-gradient(45deg,var(--bg2),var(--bg2) 12px,#0c1017 12px,#0c1017 24px);
    aspect-ratio:16/9; display:grid; place-items:center;
    color:var(--faint); font-size:13.5px; text-align:center; padding:16px;
  }
  figure.shot img { width:100%; border-radius:var(--radius-sm); border:1px solid var(--line); display:block; }
  figure.shot figcaption { color:var(--faint); font-size:13px; margin-top:8px; text-align:center; }

  .note { display:flex; gap:10px; padding:14px 16px; border-radius:var(--radius-sm);
    background:rgba(124,168,255,.08); border:1px solid rgba(124,168,255,.28);
    color:#cdddff; font-size:14.5px; margin:14px 0; }
  .note.warn { background:rgba(255,107,107,.07); border-color:rgba(255,107,107,.3); color:#ffd4d4; }
  .note .i { flex:none; }

  /* features */
  .feat { display:grid; grid-template-columns:repeat(auto-fill,minmax(220px,1fr)); gap:12px; }
  .feat .f { background:var(--panel); border:1px solid var(--line); border-radius:var(--radius);
    padding:16px; }
  .feat .f .ico { font-size:22px; margin-bottom:8px; }
  .feat .f b { display:block; font-size:15.5px; margin-bottom:3px; }
  .feat .f span { color:var(--muted); font-size:14px; }

  footer { margin-top:44px; padding-top:22px; border-top:1px solid var(--line);
    color:var(--faint); font-size:13.5px; text-align:center; }
</style>
</head>
<body>
<div class="wrap">

  <header class="hero">
    <div class="badge">Домашній кінотеатр</div>
    <div class="logo"><img src="/logo.svg" alt="">Promin</div>
    <p>Ваш особистий медіапортал для Smart-TV: фільми, серіали, онлайн-джерела й торренти в одному застосунку. Керується пультом. Ця сторінка — як усе встановити та почати.</p>
    <nav class="steps-map">
      <a href="#step1">1 · Встановити MSX</a>
      <a href="#step2">2 · Налаштувати</a>
      <a href="#step3">3 · Увійти за PIN</a>
      <a href="#feat">Можливості</a>
    </nav>
  </header>

  <!-- ============ STEP 1 — install MSX ============ -->
  <h2 class="step" id="step1"><span class="n">1</span>Встановити Media Station X</h2>
  <p class="lead">Promin працює всередині безкоштовного застосунку <b>Media Station X</b> (MSX) — легкої оболонки, яка вже є в магазинах Samsung, LG та інших ТВ. Оберіть свій телевізор:</p>

  <details class="acc" open>
    <summary>Інструкція встановлення MSX <span class="chev">▾</span></summary>
    <div class="body">
      <div class="tabs" role="tablist">
        <button class="on" data-tab="samsung">Samsung</button>
        <button data-tab="lg">LG</button>
        <button data-tab="other">Інші ТВ</button>
      </div>

      <!-- Samsung -->
      <div class="pane on" data-pane="samsung">
        <ol class="how">
          <li>На пульті натисніть <b>Home</b> і відкрийте <b>Apps</b> (магазин застосунків Samsung).</li>
          <li>Відкрийте пошук (іконка 🔍) і введіть <b>Media Station X</b>.</li>
          <li>Оберіть застосунок і натисніть <b>Встановити</b>, дочекайтесь завершення.</li>
          <li>Запустіть <b>Media Station X</b> — перейдіть до <a href="#step2">Кроку 2</a>.</li>
        </ol>
        <figure class="shot" data-shot="samsung-store">
          <div class="box">скріншот: Media Station X у Samsung Apps</div>
          <figcaption>Samsung Apps → пошук «Media Station X»</figcaption>
        </figure>
      </div>

      <!-- LG -->
      <div class="pane" data-pane="lg">
        <ol class="how">
          <li>На пульті натисніть <b>Home</b> і відкрийте <b>LG Content Store</b>.</li>
          <li>У пошуку введіть <b>Media Station X</b>.</li>
          <li>Натисніть <b>Встановити</b> та дочекайтесь завершення.</li>
          <li>Запустіть <b>Media Station X</b> — перейдіть до <a href="#step2">Кроку 2</a>.</li>
        </ol>
        <figure class="shot" data-shot="lg-store">
          <div class="box">скріншот: Media Station X у LG Content Store</div>
          <figcaption>LG Content Store → пошук «Media Station X»</figcaption>
        </figure>
      </div>

      <!-- Other -->
      <div class="pane" data-pane="other">
        <p class="lead">Android TV, Google TV, Fire TV, приставки та інші платформи:</p>
        <ol class="how">
          <li>Відкрийте магазин застосунків вашого пристрою (<b>Google Play</b>, <b>Amazon Appstore</b> тощо).</li>
          <li>Знайдіть і встановіть <b>Media Station X</b>.</li>
          <li>Якщо застосунку немає в магазині — відкрийте <a href="https://msx.benzac.de/" target="_blank" rel="noopener">msx.benzac.de</a> у браузері ТВ (той самий MSX у веб-версії).</li>
          <li>Запустіть MSX — перейдіть до <a href="#step2">Кроку 2</a>.</li>
        </ol>
        <figure class="shot" data-shot="other-store">
          <div class="box">скріншот: встановлення MSX на інших ТВ</div>
          <figcaption>MSX у магазині застосунків / у браузері</figcaption>
        </figure>
      </div>
    </div>
  </details>

  <!-- ============ STEP 2 — point MSX at Promin ============ -->
  <h2 class="step" id="step2"><span class="n">2</span>Підключити Promin</h2>
  <p class="lead">У MSX потрібно один раз вказати стартову адресу Promin. Далі MSX завжди відкриватиме портал сам.</p>

  <details class="acc" open>
    <summary>Вказати стартову сторінку в MSX <span class="chev">▾</span></summary>
    <div class="body">
      <ol class="how">
        <li>Відкрийте <b>Media Station X</b> → зайдіть у <b>Settings</b> (Налаштування) → <b>Start Parameter</b>.</li>
        <li>Введіть адресу стартового меню Promin:
          <div class="url">
            <code id="startUrl">promin.club/msx/start.json</code>
            <button class="copy" data-copy="startUrl">Копіювати</button>
          </div>
        </li>
        <li>Збережіть і перезапустіть MSX — відкриється головний екран <b>Promin</b>.</li>
      </ol>
      <div class="note"><span class="i">💡</span><span>Деякі версії MSX приймають просто <code>promin.club</code> — але надійніше вводити повний шлях <code>/msx/start.json</code>.</span></div>
      <figure class="shot" data-shot="msx-setup">
        <div class="box">скріншот: поле Start Parameter у MSX</div>
        <figcaption>MSX → Settings → Start Parameter</figcaption>
      </figure>
    </div>
  </details>

  <!-- ============ STEP 3 — PIN login ============ -->
  <h2 class="step" id="step3"><span class="n">3</span>Увійти за PIN-кодом</h2>
  <p class="lead">Promin закритий за замовчуванням. Доступ — лише за особистим 6-значним PIN-кодом, який видає власник порталу. Один PIN = один акаунт із власною синхронізацією.</p>

  <details class="acc" open>
    <summary>Перший вхід <span class="chev">▾</span></summary>
    <div class="body">
      <ol class="how">
        <li>Після запуску Promin показує екран вводу <b>PIN</b>.</li>
        <li>Введіть свій <b>6-значний код</b> пультом (цифри або екранна клавіатура).</li>
        <li>Готово — відкриється ваш акаунт: історія, обране й плейлисти підтягнуться автоматично.</li>
      </ol>
      <div class="note"><span class="i">🔑</span><span>PIN працює з <b>будь-якого пристрою</b>: увійшли тим самим кодом на іншому ТВ — і бачите той самий акаунт. Немає коду — попросіть власника порталу створити профіль в адмін-панелі.</span></div>
      <figure class="shot" data-shot="pin-screen">
        <div class="box">скріншот: екран вводу PIN у Promin</div>
        <figcaption>Екран вводу PIN</figcaption>
      </figure>
    </div>
  </details>

  <!-- ============ features ============ -->
  <h2 class="step" id="feat"><span class="n">★</span>Можливості</h2>
  <p class="lead">Що вміє Promin після входу:</p>
  <div class="feat">
    <div class="f"><div class="ico">🎬</div><b>Каталог</b><span>Фільми й серіали з постерами, рейтингами та описами (TMDB).</span></div>
    <div class="f"><div class="ico">📡</div><b>Онлайн-джерела</b><span>Перегляд онлайн через вбудовані балансери — без завантаження.</span></div>
    <div class="f"><div class="ico">🧲</div><b>Торренти</b><span>Пряме відтворення роздач із паузою й перемоткою, вибір аудіодоріжки.</span></div>
    <div class="f"><div class="ico">▶️</div><b>Продовжити перегляд</b><span>Таймкоди зберігаються — повертайтесь туди, де зупинились.</span></div>
    <div class="f"><div class="ico">📚</div><b>Бібліотека</b><span>Обране й плейлисти в одному розділі, синхронні між пристроями.</span></div>
    <div class="f"><div class="ico">👥</div><b>Профілі</b><span>У кожного свій PIN і свій акаунт — історія не змішується.</span></div>
  </div>

  <footer>
    Promin · <a href="https://promin.club">promin.club</a><br>
    Потрібен PIN-код? Зверніться до власника порталу.
  </footer>

</div>

<script>
  // tabs (scoped per accordion)
  document.querySelectorAll('.tabs').forEach(function (tabs) {
    var scope = tabs.closest('.body');
    tabs.querySelectorAll('button').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var key = btn.getAttribute('data-tab');
        tabs.querySelectorAll('button').forEach(function (b) { b.classList.toggle('on', b === btn); });
        scope.querySelectorAll('.pane').forEach(function (p) {
          p.classList.toggle('on', p.getAttribute('data-pane') === key);
        });
      });
    });
  });

  // copy-to-clipboard
  document.querySelectorAll('.copy').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var el = document.getElementById(btn.getAttribute('data-copy'));
      var text = el ? el.textContent : '';
      var done = function () { btn.textContent = 'Скопійовано'; btn.classList.add('done');
        setTimeout(function () { btn.textContent = 'Копіювати'; btn.classList.remove('done'); }, 1600); };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done, done);
      } else {
        var r = document.createRange(); r.selectNode(el);
        var s = window.getSelection(); s.removeAllRanges(); s.addRange(r);
        try { document.execCommand('copy'); } catch (e) {}
        s.removeAllRanges(); done();
      }
    });
  });
</script>
</body>
</html>`
