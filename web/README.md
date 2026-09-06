# Promin web

Vanilla TypeScript frontend for TV webviews (D-pad first, ES5 output for
Chromium ~47), phones and desktop browsers.

```
npm install
npm run typecheck   # tsc --noEmit
npm run build       # esbuild -> swc (ES5) -> ../server/webdist/{app.js,styles.css,index.html,msx/start.json}
npm run check:es5   # es-check es5 against the built bundle
```

Output lands in `../server/webdist/`, which the Go binary embeds; `build`
overwrites only its own artifacts. Architecture of the app: `docs/frontend.md`,
player: `docs/player.md`.
