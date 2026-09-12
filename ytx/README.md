# ytx — YouTube sidecar

Signs a Promin profile into YouTube as the TV app (device code), reads the
account's feeds through InnerTube's TV client and serves media tracks pulled
over SABR. Promin's Go server is the only caller; see `docs/proposals/youtube.md`
for the why and `src/server.js` for the routes.

```
npm ci && YTX_DATA=./data npm start
curl -XPOST localhost:8091/v1/accounts/2/login        # → user_code + verification_url
curl localhost:8091/v1/accounts/2                     # → linked: true after approval
curl localhost:8091/v1/accounts/2/browse/home | jq '.shelves[0].items[0]'
curl -o v.mp4 'localhost:8091/v1/accounts/2/stream/dQw4w9WgXcQ/video?quality=1080p'
```

Dependencies to bump when YouTube changes something: `youtubei.js`, `googlevideo`.
