package httpapi

// adminHTML is the self-contained phone-first admin panel (modern mobile Chrome,
// no ES5 constraint). Login form → three tabs: profiles (CRUD, per-profile
// feature access, devices, Telegram chats), live TV (maintenance + manual EPG
// mapping), status. All state via fetch to /admin/* (cookie session).
const adminHTML = `<!doctype html>
<html lang="uk">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>Promin · Адмін</title>
<link rel="icon" href="/logo.svg" type="image/svg+xml">
<style>
  :root { color-scheme: dark; --bg:#0a0c10; --panel:#141821; --line:#232a36; --ink:#f3f5f9; --muted:#98a2b3; --accent:#7ca8ff; --danger:#ff6b6b; --good:#57c66a; --warn:#f0b35a; }
  * { box-sizing:border-box; -webkit-tap-highlight-color:transparent; }
  body { margin:0; background:var(--bg); color:var(--ink); font:16px/1.5 -apple-system,system-ui,Roboto,sans-serif; }
  .wrap { max-width:600px; margin:0 auto; padding:16px 16px calc(20px + env(safe-area-inset-bottom)); }
  h1 { font-size:22px; margin:8px 0 16px; }
  h2 { font-size:15px; color:var(--muted); font-weight:600; margin:18px 0 8px; text-transform:uppercase; letter-spacing:.04em; }
  .card { background:var(--panel); border:1px solid var(--line); border-radius:14px; padding:14px; margin-bottom:12px; }
  label { display:block; font-size:13px; color:var(--muted); margin:0 0 6px; }
  input, select { width:100%; padding:11px 13px; font-size:16px; background:#0e121a; border:1px solid var(--line); border-radius:10px; color:var(--ink); }
  input:focus, select:focus { outline:none; border-color:var(--accent); }
  button { font:inherit; font-weight:600; border:none; border-radius:10px; padding:11px 14px; cursor:pointer; }
  .btn { background:var(--accent); color:#0a0c10; width:100%; }
  .btn.sec { background:#232a36; color:var(--ink); }
  .btn.dngr { background:transparent; color:var(--danger); border:1px solid var(--danger); }
  .btn.sm { width:auto; padding:7px 11px; font-size:14px; }
  .btn:disabled { opacity:.5; }
  .row { display:flex; align-items:center; justify-content:space-between; gap:10px; padding:12px 0; border-bottom:1px solid var(--line); }
  .row:last-child { border-bottom:none; }
  .row.tap { cursor:pointer; }
  .who { min-width:0; flex:1; }
  .who b { display:block; font-size:17px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .badges { display:flex; flex-wrap:wrap; gap:6px; margin-top:3px; }
  .badge { font-size:12px; padding:1px 8px; border-radius:999px; border:1px solid var(--line); color:var(--muted); white-space:nowrap; }
  .badge.on { color:var(--good); border-color:var(--good); }
  .badge.off { color:var(--danger); border-color:var(--danger); }
  .badge.warn { color:var(--warn); border-color:var(--warn); }
  .badge.admin { color:var(--accent); border-color:var(--accent); }
  .acts { display:flex; gap:8px; flex-shrink:0; }
  .err { color:var(--danger); font-size:14px; min-height:20px; margin-top:8px; }
  .muted { color:var(--muted); font-size:14px; }
  .hidden { display:none !important; }
  .top { display:flex; align-items:center; justify-content:space-between; margin-bottom:12px; }
  .tabs { display:flex; gap:6px; margin-bottom:14px; }
  .tabs button { flex:1; background:#0e121a; color:var(--muted); border:1px solid var(--line); }
  .tabs button.on { background:#232a36; color:var(--ink); border-color:#2f3847; }
  .sw { display:flex; align-items:center; justify-content:space-between; padding:10px 0; border-bottom:1px solid var(--line); }
  .sw:last-child { border-bottom:none; }
  .sw input { width:auto; accent-color:var(--accent); transform:scale(1.4); margin-right:6px; }
  .chips { display:flex; flex-wrap:wrap; gap:8px; margin-top:8px; }
  .chip { padding:6px 12px; border-radius:999px; border:1px solid var(--line); color:var(--muted); font-size:14px; cursor:pointer; background:transparent; }
  .chip.on { color:var(--ink); border-color:var(--accent); background:rgba(124,168,255,.14); }
  .detail { margin-top:6px; padding:12px; background:#0e121a; border-radius:12px; border:1px solid var(--line); }
  .grid2 { display:grid; grid-template-columns:1fr 1fr; gap:8px; }
  .kv { display:flex; justify-content:space-between; gap:12px; padding:6px 0; border-bottom:1px solid var(--line); font-size:15px; }
  .kv:last-child { border-bottom:none; }
  .kv span:last-child { color:var(--muted); text-align:right; }
  .list { max-height:60vh; overflow:auto; }
  .hint { font-size:13px; color:var(--muted); margin-top:6px; }
</style>
</head>
<body>
<div class="wrap">
  <div id="loginView" class="hidden">
    <h1><img src="/logo.svg" alt="" style="width:36px;height:36px;vertical-align:-8px;margin-right:10px">Promin · Адмін</h1>
    <div class="card">
      <label>Пароль адміна</label>
      <input id="pw" type="password" autocomplete="current-password" inputmode="text">
      <div class="err" id="loginErr"></div>
      <button class="btn" id="loginBtn" style="margin-top:12px">Увійти</button>
    </div>
  </div>

  <div id="panelView" class="hidden">
    <div class="top"><h1 style="margin:0">Promin · Адмін</h1><button class="btn sec sm" id="logoutBtn">Вийти</button></div>
    <div class="tabs"><button data-tab="profiles" class="on">Профілі</button><button data-tab="tv">ТВ</button><button data-tab="status">Стан</button></div>

    <div id="tab-profiles">
      <div class="card">
        <label>Нове ім'я</label>
        <input id="newLogin" placeholder="Ім'я">
        <label style="margin-top:12px">PIN (6 цифр, можна пізніше)</label>
        <input id="newPin" inputmode="numeric" pattern="[0-9]*" maxlength="6" placeholder="______">
        <div class="err" id="createErr"></div>
        <button class="btn" id="createBtn" style="margin-top:12px">Створити профіль</button>
      </div>
      <div class="card" id="roster"></div>
    </div>

    <div id="tab-tv" class="hidden">
      <div class="card">
        <div class="grid2">
          <button class="btn sec sm" data-resync="catalogue">Оновити каталог</button>
          <button class="btn sec sm" data-resync="check">Перевірити потоки</button>
          <button class="btn sec sm" data-resync="epg">Оновити програму</button>
          <button class="btn sec sm" id="tvStatsBtn">Оновити цифри</button>
        </div>
        <div class="hint" id="tvHint"></div>
      </div>
      <div class="card">
        <h2 style="margin-top:0">Телепрограма каналів</h2>
        <div class="hint" style="margin:0 0 10px">Наші канали і до якого каналу з телепрограми кожен прив'язаний. Натисніть канал, щоб вибрати іншу програму зі списку.</div>
        <input id="chQ" placeholder="Знайти наш канал (назва або id)">
        <div class="grid2" style="margin-top:8px">
          <select id="chCountry"><option value="">Усі країни</option></select>
          <label class="sw" style="padding:0;border:none;justify-content:flex-start;gap:8px;color:var(--ink);font-size:15px"><input type="checkbox" id="chNoEpg"> без програми</label>
        </div>
        <div class="list" id="chList" style="margin-top:8px"></div>
      </div>
      <div class="card hidden" id="mapCard">
        <div class="top"><b id="mapTitle"></b><button class="btn sec sm" id="mapClose">✕</button></div>
        <label>Зараз</label><div id="mapNow"></div>
        <label style="margin-top:12px">Програма з телепрограми (вибрати зі списку)</label>
        <input id="epgQ" placeholder="Почніть вводити назву каналу…">
        <div class="list" id="epgList" style="margin-top:8px"></div>
        <div class="grid2" style="margin-top:10px">
          <button class="btn sec sm" id="mapNone">Без програми</button>
          <button class="btn sec sm" id="mapAuto">Авто (як програма знайде сама)</button>
        </div>
        <div class="hint">Вибір застосовується одразу: у списку каналів і в плеєрі на ТВ.</div>
      </div>
    </div>

    <div id="tab-status" class="hidden">
      <div class="card" id="statusCard"></div>
    </div>
  </div>
</div>

<script>
const $ = s => document.querySelector(s);
const FEATS = [['online','Онлайн-джерела'],['torrents','Торренти'],['youtube','YouTube'],['tv','ТВ']];
let status = null, profiles = [], openId = null, mapCh = null;

async function api(method, path, body) {
  const r = await fetch(path, { method, headers: body ? {'Content-Type':'application/json'} : {}, body: body ? JSON.stringify(body) : undefined });
  let data = null; try { data = await r.json(); } catch (e) {}
  return { ok: r.ok, status: r.status, data };
}
function esc(s){ return String(s == null ? '' : s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c])); }
function show(id) { $('#loginView').classList.toggle('hidden', id!=='login'); $('#panelView').classList.toggle('hidden', id!=='panel'); }
function ago(ts){ if(!ts) return 'ніколи'; const d=Math.floor(Date.now()/1000-ts); if(d<60) return 'зараз'; if(d<3600) return Math.floor(d/60)+' хв'; if(d<86400) return Math.floor(d/3600)+' год'; return Math.floor(d/86400)+' дн'; }
function when(ts){ return ts ? new Date(ts*1000).toLocaleString('uk-UA',{hour:'2-digit',minute:'2-digit',day:'2-digit',month:'2-digit'}) : '—'; }
function errMsg(code){ return code==='login_taken'?"Таке ім'я вже є":code==='pin_taken'?'Такий PIN зайнятий':'Помилка'; }

// ---- tabs ----
document.querySelectorAll('.tabs button').forEach(b => b.onclick = () => {
  document.querySelectorAll('.tabs button').forEach(x => x.classList.toggle('on', x===b));
  ['profiles','tv','status'].forEach(t => $('#tab-'+t).classList.toggle('hidden', t!==b.dataset.tab));
  if (b.dataset.tab==='tv') { loadStatus(); loadChannels(); }
  if (b.dataset.tab==='status') loadStatus(true);
});

// ---- login ----
$('#loginBtn').onclick = async () => {
  $('#loginErr').textContent = '';
  const r = await api('POST', '/admin/login', { password: $('#pw').value });
  if (r.ok) { $('#pw').value=''; refresh(); }
  else if (r.status === 429) $('#loginErr').textContent = 'Забагато спроб, зачекайте';
  else if (r.status === 503) $('#loginErr').textContent = 'Адмін не налаштований (PROMIN_ADMIN_PASSWORD)';
  else $('#loginErr').textContent = 'Невірний пароль';
};
$('#pw').addEventListener('keydown', e => { if (e.key === 'Enter') $('#loginBtn').click(); });
$('#logoutBtn').onclick = async () => { await api('POST', '/admin/logout'); show('login'); };

// ---- profiles ----
async function refresh() {
  const r = await api('GET', '/admin/profiles');
  if (r.status === 401) { show('login'); return; }
  if (!r.ok) return;
  show('panel');
  if (!status) await loadStatus();
  profiles = (r.data && r.data.profiles) || [];
  renderRoster();
}
function featBadges(p){
  if (p.is_admin) return '<span class="badge admin">адмін</span>';
  if (!p.restricted) return '<span class="badge on">повний доступ</span>';
  return FEATS.map(([k,n]) => '<span class="badge '+(p.features[k]?'on':'off')+'">'+n+'</span>').join('') +
    (p.features.tv && p.features.tv_countries.length ? '<span class="badge warn">'+p.features.tv_countries.join(', ')+'</span>' : '');
}
function renderRoster(){
  $('#roster').innerHTML = profiles.map(p => {
    const meta = '<span class="badge">'+(p.has_pin?'PIN є':'без PIN')+'</span><span class="badge">'+p.devices+' пристр.</span>' +
      (p.telegram ? '<span class="badge">TG '+p.telegram+'</span>' : '') + '<span class="badge">'+ago(p.last_seen)+'</span>';
    return '<div class="row tap" data-open="'+p.id+'"><div class="who"><b>'+esc(p.login)+'</b><div class="badges">'+meta+'</div><div class="badges">'+featBadges(p)+'</div></div><div class="muted">'+(openId===p.id?'▲':'▼')+'</div></div>' +
      (openId===p.id ? '<div class="detail" id="detail-'+p.id+'">'+detailHTML(p)+'</div>' : '');
  }).join('') || '<div class="muted">Ще немає профілів</div>';
  if (openId!=null) loadDetail(openId);
}
function detailHTML(p){
  let h = '<div class="acts" style="margin-bottom:10px"><button class="btn sec sm" data-pin="'+p.id+'">PIN</button><button class="btn sec sm" data-ren="'+p.id+'" data-name="'+esc(p.login)+'">Перейменувати</button>'+(p.is_admin?'':'<button class="btn dngr sm" data-del="'+p.id+'">Видалити</button>')+'</div>';
  if (!p.is_admin) {
    h += '<h2>Доступ</h2>' + FEATS.map(([k,n]) => '<label class="sw"><span>'+n+'</span><input type="checkbox" data-feat="'+k+'" data-id="'+p.id+'" '+(p.features[k]?'checked':'')+'></label>').join('');
    const cs = (status && status.countries) || [];
    if (cs.length) h += '<label style="margin-top:10px">Країни ТВ (нічого не вибрано = усі)</label><div class="chips">' + cs.map(c => '<button class="chip '+(p.features.tv_countries.indexOf(c)>=0?'on':'')+'" data-cc="'+c+'" data-id="'+p.id+'">'+c+'</button>').join('') + '</div>';
  }
  h += '<h2>Пристрої'+(p.is_admin?'':' <button class="btn dngr sm" data-revokeall="'+p.id+'" style="margin-left:8px">Вийти з усіх</button>')+'</h2><div id="devs-'+p.id+'" class="muted list">…</div>';
  h += '<h2>Telegram</h2><div id="tg-'+p.id+'" class="muted">…</div>';
  return h;
}
async function loadDetail(id){
  const d = await api('GET', '/admin/profiles/'+id+'/devices');
  const el = $('#devs-'+id);
  if (el) el.innerHTML = ((d.data&&d.data.devices)||[]).map(x => '<div class="row"><div class="who"><b style="font-size:15px">'+esc(x.device_name||x.device_type)+'</b><div class="muted">'+esc(x.device_type)+' · активний '+ago(x.last_seen)+' · з '+when(x.created_at)+'</div></div><button class="btn dngr sm" data-revoke="'+esc(x.token_id)+'" data-id="'+id+'">✕</button></div>').join('') || '<div class="muted">немає активних сесій</div>';
  const t = await api('GET', '/admin/profiles/'+id+'/telegram');
  const te = $('#tg-'+id);
  if (te) te.innerHTML = ((t.data&&t.data.links)||[]).map(x => '<div class="row"><div class="who"><b style="font-size:15px">'+esc(x.first_name||('chat '+x.chat_id))+'</b><div class="muted">'+(x.username?'@'+esc(x.username)+' · ':'')+'з '+when(x.created_at)+'</div></div><button class="btn dngr sm" data-unlink="'+x.chat_id+'" data-id="'+id+'">✕</button></div>').join('') || '<div class="muted">чатів не прив\'язано</div>';
}
async function saveFeatures(id){
  const p = profiles.find(x => x.id===id); if (!p) return;
  const f = { online:false, torrents:false, youtube:false, tv:false, tv_countries:[] };
  document.querySelectorAll('#detail-'+id+' [data-feat]').forEach(i => f[i.dataset.feat] = i.checked);
  document.querySelectorAll('#detail-'+id+' .chip.on').forEach(c => f.tv_countries.push(c.dataset.cc));
  const r = await api('PATCH', '/admin/profiles/'+id, { features: f });
  if (!r.ok) alert('Не збереглося');
  const list = await api('GET', '/admin/profiles'); profiles = (list.data&&list.data.profiles)||profiles;
  const row = document.querySelector('[data-open="'+id+'"] .badges:last-child'); const np = profiles.find(x=>x.id===id);
  if (row && np) row.innerHTML = featBadges(np);
}

$('#createBtn').onclick = async () => {
  $('#createErr').textContent = '';
  const login = $('#newLogin').value.trim(), pin = $('#newPin').value.trim();
  if (!login) { $('#createErr').textContent = "Введіть ім'я"; return; }
  if (pin && !/^[0-9]{6}$/.test(pin)) { $('#createErr').textContent = 'PIN — рівно 6 цифр'; return; }
  const r = await api('POST', '/admin/profiles', { login, pin });
  if (r.ok) { $('#newLogin').value=''; $('#newPin').value=''; refresh(); }
  else if (r.data && r.data.error) $('#createErr').textContent = errMsg(r.data.error.code);
  else $('#createErr').textContent = 'Помилка';
};

$('#roster').addEventListener('change', e => { const t = e.target; if (t.dataset.feat) saveFeatures(Number(t.dataset.id)); });
$('#roster').addEventListener('click', async e => {
  const t = e.target.closest('[data-open],[data-del],[data-ren],[data-pin],[data-revoke],[data-revokeall],[data-unlink],[data-cc]');
  if (!t) return;
  if (t.dataset.cc) { t.classList.toggle('on'); saveFeatures(Number(t.dataset.id)); return; }
  if (t.dataset.open) { const id = Number(t.dataset.open); openId = openId===id ? null : id; renderRoster(); return; }
  if (t.dataset.del) {
    if (!confirm('Видалити профіль? Дані профілю буде стерто.')) return;
    await api('DELETE', '/admin/profiles/' + t.dataset.del); openId = null; refresh();
  } else if (t.dataset.ren) {
    const name = prompt("Нове ім'я", t.dataset.name || ''); if (name==null) return;
    const r = await api('PATCH', '/admin/profiles/' + t.dataset.ren, { login: name.trim() });
    if (!r.ok && r.data && r.data.error) alert(errMsg(r.data.error.code)); refresh();
  } else if (t.dataset.pin) {
    const pin = prompt('Новий PIN (6 цифр). Порожньо = прибрати PIN.'); if (pin==null) return;
    if (pin && !/^[0-9]{6}$/.test(pin)) { alert('PIN — рівно 6 цифр'); return; }
    const r = await api('PATCH', '/admin/profiles/' + t.dataset.pin, { pin });
    if (!r.ok && r.data && r.data.error) alert(errMsg(r.data.error.code)); refresh();
  } else if (t.dataset.revokeall) {
    if (!confirm('Вийти з усіх пристроїв цього профілю?')) return;
    await api('DELETE', '/admin/profiles/'+t.dataset.revokeall+'/devices'); refresh();
  } else if (t.dataset.revoke) {
    if (!confirm('Вийти з цього пристрою?')) return;
    await api('DELETE', '/admin/profiles/'+t.dataset.id+'/devices/'+encodeURIComponent(t.dataset.revoke)); refresh();
  } else if (t.dataset.unlink) {
    if (!confirm('Відв\'язати цей Telegram-чат?')) return;
    await api('DELETE', '/admin/profiles/'+t.dataset.id+'/telegram/'+t.dataset.unlink); refresh();
  }
});

// ---- status ----
async function loadStatus(render){
  const r = await api('GET', '/admin/status');
  if (!r.ok) return;
  status = r.data;
  const sel = $('#chCountry');
  if (sel.options.length <= 1) (status.countries||[]).forEach(c => { const o = document.createElement('option'); o.value=c; o.textContent=c; sel.appendChild(o); });
  const s = status.tv_stats;
  if (s) $('#tvHint').textContent = 'Каналів з потоком '+s.alive+' з '+s.channels+', потоків '+s.streams+', з програмою '+s.with_epg+', передач '+s.programmes+'. Каталог '+when(s.synced_at)+', перевірка '+when(s.checked_at)+', програма '+when(s.epg_at)+'.';
  if (render) {
    const kv = (k,v) => '<div class="kv"><span>'+k+'</span><span>'+v+'</span></div>';
    $('#statusCard').innerHTML = kv('Версія', esc((status.version||'').slice(0,12))) + kv('YouTube', status.youtube?'увімкнено':'вимкнено') + kv('ТВ', status.tv?'увімкнено ('+(status.countries||[]).join(', ')+')':'вимкнено') +
      (s ? kv('Каналів (з потоком / усього)', s.alive+' / '+s.channels) + kv('Потоків', s.streams) + kv('Каналів з програмою', s.with_epg) + kv('Передач у базі', s.programmes) + kv('Каталог оновлено', when(s.synced_at)) + kv('Потоки перевірено', when(s.checked_at)) + kv('Програму оновлено', when(s.epg_at)) : '') +
      kv('Профілів', profiles.length) + '<div class="hint" style="margin-top:10px">Логи: <a href="/logs" style="color:var(--accent)">/logs</a></div>';
  }
}
document.querySelectorAll('[data-resync]').forEach(b => b.onclick = async () => {
  b.disabled = true; const r = await api('POST', '/admin/tv/resync', { kind: b.dataset.resync });
  $('#tvHint').textContent = r.ok ? (r.data.started ? 'Запущено у фоні. Оновіть цифри за хвилину.' : 'Вже виконується.') : 'Помилка';
  setTimeout(() => { b.disabled = false; }, 3000);
});
$('#tvStatsBtn').onclick = () => loadStatus();

// ---- live TV: channel ↔ guide mapping ----
let chTimer = 0;
function debounce(fn){ return () => { clearTimeout(chTimer); chTimer = setTimeout(fn, 250); }; }
async function loadChannels(){
  const q = $('#chQ').value.trim(), c = $('#chCountry').value, no = $('#chNoEpg').checked;
  const r = await api('GET', '/admin/tv/channels?q='+encodeURIComponent(q)+'&country='+encodeURIComponent(c)+(no?'&noepg=1':''));
  const items = (r.data && r.data.items) || [];
  $('#chList').innerHTML = items.map(ch => {
    const src = ch.epg_id ? ch.epg_id.split(':')[0] : '';
    const b = ch.override==='none' ? '<span class="badge off">без програми (вручну)</span>' : ch.epg_id ? '<span class="badge '+(ch.override?'warn':'on')+'">'+(ch.override?'вручну: ':'')+esc(ch.epg_name||ch.epg_id)+' <span style="opacity:.7">· '+esc(src)+'</span></span>' : '<span class="badge off">програму не знайдено</span>';
    return '<div class="row tap" data-ch="'+esc(ch.id)+'" data-name="'+esc(ch.name)+'" data-alt="'+esc((ch.alt_names||[])[0]||'')+'" data-country="'+esc(ch.country)+'"><div class="who"><b style="font-size:15px">'+esc(ch.name)+' <span class="muted">'+esc(ch.country)+(ch.alive?'':' · без потоку')+'</span></b><div class="badges">'+b+'</div></div><div class="muted">›</div></div>';
  }).join('') || '<div class="muted">нічого не знайдено</div>';
}
$('#chQ').addEventListener('input', debounce(loadChannels));
$('#chCountry').addEventListener('change', loadChannels);
$('#chNoEpg').addEventListener('change', loadChannels);
$('#chList').addEventListener('click', e => {
  const t = e.target.closest('[data-ch]'); if (!t) return;
  mapCh = { id: t.dataset.ch, name: t.dataset.name };
  $('#mapCard').classList.remove('hidden');
  $('#mapTitle').textContent = mapCh.name + ' (' + mapCh.id + ')';
  $('#mapNow').innerHTML = t.querySelector('.badges').innerHTML;
  // Native spelling (iptv-org alt_names) finds feed channels far more often than the English catalogue name.
  $('#epgQ').value = (t.dataset.alt || mapCh.name.replace(/\b(HD|TV|Ukraine|Ukraina|International)\b/gi,'')).trim();
  searchEpg();
  $('#mapCard').scrollIntoView({ behavior:'smooth' });
});
async function searchEpg(){
  const q = $('#epgQ').value.trim();
  if (!q) { $('#epgList').innerHTML = ''; return; }
  const r = await api('GET', '/admin/tv/epg/search?q='+encodeURIComponent(q));
  const items = (r.data && r.data.items) || [];
  $('#epgList').innerHTML = items.map(x => '<div class="row tap" data-src="'+esc(x.source)+'" data-xid="'+esc(x.xmltv_id)+'" data-label="'+esc(x.names[0]||x.xmltv_id)+'"><div class="who"><b style="font-size:15px">'+esc(x.names[0]||x.xmltv_id)+'</b><div class="muted">'+esc(x.source)+(x.names.length>1?' · '+esc(x.names.slice(1).join(', ')):'')+'</div></div><div class="muted">вибрати</div></div>').join('') || '<div class="muted">У телепрограмі такого каналу немає — спробуйте коротшу або іншу назву.</div>';
}
$('#epgQ').addEventListener('input', debounce(searchEpg));
$('#epgList').addEventListener('click', async e => {
  const t = e.target.closest('[data-xid]'); if (!t || !mapCh) return;
  const r = await api('PUT', '/admin/tv/channels/'+encodeURIComponent(mapCh.id)+'/epg', { source: t.dataset.src, xmltv_id: t.dataset.xid });
  if (r.ok) { $('#mapNow').innerHTML = '<span class="badge warn">вручну: '+esc(t.dataset.label)+' · '+esc(t.dataset.src)+'</span> ✓ застосовано'; loadChannels(); } else alert('Помилка');
});
$('#mapNone').onclick = async () => { if (!mapCh) return; const r = await api('PUT', '/admin/tv/channels/'+encodeURIComponent(mapCh.id)+'/epg', { source: '', xmltv_id: '' }); if (r.ok) { $('#mapNow').innerHTML = '<span class="badge off">без програми (вручну)</span>'; loadChannels(); } };
$('#mapAuto').onclick = async () => { if (!mapCh) return; const r = await api('DELETE', '/admin/tv/channels/'+encodeURIComponent(mapCh.id)+'/epg'); if (r.ok) { $('#mapNow').innerHTML = r.data && r.data.epg_id ? '<span class="badge on">авто: '+esc(r.data.epg_id)+'</span>' : '<span class="badge off">авто: програму не знайдено</span>'; loadChannels(); } };
$('#mapClose').onclick = () => { $('#mapCard').classList.add('hidden'); mapCh = null; };

refresh();
</script>
</body>
</html>`
