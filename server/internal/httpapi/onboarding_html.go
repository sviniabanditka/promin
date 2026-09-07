package httpapi

import "net/http"

// onboardingPage serves the public /onboarding help page. Three top-level
// sections: Install (Media Station X on Samsung / LG / other TVs, start URL,
// PIN login), Telegram (why the bot + Mini App, how to link, several phones)
// and Tips/FAQ. Open pre-gate (informational, no session needed).
// Self-contained modern HTML — phone + desktop, no ES5 constraint, no
// external assets.
//
// i18n: every visible string is a `data-t="key"` element; the JS dictionary T
// maps key → [uk, ru, en] (a triple per key, so parity holds by construction;
// TestOnboardingI18n checks the HTML keys against the dictionary). Language:
// ?lang= → localStorage → navigator.language → uk; the switcher is in the hero.
//
// Screenshot slots are marked `.shot` figures with a data-shot key. A filled
// slot holds an <img src="/shot-<key>.webp?v=N"> (file in web/assets/, copied
// to the bundle root by the web build) with data-t-alt for its alt text; an
// empty one still shows the dashed `.box` placeholder.
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
  figure.shot img { width:100%; height:auto; border-radius:var(--radius-sm); border:1px solid var(--line); display:block; }
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
  /* language switcher (hero, top-right) */
  .lang { position:absolute; top:14px; right:14px; display:flex; gap:4px;
    padding:3px; border:1px solid var(--line); border-radius:999px; background:var(--panel); }
  .lang button { font:inherit; font-size:12.5px; font-weight:700; letter-spacing:.5px; cursor:pointer;
    padding:5px 10px; border-radius:999px; border:none; background:transparent; color:var(--muted); }
  .lang button.on { background:var(--line); color:var(--ink); }

  /* top-level sections */
  .top { position:sticky; top:0; z-index:5; display:flex; gap:6px; margin:0 -18px; padding:10px 18px;
    background:rgba(10,12,16,.92); backdrop-filter:blur(10px); border-bottom:1px solid var(--line);
    overflow-x:auto; scrollbar-width:none; }
  .top::-webkit-scrollbar { display:none; }
  .top button { flex:none; font:inherit; font-weight:600; font-size:14.5px; cursor:pointer;
    padding:10px 16px; border-radius:12px; border:1px solid transparent; background:transparent; color:var(--muted); }
  .top button:hover { color:var(--ink); }
  .top button.on { background:var(--panel); border-color:var(--line); color:var(--ink); }
  .sec { display:none; }
  .sec.on { display:block; animation:fade .25s ease; }
  .acc .body p { margin:8px 0 0; color:var(--muted); }
  .acc .body p b, .acc .body p code, .acc .body p a { color:var(--ink); }

</style>
</head>
<body>
<div class="wrap">

  <header class="hero">
    <div class="lang" id="lang">
      <button data-lang="uk">UK</button><button data-lang="ru">RU</button><button data-lang="en">EN</button>
    </div>
    <div class="badge" data-t="badge"></div>
    <div class="logo"><img src="/logo.svg" alt="">Promin</div>
    <p data-t="hero_p"></p>
  </header>

  <nav class="top" id="top">
    <button class="on" data-sec="install" data-t="tab_install"></button>
    <button data-sec="telegram" data-t="tab_tg"></button>
    <button data-sec="faq" data-t="tab_faq"></button>
  </nav>

  <section class="sec on" data-sec="install">
  <nav class="steps-map">
    <a href="#step1" data-t="nav1"></a>
    <a href="#step2" data-t="nav2"></a>
    <a href="#step3" data-t="nav3"></a>
    <a href="#feat" data-t="nav4"></a>
  </nav>

  <h2 class="step" id="step1"><span class="n">1</span><span data-t="s1_h"></span></h2>
  <p class="lead" data-t="s1_lead"></p>
  <details class="acc" open>
    <summary><span data-t="s1_sum"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <div class="tabs" role="tablist">
        <button class="on" data-tab="samsung">Samsung</button>
        <button data-tab="lg">LG</button>
        <button data-tab="other" data-t="tv_other"></button>
      </div>
      <div class="pane on" data-pane="samsung">
        <ol class="how"><li data-t="sam1"></li><li data-t="sam2"></li><li data-t="sam3"></li><li data-t="go2"></li></ol>
        <figure class="shot" data-shot="samsung-store">
          <div class="box" data-t="sam_shot"></div>
          <figcaption data-t="sam_cap"></figcaption>
        </figure>
      </div>
      <div class="pane" data-pane="lg">
        <ol class="how"><li data-t="lg1"></li><li data-t="lg2"></li><li data-t="lg3"></li><li data-t="go2"></li></ol>
        <figure class="shot" data-shot="lg-store">
          <div class="box" data-t="lg_shot"></div>
          <figcaption data-t="lg_cap"></figcaption>
        </figure>
      </div>
      <div class="pane" data-pane="other">
        <p class="lead" data-t="oth_lead"></p>
        <ol class="how"><li data-t="oth1"></li><li data-t="oth2"></li><li data-t="oth3"></li><li data-t="oth4"></li></ol>
        <figure class="shot" data-shot="other-store">
          <div class="box" data-t="oth_shot"></div>
          <figcaption data-t="oth_cap"></figcaption>
        </figure>
      </div>
    </div>
  </details>

  <h2 class="step" id="step2"><span class="n">2</span><span data-t="s2_h"></span></h2>
  <p class="lead" data-t="s2_lead"></p>
  <details class="acc" open>
    <summary><span data-t="s2_sum"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <ol class="how">
        <li data-t="s2_1"></li>
        <li><span data-t="s2_2"></span>
          <div class="url">
            <code id="startUrl">promin.club</code>
            <button class="copy" data-copy="startUrl" data-t="copy"></button>
          </div>
        </li>
        <li data-t="s2_3"></li>
      </ol>
      <div class="note"><span class="i">💡</span><span data-t="s2_note"></span></div>
      <figure class="shot" data-shot="msx-setup">
          <img src="/shot-msx-setup.webp?v=1" width="1440" height="856" loading="lazy" alt="" data-t-alt="s2_shot">
          <figcaption data-t="s2_cap"></figcaption>
        </figure>
    </div>
  </details>

  <h2 class="step" id="step3"><span class="n">3</span><span data-t="s3_h"></span></h2>
  <p class="lead" data-t="s3_lead"></p>
  <details class="acc" open>
    <summary><span data-t="s3_sum"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <ol class="how"><li data-t="s3_1"></li><li data-t="s3_2"></li><li data-t="s3_3"></li></ol>
      <div class="note"><span class="i">🔑</span><span data-t="s3_note"></span></div>
      <figure class="shot" data-shot="pin-screen">
          <img src="/shot-pin-screen.webp?v=1" width="1440" height="810" loading="lazy" alt="" data-t-alt="s3_shot">
          <figcaption data-t="s3_cap"></figcaption>
        </figure>
    </div>
  </details>

  <h2 class="step" id="feat"><span class="n">★</span><span data-t="feat_h"></span></h2>
  <p class="lead" data-t="feat_lead"></p>
  <div class="feat">
    <div class="f"><div class="ico">🎬</div><b data-t="f1_b"></b><span data-t="f1"></span></div>
    <div class="f"><div class="ico">📡</div><b data-t="f2_b"></b><span data-t="f2"></span></div>
    <div class="f"><div class="ico">🧲</div><b data-t="f3_b"></b><span data-t="f3"></span></div>
    <div class="f"><div class="ico">▶️</div><b data-t="f4_b"></b><span data-t="f4"></span></div>
    <div class="f"><div class="ico">📚</div><b data-t="f5_b"></b><span data-t="f5"></span></div>
    <div class="f"><div class="ico">👥</div><b data-t="f6_b"></b><span data-t="f6"></span></div>
  </div>
