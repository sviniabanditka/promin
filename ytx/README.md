# ytx — YouTube sidecar

Signs a Promin profile into YouTube as the TV app (device code) and reads the
account's feeds through InnerTube's TV client. Media tracks are pulled over
SABR by the same signed-in session, presenting the TV app's own PO token
(`src/attest.js`, `src/potoken.js` — without it the stream is cut after ~70 s).
Promin's Go server is the only caller; see `docs/youtube.md` for the why and
`src/server.js` for the routes.

```
npm ci && YTX_DATA=./data npm start
curl -XPOST localhost:8091/v1/accounts/2/login        # → user_code + verification_url
curl localhost:8091/v1/accounts/2                     # → linked: true after approval
curl localhost:8091/v1/accounts/2/browse/home | jq '.shelves[0].items[0]'
curl 'localhost:8091/v1/accounts/2/stream/dQw4w9WgXcQ/probe'                           # 200 or 409 with the reason
curl -o v.mp4 'localhost:8091/v1/accounts/2/stream/dQw4w9WgXcQ/video?quality=1080p&start=600'
curl -XPOST -d '{"position_sec":630,"duration_sec":1200}' localhost:8091/v1/accounts/2/watch/dQw4w9WgXcQ   # history + resume point
```

Dependencies to bump when YouTube changes something: `youtubei.js`, `googlevideo`,
`bgutils-js` (PO tokens; symptom: `sabr stream protection status=2` in the log and
playback stopping after about a minute).
