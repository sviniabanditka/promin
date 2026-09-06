// Telegram Mini App build (docs/miniapp.md): miniapp/src/main.tsx -> single
// IIFE, ES2020 (Telegram's webview is modern; no swc/ES5 pass). Output goes to
// server/webdist/tg/ (index.html, app.js, app.css + .gz siblings), embedded by
// the Go binary with the rest of webdist. Run alone via `npm run build:tg`;
// scripts/build.js calls main() after the TV build.

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const zlib = require('zlib');
const esbuild = require('esbuild');

const WEB_DIR = path.join(__dirname, '..');
const SRC_DIR = path.join(WEB_DIR, 'miniapp');
const OUT_DIR = path.join(WEB_DIR, '..', 'server', 'webdist', 'tg');

const hashOf = (buf) => crypto.createHash('sha1').update(buf).digest('hex').slice(0, 10);
const writeWithGz = (p, buf) => {
  fs.writeFileSync(p, buf);
  fs.writeFileSync(p + '.gz', zlib.gzipSync(buf, { level: 9 }));
};

function main() {
  fs.mkdirSync(OUT_DIR, { recursive: true });

  const js = esbuild.buildSync({
    entryPoints: [path.join(SRC_DIR, 'src', 'main.tsx')],
    bundle: true,
    format: 'iife',
    target: 'es2020',
    minify: true,
    charset: 'utf8',
    jsx: 'automatic',
    jsxImportSource: 'preact',
    write: false,
    logLevel: 'warning',
  }).outputFiles[0].text;

  const css = esbuild.transformSync(fs.readFileSync(path.join(SRC_DIR, 'app.css'), 'utf8'), { loader: 'css', minify: true }).code;

  const jsBuf = Buffer.from(js, 'utf8');
  const cssBuf = Buffer.from(css, 'utf8');
  writeWithGz(path.join(OUT_DIR, 'app.js'), jsBuf);
  writeWithGz(path.join(OUT_DIR, 'app.css'), cssBuf);

  const html = fs
    .readFileSync(path.join(SRC_DIR, 'index.html'), 'utf8')
    .split('"/tg/app.css"').join('"/tg/app.css?v=' + hashOf(cssBuf) + '"')
    .split('"/tg/app.js"').join('"/tg/app.js?v=' + hashOf(jsBuf) + '"');
  fs.writeFileSync(path.join(OUT_DIR, 'index.html'), html);

  console.log('built ' + path.relative(WEB_DIR, path.join(OUT_DIR, 'app.js')) + ' (' + (jsBuf.length / 1024).toFixed(1) + ' KB)');
}

if (require.main === module) main();
module.exports = { main };
