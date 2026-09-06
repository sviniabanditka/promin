// Build pipeline: esbuild bundles src/app.ts into a single IIFE (ES2017),
// then @swc/core transpiles the whole bundle to ES5 in one pass (see
// docs/frontend.md, section 1). Output goes straight to
// server/webdist/, which the Go backend embeds.
//
// This script only ever writes its own known artifacts (app.js,
// styles.css, index.html, msx/start.json) — it never wipes the output
// directory, so other files placed there (embed.go, .gitkeep) survive.

const fs = require('fs');
const path = require('path');
const esbuild = require('esbuild');
const swc = require('@swc/core');

const WEB_DIR = path.join(__dirname, '..');
const OUT_DIR = path.join(WEB_DIR, '..', 'server', 'webdist');

// inlineCssVars compiles a single top :root { --name: value } token block into
// static literals: every var(--name) is replaced by its value and the :root
// block is stripped. Keeps the shipped CSS free of CSS custom properties for
// old Tizen (Chromium ~47). Only the flat, no-fallback var(--x) form is used;
// a leftover var( in the output is a hard error so a typo can't ship broken CSS.
function inlineCssVars(css) {
  // Strip CSS comments first: they're noise in the shipped file and, more
  // importantly, the header comment literally mentions var()/CSS variables,
  // which would trip the "no var() survives" assert below.
  css = css.replace(/\/\*[\s\S]*?\*\//g, '');
  const rootMatch = css.match(/:root\s*\{([^}]*)\}/);
  const vars = {};
  if (rootMatch) {
    const body = rootMatch[1];
    const re = /--([\w-]+)\s*:\s*([^;]+);/g;
    let m;
    while ((m = re.exec(body)) !== null) vars[m[1].trim()] = m[2].trim();
    css = css.replace(rootMatch[0], '');
  }
  // Resolve vars that themselves reference other vars (one pass is enough for
  // our shallow token graph; assert no var() survives afterward).
  css = css.replace(/var\(\s*--([\w-]+)\s*\)/g, function (_, name) {
    let v = vars[name];
    if (v == null) throw new Error('inlineCssVars: unknown --' + name);
    v = v.replace(/var\(\s*--([\w-]+)\s*\)/g, function (_2, n2) {
      if (vars[n2] == null) throw new Error('inlineCssVars: unknown --' + n2);
      return vars[n2];
    });
    return v;
  });
  if (/var\(/.test(css)) throw new Error('inlineCssVars: unresolved var() remains');
  return css;
}

function main() {
  fs.mkdirSync(OUT_DIR, { recursive: true });
  fs.mkdirSync(path.join(OUT_DIR, 'msx'), { recursive: true });
  fs.mkdirSync(path.join(OUT_DIR, 'vendor'), { recursive: true });

  // 1) esbuild: bundle + tree-shake + minify -> single IIFE, ES2017 target.
  const bundled = esbuild.buildSync({
    entryPoints: [path.join(WEB_DIR, 'src', 'app.ts')],
    bundle: true,
    format: 'iife',
    target: 'es2017',
    minify: true,
    // Keep UTF-8 literals as-is instead of \u-escaping every Cyrillic char —
    // ~5000 i18n keys were ascii-escaped, bloating the bundle ~20KB.
    charset: 'utf8',
    write: false,
    logLevel: 'warning',
  });
  const preCode = bundled.outputFiles[0].text;

  // 2) swc: transpile the whole bundle to ES5 in a single pass.
  const swcConfig = JSON.parse(fs.readFileSync(path.join(WEB_DIR, '.swcrc'), 'utf8'));
  let { code } = swc.transformSync(preCode, swcConfig);

  const crypto = require('crypto');
  const zlib = require('zlib');
  const hashOf = (buf) => crypto.createHash('sha1').update(buf).digest('hex').slice(0, 10);
  // Precompressed sibling: the Go static handler serves name.gz when the client
  // accepts gzip. The origin had no compression at all, and the h1 path for old
  // Samsung (:8444) is not behind Cloudflare — 333 KB went over Wi-Fi raw.
  const writeWithGz = (p, buf) => {
    fs.writeFileSync(p, buf);
    fs.writeFileSync(p + '.gz', zlib.gzipSync(buf, { level: 9 }));
  };

  // hls.js gets a content hash in its URL too (it was served max-age=86400 with
  // no version → a TV re-downloaded 297 KB daily and could run a stale copy for
  // a day after an upgrade). The bundle references it by literal path.
  const hlsSrc = fs.readFileSync(path.join(WEB_DIR, 'vendor', 'hls.min.js'));
  const hlsV = hashOf(hlsSrc);
  if (code.indexOf('/vendor/hls.min.js') === -1) throw new Error('build: hls.min.js path literal not found in bundle');
  code = code.split('/vendor/hls.min.js').join('/vendor/hls.min.js?v=' + hlsV);

  const appJsPath = path.join(OUT_DIR, 'app.js');
  writeWithGz(appJsPath, Buffer.from(code, 'utf8'));

  // 3) static assets. index.html gets cache-busting ?v=<hash> on app.js and
  // styles.css: they are served with a long max-age, and TV webviews cache
  // hard — without the version token a deploy is invisible for up to a day.
  // styles.css: inline CSS custom properties to static values. Old Tizen
  // webviews (Chromium ~47) predate CSS variables (Chrome 49+), so `var(--x)`
  // silently fails there and the whole palette drops out. Source uses a :root
  // token block for maintainability; this compiles it away so the shipped CSS
  // is plain literals. Dev (modern browser serving src) resolves var() natively.
  let css = inlineCssVars(fs.readFileSync(path.join(WEB_DIR, 'src', 'styles.css'), 'utf8'));
  // Constraint guard: Chromium ~47 (TV) has no grid/flex-gap/sticky/blur/backdrop —
  // fail the build if any slipped into the shipped CSS.
  const forbidden = /(display\s*:\s*grid|[^-]gap\s*:|position\s*:\s*sticky|backdrop-filter\s*:|filter\s*:\s*blur)/;
  if (forbidden.test(css)) throw new Error('styles.css: forbidden Chromium-47-illegal property (grid/gap/sticky/blur/backdrop-filter)');
  // Minify whitespace (source is unminified; only var() inlining ran before).
  css = esbuild.transformSync(css, { loader: 'css', minifyWhitespace: true }).code;
  writeWithGz(path.join(OUT_DIR, 'styles.css'), Buffer.from(css, 'utf8'));

  const appV = hashOf(fs.readFileSync(appJsPath));
  const cssV = hashOf(Buffer.from(css, 'utf8'));
  const html = fs
    .readFileSync(path.join(WEB_DIR, 'index.html'), 'utf8')
    .split('href="styles.css"').join('href="styles.css?v=' + cssV + '"')
    .split('"app.js"').join('"app.js?v=' + appV + '"'); // both the preload href and the script src
  fs.writeFileSync(path.join(OUT_DIR, 'index.html'), html);

  fs.copyFileSync(path.join(WEB_DIR, 'msx', 'start.json'), path.join(OUT_DIR, 'msx', 'start.json'));

  // hls.js is NOT bundled into app.js — it's lazy-loaded as a separate
  // <script> only on engines that need it (webOS/Android TV/desktop; Tizen
  // uses native HLS). We ship the "light" build (no alt-audio/subtitle/EME
  // modules — the backend handles demux, subs and voices out-of-band), which
  // passes `es-check es5`. See docs/frontend.md Served at
  // /vendor/hls.min.js by the Go static handler (embeds all of webdist).
  writeWithGz(path.join(OUT_DIR, 'vendor', 'hls.min.js'), hlsSrc);

  const sizeKb = (fs.statSync(appJsPath).size / 1024).toFixed(1);
  console.log('built ' + path.relative(WEB_DIR, appJsPath) + ' (' + sizeKb + ' KB)');
}

main();
