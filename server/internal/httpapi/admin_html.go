package httpapi

// adminHTML is the self-contained phone-first admin panel (modern mobile Chrome,
// no ES5 constraint). Login form → profile roster CRUD. All state via fetch to
// /admin/* (cookie session).
const adminHTML = `<!doctype html>
<html lang="uk">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>Promin · Адмін</title>
<link rel="icon" href="/logo.svg" type="image/svg+xml">
<style>
  :root { color-scheme: dark; --bg:#0a0c10; --panel:#141821; --line:#232a36; --ink:#f3f5f9; --muted:#98a2b3; --accent:#7ca8ff; --danger:#ff6b6b; --good:#57c66a; }
  * { box-sizing:border-box; -webkit-tap-highlight-color:transparent; }
  body { margin:0; background:var(--bg); color:var(--ink); font:16px/1.5 -apple-system,system-ui,Roboto,sans-serif; }
  .wrap { max-width:560px; margin:0 auto; padding:20px 16px calc(20px + env(safe-area-inset-bottom)); }
  h1 { font-size:22px; margin:8px 0 20px; }
  .card { background:var(--panel); border:1px solid var(--line); border-radius:14px; padding:16px; margin-bottom:14px; }
  label { display:block; font-size:13px; color:var(--muted); margin:0 0 6px; }
  input { width:100%; padding:12px 14px; font-size:17px; background:#0e121a; border:1px solid var(--line); border-radius:10px; color:var(--ink); }
  input:focus { outline:none; border-color:var(--accent); }
  button { font:inherit; font-weight:600; border:none; border-radius:10px; padding:12px 16px; cursor:pointer; }
  .btn { background:var(--accent); color:#0a0c10; width:100%; }
  .btn.sec { background:#232a36; color:var(--ink); }
  .btn.dngr { background:transparent; color:var(--danger); border:1px solid var(--danger); }
  .btn.sm { width:auto; padding:8px 12px; font-size:14px; }
  .row { display:flex; align-items:center; justify-content:space-between; gap:10px; padding:14px 0; border-bottom:1px solid var(--line); }
  .row:last-child { border-bottom:none; }
  .row .who { min-width:0; }
  .row .who b { display:block; font-size:17px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .badge { font-size:12px; padding:2px 8px; border-radius:999px; border:1px solid var(--line); color:var(--muted); }
  .badge.on { color:var(--good); border-color:var(--good); }
  .badge.admin { color:var(--accent); border-color:var(--accent); }
  .acts { display:flex; gap:8px; flex-shrink:0; }
  .err { color:var(--danger); font-size:14px; min-height:20px; margin-top:8px; }
  .muted { color:var(--muted); font-size:14px; }
  .hidden { display:none; }
  .top { display:flex; align-items:center; justify-content:space-between; margin-bottom:16px; }
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
    <div class="top"><h1 style="margin:0">Профілі</h1><button class="btn sec sm" id="logoutBtn">Вийти</button></div>
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
</div>

<script>
const $ = s => document.querySelector(s);
async function api(method, path, body) {
  const r = await fetch(path, { method, headers: body ? {'Content-Type':'application/json'} : {}, body: body ? JSON.stringify(body) : undefined });
  let data = null; try { data = await r.json(); } catch (e) {}
  return { ok: r.ok, status: r.status, data };
}
function show(id) { $('#loginView').classList.toggle('hidden', id!=='login'); $('#panelView').classList.toggle('hidden', id!=='panel'); }

async function refresh() {
  const r = await api('GET', '/admin/profiles');
  if (r.status === 401) { show('login'); return; }
  if (!r.ok) { return; }
  show('panel');
  const list = (r.data && r.data.profiles) || [];
  $('#roster').innerHTML = list.map(p => {
    const badges = (p.is_admin ? '<span class="badge admin">адмін</span> ' : '') +
      (p.has_pin ? '<span class="badge on">PIN є</span>' : '<span class="badge">без PIN</span>');
    const del = p.is_admin ? '' : '<button class="btn dngr sm" data-del="'+p.id+'">✕</button>';
    return '<div class="row"><div class="who"><b>'+esc(p.login)+'</b><div>'+badges+'</div></div>'+
      '<div class="acts"><button class="btn sec sm" data-pin="'+p.id+'">PIN</button>'+
      '<button class="btn sec sm" data-ren="'+p.id+'" data-name="'+esc(p.login)+'">✎</button>'+del+'</div></div>';
  }).join('') || '<div class="muted">Ще немає профілів</div>';
}
function esc(s){ return String(s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c])); }

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
function errMsg(code){ return code==='login_taken'?"Таке ім'я вже є":code==='pin_taken'?'Такий PIN зайнятий':'Помилка'; }

$('#roster').addEventListener('click', async e => {
  const t = e.target;
  if (t.dataset.del) {
    if (!confirm('Видалити профіль? Дані профілю буде стерто.')) return;
    await api('DELETE', '/admin/profiles/' + t.dataset.del); refresh();
  } else if (t.dataset.ren) {
    const name = prompt("Нове ім'я", t.dataset.name || ''); if (name==null) return;
    const r = await api('PATCH', '/admin/profiles/' + t.dataset.ren, { login: name.trim() });
    if (!r.ok && r.data && r.data.error) alert(errMsg(r.data.error.code)); refresh();
  } else if (t.dataset.pin) {
    const pin = prompt('Новий PIN (6 цифр). Порожньо = прибрати PIN.'); if (pin==null) return;
    if (pin && !/^[0-9]{6}$/.test(pin)) { alert('PIN — рівно 6 цифр'); return; }
    const r = await api('PATCH', '/admin/profiles/' + t.dataset.pin, { pin });
    if (!r.ok && r.data && r.data.error) alert(errMsg(r.data.error.code)); refresh();
  }
});

refresh();
</script>
</body>
</html>`