</section>
  <section class="sec" data-sec="telegram">

  <h2 class="step" id="tg"><span class="n">✈</span><span data-t="tg_h"></span></h2>
  <p class="lead" data-t="tg_lead"></p>
  <h3 data-t="tg_why_h"></h3>
  <div class="feat">
    <div class="f"><div class="ico">🔍</div><b data-t="w1_b"></b><span data-t="w1"></span></div>
    <div class="f"><div class="ico">🎛</div><b data-t="w2_b"></b><span data-t="w2"></span></div>
    <div class="f"><div class="ico">▶️</div><b data-t="w3_b"></b><span data-t="w3"></span></div>
    <div class="f"><div class="ico">📱</div><b data-t="w4_b"></b><span data-t="w4"></span></div>
    <div class="f"><div class="ico">👨‍👩‍👧</div><b data-t="w5_b"></b><span data-t="w5"></span></div>
    <div class="f"><div class="ico">🌐</div><b data-t="w6_b"></b><span data-t="w6"></span></div>
  </div>

  <h2 class="step" id="tg1"><span class="n">1</span><span data-t="tg1_h"></span></h2>
  <p class="lead" data-t="tg1_lead"></p>
  <details class="acc" open>
    <summary><span data-t="tg1_sum"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <ol class="how"><li data-t="tg1_1"></li><li data-t="tg1_2"></li><li data-t="tg1_3"></li><li data-t="tg1_4"></li><li data-t="tg1_5"></li></ol>
      <figure class="shot" data-shot="tg-link">
          <div class="box" data-t="tg1_shot"></div>
          <figcaption data-t="tg1_cap"></figcaption>
        </figure>
    </div>
  </details>

  <h2 class="step" id="tg2"><span class="n">2</span><span data-t="tg2_h"></span></h2>
  <p class="lead" data-t="tg2_lead"></p>
  <details class="acc" open>
    <summary><span data-t="tg2_sum"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <ol class="how"><li data-t="k1"></li><li data-t="k2"></li><li data-t="k3"></li><li data-t="k4"></li><li data-t="k5"></li></ol>
      <div class="note"><span class="i">📺</span><span data-t="tg2_note"></span></div>
    </div>
  </details>

  <h2 class="step" id="tg3"><span class="n">3</span><span data-t="tg3_h"></span></h2>
  <p class="lead" data-t="tg3_lead"></p>
  <details class="acc" open>
    <summary><span data-t="tg3_sum"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <ol class="how"><li data-t="m1"></li><li data-t="m2"></li><li data-t="m3"></li><li data-t="m4"></li></ol>
      <figure class="shot" data-shot="tg-miniapp">
          <div class="box" data-t="tg3_shot"></div>
          <figcaption data-t="tg3_cap"></figcaption>
        </figure>
    </div>
  </details>

  <h2 class="step" id="tg4"><span class="n">4</span><span data-t="tg4_h"></span></h2>
  <p class="lead" data-t="tg4_lead"></p>
  <details class="acc" open>
    <summary><span data-t="tg4_sum"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <ol class="how"><li data-t="a1"></li><li data-t="a2"></li><li data-t="a3"></li></ol>
      <div class="note"><span class="i">👥</span><span data-t="tg4_note"></span></div>
    </div>
  </details>
</section>
  <section class="sec" data-sec="faq">
  <h2 class="step" id="faq"><span class="n">?</span><span data-t="faq_h"></span></h2>
  <p class="lead" data-t="faq_lead"></p>
  <details class="acc">
    <summary><span data-t="q1"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <p data-t="q1a"></p>
    </div>
  </details>
  <details class="acc">
    <summary><span data-t="q2"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <p data-t="q2a"></p>
    </div>
  </details>
  <details class="acc">
    <summary><span data-t="q3"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <p data-t="q3a"></p>
    </div>
  </details>
  <details class="acc">
    <summary><span data-t="q4"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <p data-t="q4a"></p>
    </div>
  </details>
  <details class="acc">
    <summary><span data-t="q5"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <p data-t="q5a"></p>
    </div>
  </details>
  <details class="acc">
    <summary><span data-t="q6"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <p data-t="q6a"></p>
    </div>
  </details>
  <details class="acc">
    <summary><span data-t="q7"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <p data-t="q7a"></p>
    </div>
  </details>
  <details class="acc">
    <summary><span data-t="q8"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <p data-t="q8a"></p>
    </div>
  </details>
  <details class="acc">
    <summary><span data-t="q9"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <p data-t="q9a"></p>
    </div>
  </details>
  <details class="acc">
    <summary><span data-t="q10"></span> <span class="chev">▾</span></summary>
    <div class="body">
      <p data-t="q10a"></p>
    </div>
  </details>
</section>

  <footer>
    Promin · <a href="https://promin.club">promin.club</a><br>
    <span data-t="footer"></span>
  </footer>

</div>

<script>
  // i18n: T[key] = [uk, ru, en]. Language: ?lang= -> localStorage -> navigator -> uk.
  var T = {
    title: ["Promin · Інструкція", "Promin · Инструкция", "Promin · Guide"],
    badge: ["Домашній кінотеатр", "Домашний кинотеатр", "Home cinema"],
    hero_p: ["Ваш особистий медіапортал для Smart-TV: фільми, серіали, онлайн-джерела й торренти в одному застосунку. Керується пультом — або телефоном через Telegram.", "Ваш личный медиапортал для Smart-TV: фильмы, сериалы, онлайн-источники и торренты в одном приложении. Управляется пультом — или телефоном через Telegram.", "Your personal Smart-TV media portal: movies, series, online sources and torrents in one app. Driven by the remote — or by your phone through Telegram."],
    tab_install: ["📺 Встановлення", "📺 Установка", "📺 Install"],
    tab_tg: ["✈️ Telegram", "✈️ Telegram", "✈️ Telegram"],
    tab_faq: ["💡 Поради та FAQ", "💡 Советы и FAQ", "💡 Tips & FAQ"],
    nav1: ["1 · Встановити MSX", "1 · Установить MSX", "1 · Install MSX"],
    nav2: ["2 · Налаштувати", "2 · Настроить", "2 · Set up"],
    nav3: ["3 · Увійти за PIN", "3 · Войти по PIN", "3 · PIN login"],
    nav4: ["Можливості", "Возможности", "Features"],
    s1_h: ["Встановити Media Station X", "Установить Media Station X", "Install Media Station X"],
    s1_lead: ["Promin працює всередині безкоштовного застосунку <b>Media Station X</b> (MSX) — легкої оболонки, яка вже є в магазинах Samsung, LG та інших ТВ. Оберіть свій телевізор:", "Promin работает внутри бесплатного приложения <b>Media Station X</b> (MSX) — лёгкой оболочки, которая уже есть в магазинах Samsung, LG и других ТВ. Выберите свой телевизор:", "Promin runs inside the free <b>Media Station X</b> (MSX) app — a light shell already available in the Samsung, LG and other TV stores. Pick your TV:"],
    s1_sum: ["Інструкція встановлення MSX", "Инструкция по установке MSX", "How to install MSX"],
    tv_other: ["Інші ТВ", "Другие ТВ", "Other TVs"],
    sam1: ["На пульті натисніть <b>Home</b> і відкрийте <b>Apps</b> (магазин застосунків Samsung).", "На пульте нажмите <b>Home</b> и откройте <b>Apps</b> (магазин приложений Samsung).", "Press <b>Home</b> on the remote and open <b>Apps</b> (the Samsung app store)."],
    sam2: ["Відкрийте пошук (іконка 🔍) і введіть <b>Media Station X</b>.", "Откройте поиск (иконка 🔍) и введите <b>Media Station X</b>.", "Open search (🔍) and type <b>Media Station X</b>."],
    sam3: ["Оберіть застосунок і натисніть <b>Встановити</b>, дочекайтесь завершення.", "Выберите приложение и нажмите <b>Установить</b>, дождитесь завершения.", "Select the app, press <b>Install</b> and wait for it to finish."],
    go2: ["Запустіть <b>Media Station X</b> — перейдіть до <a href=\"#step2\">Кроку 2</a>.", "Запустите <b>Media Station X</b> — перейдите к <a href=\"#step2\">Шагу 2</a>.", "Launch <b>Media Station X</b> — continue with <a href=\"#step2\">Step 2</a>."],
    sam_shot: ["скріншот: Media Station X у Samsung Apps", "скриншот: Media Station X в Samsung Apps", "screenshot: Media Station X in Samsung Apps"],
    sam_cap: ["Samsung Apps → пошук «Media Station X»", "Samsung Apps → поиск «Media Station X»", "Samsung Apps → search “Media Station X”"],
    lg1: ["На пульті натисніть <b>Home</b> і відкрийте <b>LG Content Store</b>.", "На пульте нажмите <b>Home</b> и откройте <b>LG Content Store</b>.", "Press <b>Home</b> on the remote and open the <b>LG Content Store</b>."],
    lg2: ["У пошуку введіть <b>Media Station X</b>.", "В поиске введите <b>Media Station X</b>.", "Search for <b>Media Station X</b>."],
    lg3: ["Натисніть <b>Встановити</b> та дочекайтесь завершення.", "Нажмите <b>Установить</b> и дождитесь завершения.", "Press <b>Install</b> and wait for it to finish."],
    lg_shot: ["скріншот: Media Station X у LG Content Store", "скриншот: Media Station X в LG Content Store", "screenshot: Media Station X in the LG Content Store"],
    lg_cap: ["LG Content Store → пошук «Media Station X»", "LG Content Store → поиск «Media Station X»", "LG Content Store → search “Media Station X”"],
    oth_lead: ["Android TV, Google TV, Fire TV, приставки та інші платформи:", "Android TV, Google TV, Fire TV, приставки и другие платформы:", "Android TV, Google TV, Fire TV, set-top boxes and other platforms:"],
    oth1: ["Відкрийте магазин застосунків вашого пристрою (<b>Google Play</b>, <b>Amazon Appstore</b> тощо).", "Откройте магазин приложений вашего устройства (<b>Google Play</b>, <b>Amazon Appstore</b> и т. д.).", "Open your device’s app store (<b>Google Play</b>, <b>Amazon Appstore</b>, etc.)."],
    oth2: ["Знайдіть і встановіть <b>Media Station X</b>.", "Найдите и установите <b>Media Station X</b>.", "Find and install <b>Media Station X</b>."],
    oth3: ["Якщо застосунку немає в магазині — відкрийте <a href=\"https://msx.benzac.de/\" target=\"_blank\" rel=\"noopener\">msx.benzac.de</a> у браузері ТВ (той самий MSX у веб-версії).", "Если приложения нет в магазине — откройте <a href=\"https://msx.benzac.de/\" target=\"_blank\" rel=\"noopener\">msx.benzac.de</a> в браузере ТВ (тот же MSX в веб-версии).", "If the store does not have it, open <a href=\"https://msx.benzac.de/\" target=\"_blank\" rel=\"noopener\">msx.benzac.de</a> in the TV browser (the same MSX as a web app)."],
    oth4: ["Запустіть MSX — перейдіть до <a href=\"#step2\">Кроку 2</a>.", "Запустите MSX — перейдите к <a href=\"#step2\">Шагу 2</a>.", "Launch MSX — continue with <a href=\"#step2\">Step 2</a>."],
    oth_shot: ["скріншот: встановлення MSX на інших ТВ", "скриншот: установка MSX на других ТВ", "screenshot: installing MSX on other TVs"],
    oth_cap: ["MSX у магазині застосунків / у браузері", "MSX в магазине приложений / в браузере", "MSX in the app store / in the browser"],
    s2_h: ["Підключити Promin", "Подключить Promin", "Connect Promin"],
    s2_lead: ["У MSX потрібно один раз вказати стартову адресу Promin. Далі MSX завжди відкриватиме портал сам.", "В MSX нужно один раз указать стартовый адрес Promin. Дальше MSX всегда будет открывать портал сам.", "Enter the Promin start address in MSX once. From then on MSX opens the portal by itself."],
    s2_sum: ["Вказати стартову сторінку в MSX", "Указать стартовую страницу в MSX", "Set the start page in MSX"],
    s2_1: ["Відкрийте <b>Media Station X</b> → зайдіть у <b>Settings</b> (Налаштування) → <b>Start Parameter</b>.", "Откройте <b>Media Station X</b> → зайдите в <b>Settings</b> (Настройки) → <b>Start Parameter</b>.", "Open <b>Media Station X</b> → <b>Settings</b> → <b>Start Parameter</b>."],
    s2_2: ["Введіть адресу стартового меню Promin:", "Введите адрес стартового меню Promin:", "Enter the Promin start menu address:"],
    copy: ["Копіювати", "Копировать", "Copy"],
    copied: ["Скопійовано", "Скопировано", "Copied"],
    s2_3: ["Збережіть і перезапустіть MSX — відкриється головний екран <b>Promin</b>.", "Сохраните и перезапустите MSX — откроется главный экран <b>Promin</b>.", "Save and restart MSX — the <b>Promin</b> home screen opens."],
    s2_note: ["Вводьте лише домен — без <code>http://</code> і без шляхів. MSX сам знайде стартове меню Promin.", "Вводите только домен — без <code>http://</code> и без путей. MSX сам найдёт стартовое меню Promin.", "Enter just the domain — no <code>http://</code> and no path. MSX finds the Promin start menu by itself."],
    s2_shot: ["скріншот: поле Start Parameter у MSX", "скриншот: поле Start Parameter в MSX", "screenshot: the Start Parameter field in MSX"],
    s2_cap: ["MSX → Settings → Start Parameter", "MSX → Settings → Start Parameter", "MSX → Settings → Start Parameter"],
    s3_h: ["Увійти за PIN-кодом", "Войти по PIN-коду", "Log in with a PIN"],
    s3_lead: ["Promin закритий за замовчуванням. Доступ — лише за особистим 6-значним PIN-кодом, який видає власник порталу. Один PIN = один акаунт із власною синхронізацією.", "Promin закрыт по умолчанию. Доступ — только по личному 6-значному PIN-коду, который выдаёт владелец портала. Один PIN = один аккаунт со своей синхронизацией.", "Promin is closed by default. Access requires a personal 6-digit PIN issued by the portal owner. One PIN = one account with its own sync."],
    s3_sum: ["Перший вхід", "Первый вход", "First login"],
    s3_1: ["Після запуску Promin показує екран вводу <b>PIN</b>.", "После запуска Promin показывает экран ввода <b>PIN</b>.", "On launch Promin shows the <b>PIN</b> screen."],
    s3_2: ["Введіть свій <b>6-значний код</b> пультом (цифри або екранна клавіатура).", "Введите свой <b>6-значный код</b> пультом (цифры или экранная клавиатура).", "Enter your <b>6-digit code</b> with the remote (number keys or the on-screen pad)."],
    s3_3: ["Готово — відкриється ваш акаунт: історія, обране й плейлисти підтягнуться автоматично.", "Готово — откроется ваш аккаунт: история, избранное и плейлисты подтянутся автоматически.", "Done — your account opens: history, bookmarks and playlists sync automatically."],
    s3_note: ["PIN працює з <b>будь-якого пристрою</b>: увійшли тим самим кодом на іншому ТВ — і бачите той самий акаунт. Немає коду — попросіть власника порталу створити профіль в адмін-панелі.", "PIN работает с <b>любого устройства</b>: вошли тем же кодом на другом ТВ — и видите тот же аккаунт. Нет кода — попросите владельца портала создать профиль в админ-панели.", "The PIN works on <b>any device</b>: log in with the same code on another TV and you see the same account. No code yet? Ask the portal owner to create a profile in the admin panel."],
    s3_shot: ["скріншот: екран вводу PIN у Promin", "скриншот: экран ввода PIN в Promin", "screenshot: the PIN screen in Promin"],
    s3_cap: ["Екран вводу PIN", "Экран ввода PIN", "PIN screen"],
    feat_h: ["Можливості", "Возможности", "Features"],
    feat_lead: ["Що вміє Promin після входу:", "Что умеет Promin после входа:", "What Promin does once you are in:"],
    f1_b: ["Каталог", "Каталог", "Catalog"],
    f1: ["Фільми й серіали з постерами, рейтингами та описами (TMDB).", "Фильмы и сериалы с постерами, рейтингами и описаниями (TMDB).", "Movies and series with posters, ratings and descriptions (TMDB)."],
    f2_b: ["Онлайн-джерела", "Онлайн-источники", "Online sources"],
    f2: ["Перегляд онлайн через вбудовані джерела — без завантаження.", "Просмотр онлайн через встроенные источники — без загрузки.", "Watch online through built-in sources — no downloads."],
    f3_b: ["Торренти", "Торренты", "Torrents"],
    f3: ["Пряме відтворення роздач із паузою й перемоткою, вибір аудіодоріжки.", "Прямое воспроизведение раздач с паузой и перемоткой, выбор аудиодорожки.", "Stream torrents directly with pause, seeking and audio-track choice."],
    f4_b: ["Продовжити перегляд", "Продолжить просмотр", "Continue watching"],
    f4: ["Таймкоди зберігаються — повертайтесь туди, де зупинились.", "Таймкоды сохраняются — возвращайтесь туда, где остановились.", "Positions are saved — pick up where you left off."],
    f5_b: ["Бібліотека", "Библиотека", "Library"],
    f5: ["Обране, плейлисти й черга перегляду, синхронні між пристроями.", "Избранное, плейлисты и очередь просмотра, синхронные между устройствами.", "Bookmarks, playlists and a watch queue, in sync across devices."],
    f6_b: ["Профілі", "Профили", "Profiles"],
    f6: ["У кожного свій PIN і свій акаунт — історія не змішується.", "У каждого свой PIN и свой аккаунт — история не смешивается.", "Everyone has their own PIN and account — histories never mix."],
    tg_h: ["Телефон замість пульта", "Телефон вместо пульта", "Your phone as the remote"],
    tg_lead: ["Набирати назву фільму пультом по літерах — довго. Підключіть Telegram: шукайте з телефона, відкривайте на ТВ одним натисканням і керуйте переглядом не встаючи з дивана.", "Набирать название фильма пультом по буквам — долго. Подключите Telegram: ищите с телефона, открывайте на ТВ одним нажатием и управляйте просмотром не вставая с дивана.", "Typing a title letter by letter with a D-pad is slow. Link Telegram: search from your phone, open on the TV with one tap and control playback without leaving the couch."],
    tg_why_h: ["Чому це зручно", "Почему это удобно", "Why it is handy"],
    w1_b: ["Пошук з телефона", "Поиск с телефона", "Search from the phone"],
    w1: ["Напишіть боту назву — і натисніть «Відкрити на ТВ». Жодного набору пультом.", "Напишите боту название — и нажмите «Открыть на ТВ». Никакого набора пультом.", "Send the bot a title and tap “Open on TV”. No typing with the remote."],
    w2_b: ["Пульт у Telegram", "Пульт в Telegram", "Remote in Telegram"],
    w2: ["Пауза, перемотка ±30 с, серії, звук, доріжка й субтитри, нічний режим, таймер сну.", "Пауза, перемотка ±30 с, серии, звук, дорожка и субтитры, ночной режим, таймер сна.", "Pause, ±30 s seek, episodes, volume, audio track and subtitles, night mode, sleep timer."],
    w3_b: ["Продовжити та закладки", "Продолжить и закладки", "Continue and bookmarks"],
    w3: ["Список «продовжити», обране, черга й підбірки «що подивитись» — усе з телефона.", "Список «продолжить», избранное, очередь и подборки «что посмотреть» — всё с телефона.", "Continue watching, bookmarks, the queue and “what to watch” — all from the phone."],
    w4_b: ["Mini App", "Mini App", "Mini App"],
    w4: ["Повноцінний застосунок усередині Telegram: стан плеєра, пристрої, бібліотека, налаштування ТВ.", "Полноценное приложение внутри Telegram: состояние плеера, устройства, библиотека, настройки ТВ.", "A full app inside Telegram: player state, devices, library, TV settings."],
    w5_b: ["Кілька телефонів", "Несколько телефонов", "Several phones"],
    w5: ["Один профіль — уся родина. Кожен підключає свій Telegram і керує тим самим ТВ.", "Один профиль — вся семья. Каждый подключает свой Telegram и управляет тем же ТВ.", "One profile for the household. Everyone links their own Telegram and controls the same TV."],
    w6_b: ["Одна мова", "Один язык", "One language"],
    w6: ["Мова бота й ТВ спільна: змінили в боті — ТВ перемкнувся одразу.", "Язык бота и ТВ общий: сменили в боте — ТВ переключился сразу.", "The bot and the TV share one language: change it in the bot and the TV follows instantly."],
    tg1_h: ["Підключити бота", "Подключить бота", "Link the bot"],
    tg1_lead: ["Потрібно один раз зв’язати ваш Telegram із профілем Promin. Це робиться на телевізорі за хвилину.", "Нужно один раз связать ваш Telegram с профилем Promin. Это делается на телевизоре за минуту.", "Link your Telegram to the Promin profile once. It takes a minute on the TV."],
    tg1_sum: ["Зв’язати телефон з профілем", "Связать телефон с профилем", "Pair the phone with the profile"],
    tg1_1: ["На ТВ відкрийте <b>Налаштування</b> → рядок <b>Telegram-бот</b> → <b>Підключити</b>.", "На ТВ откройте <b>Настройки</b> → строку <b>Telegram-бот</b> → <b>Подключить</b>.", "On the TV open <b>Settings</b> → the <b>Telegram bot</b> row → <b>Link</b>."],
    tg1_2: ["На екрані з’явиться <b>QR-код</b> і <b>6-значний код</b> (дійсний 10 хвилин).", "На экране появится <b>QR-код</b> и <b>6-значный код</b> (действителен 10 минут).", "A <b>QR code</b> and a <b>6-digit code</b> appear (valid for 10 minutes)."],
    tg1_3: ["Наведіть камеру телефона на QR — відкриється Telegram з ботом <b>@promin_club_bot</b>. Натисніть <b>Start</b>.", "Наведите камеру телефона на QR — откроется Telegram с ботом <b>@promin_club_bot</b>. Нажмите <b>Start</b>.", "Point the phone camera at the QR — Telegram opens the <b>@promin_club_bot</b> chat. Press <b>Start</b>."],
    tg1_4: ["Без камери: відкрийте <a href=\"https://t.me/promin_club_bot\" target=\"_blank\" rel=\"noopener\">@promin_club_bot</a> і надішліть йому 6-значний код повідомленням.", "Без камеры: откройте <a href=\"https://t.me/promin_club_bot\" target=\"_blank\" rel=\"noopener\">@promin_club_bot</a> и отправьте ему 6-значный код сообщением.", "No camera? Open <a href=\"https://t.me/promin_club_bot\" target=\"_blank\" rel=\"noopener\">@promin_club_bot</a> and send the 6-digit code as a message."],
    tg1_5: ["ТВ сам закриє вікно й покаже «Telegram підключено». У боті з’явиться меню з кнопками.", "ТВ сам закроет окно и покажет «Telegram подключён». В боте появится меню с кнопками.", "The TV closes the sheet and shows “Telegram linked”. The bot displays its button menu."],
    tg1_shot: ["скріншот: QR і код підключення на ТВ", "скриншот: QR и код подключения на ТВ", "screenshot: QR and link code on the TV"],
    tg1_cap: ["Налаштування → Telegram-бот → Підключити", "Настройки → Telegram-бот → Подключить", "Settings → Telegram bot → Link"],
    tg2_h: ["Як користуватись ботом", "Как пользоваться ботом", "Using the bot"],
    tg2_lead: ["Після підключення внизу чату є постійна клавіатура — нічого не треба набирати руками.", "После подключения внизу чата есть постоянная клавиатура — ничего не нужно набирать руками.", "Once linked, a persistent keyboard sits under the chat — nothing has to be typed."],
    tg2_sum: ["Кнопки меню", "Кнопки меню", "Menu buttons"],
    k1: ["<b>🔍 Пошук</b> — або просто напишіть назву. П’ять результатів на сторінку, під кожним «Відкрити на ТВ» та ★ у закладки.", "<b>🔍 Поиск</b> — или просто напишите название. Пять результатов на страницу, под каждым «Открыть на ТВ» и ★ в закладки.", "<b>🔍 Search</b> — or just type a title. Five results per page, each with “Open on TV” and a ★ bookmark."],
    k2: ["<b>▶ Продовжити</b> — незавершені фільми й серії; ТВ продовжить з того самого місця.", "<b>▶ Продолжить</b> — незавершённые фильмы и серии; ТВ продолжит с того же места.", "<b>▶ Continue</b> — unfinished movies and episodes; the TV resumes from the same spot."],
    k3: ["<b>★ Закладки</b> та <b>🔥 Що подивитись</b> — ваше обране й полиці з головного екрана ТВ.", "<b>★ Закладки</b> и <b>🔥 Что посмотреть</b> — ваше избранное и полки с главного экрана ТВ.", "<b>★ Bookmarks</b> and <b>🔥 What to watch</b> — your bookmarks and the shelves from the TV home screen."],
    k4: ["<b>🎛 Пульт</b> — ⏪ ⏯ ⏩, попередня/наступна серія, звук, 🌙 нічний режим, 😴 таймер сну.", "<b>🎛 Пульт</b> — ⏪ ⏯ ⏩, предыдущая/следующая серия, звук, 🌙 ночной режим, 😴 таймер сна.", "<b>🎛 Remote</b> — ⏪ ⏯ ⏩, previous/next episode, mute, 🌙 night mode, 😴 sleep timer."],
    k5: ["<b>⚙ Налаштування</b> — мова (🇺🇦 🇷🇺 🇬🇧, синхронно з ТВ) і відключення.", "<b>⚙ Настройки</b> — язык (🇺🇦 🇷🇺 🇬🇧, синхронно с ТВ) и отключение.", "<b>⚙ Settings</b> — language (🇺🇦 🇷🇺 🇬🇧, in sync with the TV) and unlink."],
    tg2_note: ["Якщо ввімкнено кілька телевізорів, бот запитає, на якому відкрити. Вибір запам’ятовується на 5 хвилин.", "Если включено несколько телевизоров, бот спросит, на каком открыть. Выбор запоминается на 5 минут.", "With several TVs on, the bot asks which one to use. The choice is remembered for 5 minutes."],
    tg3_h: ["Mini App", "Mini App", "Mini App"],
    tg3_lead: ["У чаті з ботом є кнопка меню (ліворуч від поля вводу) — вона відкриває Mini App. Там усе те саме, що й на ТВ, але під пальці.", "В чате с ботом есть кнопка меню (слева от поля ввода) — она открывает Mini App. Там всё то же, что и на ТВ, но под пальцы.", "The chat has a menu button (left of the input) that opens the Mini App. Everything the TV has, laid out for a thumb."],
    tg3_sum: ["Що всередині", "Что внутри", "What is inside"],
    m1: ["<b>Головна</b> — телевізори онлайн і що на них грає зараз: смуга прогресу, доріжка, субтитри.", "<b>Главная</b> — телевизоры онлайн и что на них играет сейчас: полоса прогресса, дорожка, субтитры.", "<b>Home</b> — TVs online and what they play right now: progress bar, audio track, subtitles."],
    m2: ["<b>Пульт</b> — усі команди бота плюс перемотка по смузі, гучність, вибір озвучки й субтитрів.", "<b>Пульт</b> — все команды бота плюс перемотка по полосе, громкость, выбор озвучки и субтитров.", "<b>Remote</b> — every bot command plus seek by dragging, volume, voice and subtitle pickers."],
    m3: ["<b>Бібліотека</b> — продовжити, закладки, плейлисти, черга; будь-що відкривається на ТВ.", "<b>Библиотека</b> — продолжить, закладки, плейлисты, очередь; что угодно открывается на ТВ.", "<b>Library</b> — continue, bookmarks, playlists, queue; anything opens on the TV."],
    m4: ["<b>Налаштування</b> — усі налаштування ТВ (якість, рушій, субтитри, заставка, нічний режим…) і керування підключеними телефонами.", "<b>Настройки</b> — все настройки ТВ (качество, движок, субтитры, заставка, ночной режим…) и управление подключёнными телефонами.", "<b>Settings</b> — every TV setting (quality, engine, subtitles, screensaver, night mode…) and the linked phones."],
    tg3_shot: ["скріншот: Mini App — головна з пристроями", "скриншот: Mini App — главная с устройствами", "screenshot: Mini App — home with devices"],
    tg3_cap: ["Кнопка меню в чаті → Mini App", "Кнопка меню в чате → Mini App", "Chat menu button → Mini App"],
    tg4_h: ["Кілька телефонів на один профіль", "Несколько телефонов на один профиль", "Several phones on one profile"],
    tg4_lead: ["Телевізором користується вся родина — тож до профілю можна підключити скільки завгодно телефонів.", "Телевизором пользуется вся семья — поэтому к профилю можно подключить сколько угодно телефонов.", "The whole household shares the TV, so a profile can have any number of phones."],
    tg4_sum: ["Додати чи відключити телефон", "Добавить или отключить телефон", "Add or remove a phone"],
    a1: ["<b>З ТВ:</b> Налаштування → Telegram-бот → <b>Підключити ще один телефон</b> — новий QR і код.", "<b>С ТВ:</b> Настройки → Telegram-бот → <b>Подключить ещё один телефон</b> — новый QR и код.", "<b>From the TV:</b> Settings → Telegram bot → <b>Link another phone</b> — a fresh QR and code."],
    a2: ["<b>З Mini App:</b> Налаштування → Telegram → <b>Запросити ще один телефон</b> — відкриється шаринг Telegram, надішліть посилання рідним. Вони натискають Start — і підключені.", "<b>Из Mini App:</b> Настройки → Telegram → <b>Пригласить ещё один телефон</b> — откроется шаринг Telegram, отправьте ссылку близким. Они нажимают Start — и подключены.", "<b>From the Mini App:</b> Settings → Telegram → <b>Invite another phone</b> — Telegram’s share sheet opens; send the link to family. They press Start and are in."],
    a3: ["<b>Відключити:</b> у Mini App → Налаштування → Telegram видно всі телефони, кожен можна відключити окремо. На ТВ — <b>Відключити всі телефони</b>. У самому боті — команда <code>/unlink</code>.", "<b>Отключить:</b> в Mini App → Настройки → Telegram видны все телефоны, каждый можно отключить отдельно. На ТВ — <b>Отключить все телефоны</b>. В самом боте — команда <code>/unlink</code>.", "<b>Unlink:</b> Mini App → Settings → Telegram lists every phone and unlinks them one by one. On the TV — <b>Unlink all phones</b>. In the bot itself — <code>/unlink</code>."],
    tg4_note: ["Усі підключені телефони бачать ті самі пристрої, бібліотеку й мову. Приватність історії — на рівні профілю (PIN), не телефона.", "Все подключённые телефоны видят те же устройства, библиотеку и язык. Приватность истории — на уровне профиля (PIN), не телефона.", "All linked phones see the same devices, library and language. History privacy is per profile (PIN), not per phone."],
    faq_h: ["Поради та часті питання", "Советы и частые вопросы", "Tips and common questions"],
    faq_lead: ["Дрібниці, які роблять перегляд приємнішим, і відповіді на те, про що питають найчастіше.", "Мелочи, которые делают просмотр приятнее, и ответы на то, о чём спрашивают чаще всего.", "Small things that make watching nicer, and answers to what people ask most."],
    q1: ["Немає PIN-коду або я його забув", "Нет PIN-кода или я его забыл", "I have no PIN or forgot it"],
    q1a: ["PIN видає й змінює лише власник порталу в адмін-панелі. Напишіть йому — новий код працює одразу на всіх ваших ТВ.", "PIN выдаёт и меняет только владелец портала в админ-панели. Напишите ему — новый код работает сразу на всех ваших ТВ.", "Only the portal owner issues or changes a PIN in the admin panel. Ask them — a new code works right away on all your TVs."],
    q2: ["Кілька телевізорів і чужі пристрої", "Несколько телевизоров и чужие устройства", "Several TVs and other people’s devices"],
    q2a: ["Один PIN можна вводити на будь-якій кількості ТВ — історія й закладки спільні. Список сеансів: <b>Налаштування → Пристрої</b>. Кнопка <b>Вийти на всіх інших пристроях</b> завершує всі сеанси, крім поточного, — корисно, якщо код потрапив не в ті руки.", "Один PIN можно вводить на любом количестве ТВ — история и закладки общие. Список сеансов: <b>Настройки → Устройства</b>. Кнопка <b>Выйти на всех других устройствах</b> завершает все сеансы, кроме текущего, — полезно, если код попал не в те руки.", "One PIN works on any number of TVs — history and bookmarks are shared. Sessions are listed under <b>Settings → Devices</b>. <b>Sign out on all other devices</b> ends every session but the current one — handy if the code leaked."],
    q3: ["Відео не запускається або гальмує", "Видео не запускается или тормозит", "Video will not start or stutters"],
    q3a: ["У списку джерел показані лише ті, що знайшли цей тайтл, — спробуйте інше джерело або нижчу якість у меню плеєра. Якість за замовчуванням і <b>рушій відтворення</b> (auto / hls.js / native) — у <b>Налаштуваннях</b>: на старих ТВ інколи допомагає перемкнути рушій. Торренти запускаються за кілька секунд — плеєр покаже прогрес.", "В списке источников показаны только те, что нашли этот тайтл, — попробуйте другой источник или ниже качество в меню плеера. Качество по умолчанию и <b>движок воспроизведения</b> (auto / hls.js / native) — в <b>Настройках</b>: на старых ТВ иногда помогает переключить движок. Торренты запускаются за несколько секунд — плеер покажет прогресс.", "The source list only shows sources that found this title — try another one or a lower quality in the player menu. Default quality and the <b>player engine</b> (auto / hls.js / native) live in <b>Settings</b>; on old TVs switching the engine sometimes helps. Torrents take a few seconds to start — the player shows progress."],
    q4: ["Субтитри", "Субтитры", "Subtitles"],
    q4a: ["У меню плеєра → <b>Субтитри</b> є доріжки з джерела та <b>Зовнішні субтитри</b> (пошук по базі OpenSubtitles будь-якою мовою). Якщо субтитри спізнюються — <b>Зсув субтитрів</b> ±2 с. Розмір — у Налаштуваннях. На зовнішні субтитри діє добовий ліміт завантажень: побачили «ліміт вичерпано» — спробуйте завтра, уже завантажені лишаються.", "В меню плеера → <b>Субтитры</b> есть дорожки из источника и <b>Внешние субтитры</b> (поиск по базе OpenSubtitles на любом языке). Если субтитры опаздывают — <b>Сдвиг субтитров</b> ±2 с. Размер — в Настройках. На внешние субтитры действует суточный лимит загрузок: увидели «лимит исчерпан» — попробуйте завтра, уже загруженные остаются.", "Player menu → <b>Subtitles</b> lists the source’s tracks and <b>External subtitles</b> (an OpenSubtitles search in any language). If they lag, use <b>Subtitle offset</b> ±2 s. Size is in Settings. External subtitles have a daily download limit: on “limit reached” try tomorrow — already downloaded ones stay."],
    q5: ["Нічний режим і таймер сну", "Ночной режим и таймер сна", "Night mode and sleep timer"],
    q5a: ["<b>Нічний режим</b> (Налаштування, або 🌙 у боті) затемнює весь застосунок на 50–90 % — щоб екран не світив у темній кімнаті. <b>Таймер сну</b> в меню плеєра: 15–90 хвилин або «після цієї серії» — звук плавно стихне, відтворення стане на паузу.", "<b>Ночной режим</b> (Настройки, или 🌙 в боте) затемняет всё приложение на 50–90 % — чтобы экран не светил в тёмной комнате. <b>Таймер сна</b> в меню плеера: 15–90 минут или «после этой серии» — звук плавно стихнет, воспроизведение встанет на паузу.", "<b>Night mode</b> (Settings, or 🌙 in the bot) dims the whole app by 50–90 % so the screen does not glare in a dark room. <b>Sleep timer</b> in the player menu: 15–90 minutes or “after this episode” — the sound fades and playback pauses."],
    q6: ["Черга перегляду", "Очередь просмотра", "Watch queue"],
    q6a: ["На сторінці тайтлу або серії — <b>＋ У чергу</b>. Коли фільм закінчиться і наступної серії немає, плеєр запустить перший елемент черги. Черга спільна для ТВ, бота й Mini App (Бібліотека → Черга).", "На странице тайтла или серии — <b>＋ В очередь</b>. Когда фильм закончится и следующей серии нет, плеер запустит первый элемент очереди. Очередь общая для ТВ, бота и Mini App (Библиотека → Очередь).", "On a title or episode page press <b>＋ Queue</b>. When a film ends and there is no next episode, the player starts the first queued item. The queue is shared by the TV, the bot and the Mini App (Library → Queue)."],
    q7: ["Заставка з погодою", "Заставка с погодой", "Screensaver with weather"],
    q7a: ["Через кілька хвилин без дій ТВ показує заставку: кадри з каталогу, годинник і погоду для вашого міста. Час до запуску (3/5/10 хв або вимкнено) — у <b>Налаштуваннях → Заставка</b>.", "Через несколько минут без действий ТВ показывает заставку: кадры из каталога, часы и погоду для вашего города. Время до запуска (3/5/10 мин или выключено) — в <b>Настройках → Заставка</b>.", "After a few idle minutes the TV shows a screensaver: catalog backdrops, a clock and the weather for your city. The delay (3/5/10 min or off) is in <b>Settings → Screensaver</b>."],
    q8: ["Очистити історію або видалити дані", "Очистить историю или удалить данные", "Clear history or delete data"],
    q8a: ["<b>Налаштування → Небезпечна зона</b>. <b>Очистити історію перегляду</b> прибирає позиції «продовжити», закладки й плейлисти лишаються. <b>Видалити всі дані</b> стирає все в профілі та розлогінює всі пристрої — PIN при цьому залишається робочим.", "<b>Настройки → Опасная зона</b>. <b>Очистить историю просмотра</b> убирает позиции «продолжить», закладки и плейлисты остаются. <b>Удалить все данные</b> стирает всё в профиле и разлогинивает все устройства — PIN при этом остаётся рабочим.", "<b>Settings → Danger zone</b>. <b>Clear watch history</b> removes “continue” positions; bookmarks and playlists stay. <b>Delete all data</b> wipes the profile and signs out every device — the PIN keeps working."],
    q9: ["Старий Samsung не відкриває promin.club", "Старый Samsung не открывает promin.club", "An old Samsung cannot open promin.club"],
    q9a: ["Деякі Tizen 2016–2017 років не дружать з HTTP/2. Для них є окрема адреса — вкажіть у MSX <code>h1.promin.club</code>. Усе інше працює так само.", "Некоторые Tizen 2016–2017 годов не дружат с HTTP/2. Для них есть отдельный адрес — укажите в MSX <code>h1.promin.club</code>. Всё остальное работает так же.", "Some 2016–2017 Tizen sets struggle with HTTP/2. There is a dedicated address for them — enter <code>h1.promin.club</code> in MSX. Everything else works the same."],
    q10: ["Без телевізора — у браузері чи на телефоні", "Без телевизора — в браузере или на телефоне", "No TV — in a browser or on a phone"],
    q10a: ["Promin відкривається й у звичайному браузері за адресою <a href=\"https://promin.club\">promin.club</a>: той самий PIN, той самий акаунт. Керування — клавіатурою або дотиком.", "Promin открывается и в обычном браузере по адресу <a href=\"https://promin.club\">promin.club</a>: тот же PIN, тот же аккаунт. Управление — клавиатурой или касанием.", "Promin also opens in a regular browser at <a href=\"https://promin.club\">promin.club</a>: same PIN, same account. Use the keyboard or touch."],
    footer: ["Потрібен PIN-код? Зверніться до власника порталу.", "Нужен PIN-код? Обратитесь к владельцу портала.", "Need a PIN? Ask the portal owner."]
  };
  var LANGS = ['uk', 'ru', 'en'];
  var lang = (function () {
    var q = new URLSearchParams(location.search).get('lang');
    if (LANGS.indexOf(q) >= 0) return q;
    try { var s = localStorage.getItem('promin_lang'); if (LANGS.indexOf(s) >= 0) return s; } catch (e) {}
    var n = (navigator.language || '').slice(0, 2).toLowerCase();
    return LANGS.indexOf(n) >= 0 ? n : 'uk';
  })();
  function tr(key) { var v = T[key]; return v ? v[LANGS.indexOf(lang)] : key; }
  function applyLang() {
    document.documentElement.lang = lang;
    document.title = tr('title');
    document.querySelectorAll('[data-t]').forEach(function (el) { el.innerHTML = tr(el.getAttribute('data-t')); });
    document.querySelectorAll('[data-t-alt]').forEach(function (el) { el.alt = tr(el.getAttribute('data-t-alt')).replace(/<[^>]*>/g, ''); });
    document.querySelectorAll('#lang button').forEach(function (b) { b.classList.toggle('on', b.getAttribute('data-lang') === lang); });
    try { localStorage.setItem('promin_lang', lang); } catch (e) {}
  }
  document.querySelectorAll('#lang button').forEach(function (b) {
    b.addEventListener('click', function () { lang = b.getAttribute('data-lang'); applyLang(); });
  });
  applyLang();

  // top-level sections; #hash deep-links (#telegram, #faq, or any anchor inside a section)
  var SECS = ['install', 'telegram', 'faq'];
  function showSec(key, scrollTo) {
    document.querySelectorAll('#top button').forEach(function (b) { b.classList.toggle('on', b.getAttribute('data-sec') === key); });
    document.querySelectorAll('.sec').forEach(function (s) { s.classList.toggle('on', s.getAttribute('data-sec') === key); });
    if (scrollTo) { var el = document.getElementById(scrollTo); if (el) el.scrollIntoView(); }
  }
  document.querySelectorAll('#top button').forEach(function (b) {
    b.addEventListener('click', function () {
      var key = b.getAttribute('data-sec');
      history.replaceState(null, '', '#' + key);
      showSec(key);
      window.scrollTo({ top: document.getElementById('top').offsetTop, behavior: 'smooth' });
    });
  });
  function fromHash() {
    var h = location.hash.slice(1);
    if (!h) return;
    if (SECS.indexOf(h) >= 0) { showSec(h); return; }
    var el = document.getElementById(h);
    var sec = el && el.closest('.sec');
    if (sec) showSec(sec.getAttribute('data-sec'), h);
  }
  window.addEventListener('hashchange', fromHash);
  fromHash();

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
      var done = function () { btn.textContent = tr('copied'); btn.classList.add('done');
        setTimeout(function () { btn.textContent = tr('copy'); btn.classList.remove('done'); }, 1600); };
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
