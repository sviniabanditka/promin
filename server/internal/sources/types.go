// Package sources is the Lampac facade: it aggregates online balancers
// (/lite/events) and resolves a chosen balancer into playable stream URLs,
// per docs/backend.md and docs/api.md
//
// Torrents (JacRed via Lampac) are out of scope for this package for now;
// only the "online" balancer path is implemented.
package sources

// Balancer is one entry of Lampac's GET /lite/events response: an online
// source module with the URL to query it at.
type OnlineSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// json-тег "balanser" (не "balancer") — так его называет Lampac, и того
	// же имени ждёт resolve-эндпоинт и клиент. Единое имя на всей границе API.
	// Balancer is the provider code the client sends back to /resolve.
	Balancer    string `json:"balanser"`
	QualityNote string `json:"quality_note,omitempty"`
}

// OnlineResponse is the body of GET /api/v1/sources/online.
type OnlineResponse struct {
	Degraded bool           `json:"degraded"`
	Sources  []OnlineSource `json:"sources"`
}

// Stream is one playable variant returned by a resolved balancer: a
// specific quality/translation combination. URL is already wrapped through
// our own /relay endpoint (see relay.go), never a raw upstream URL.
type Stream struct {
	URL     string `json:"url"`
	Quality string `json:"quality"`
	Label   string `json:"label"`
}

// Subtitle is an external subtitle track exposed by the balancer.
type Subtitle struct {
	URL   string `json:"url"`
	Label string `json:"label"`
	Lang  string `json:"lang,omitempty"`
}

// Voice is an alternate translation/voiceover the balancer offers; picking
// one requires a follow-up resolve call with Voice=id.
type Voice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ResolveResponse is the body of GET /api/v1/sources/online/resolve.
type ResolveResponse struct {
	Streams   []Stream   `json:"streams"`
	Subtitles []Subtitle `json:"subtitles"`
	Voices    []Voice    `json:"voices"`
	// Voice is the id (from Voices) of the dub these Streams carry — the one
	// requested, or the source's default when none was. Empty if unknown, so
	// the player can put a checkmark on what's actually playing.
	Voice string `json:"voice,omitempty"`
	// AudioNames labels the HLS audio renditions in manifest order when the
	// manifest itself only has placeholder names (Collaps: rus0/rus1…).
	AudioNames []string `json:"audio_names,omitempty"`
	// Type is "hls" or "mp4", inferred from the first stream's URL. Empty
	// if no stream could be resolved (see Unresolved).
	Type string `json:"type,omitempty"`
	// Unresolved explains why Streams is empty despite a 200 response:
	//   - "rch_required": the balancer needs a companion app (RCH) on the
	//     user's real device to proxy the request — it detected the
	//     request came from a datacenter IP and asked for RCH. This is
	//     NOT necessarily a dead end: if the requesting client already has
	//     a WebSocket registered at /nws (RCH), Lampac services the
	//     request through that registered client transparently and this
	//     branch is never reached — Streams comes back populated instead,
	//     same as any other resolve. Unresolved="rch_required" only
	//     surfaces when Lampac had NO registered rch client to use, which
	//     is exactly the signal RCH/NWS below responds to: connect to
	//     NWS, retry the resolve, and next time Lampac completes it
	//     itself. See server/internal/httpapi/handlers_rch.go and
	//     web/src/core/rch.ts for the two ends of that channel.
	//   - "navigation_required": Promin followed the balancer's
	//     season/episode/similar-title navigation chain (up to a hop
	//     limit) but never reached a terminal node with a playable stream
	//     matching the requested title/season/episode.
	// Empty when Streams is non-empty.
	Unresolved string `json:"unresolved,omitempty"`
}

// ResolveRequest bundles everything needed to query one balancer.
// Title/OriginalTitle/Year/IMDbID matter in practice: several balancers
// (filmix in particular) match by title, not tmdb_id, and return 503 with
// only tmdb_id/imdb_id present — see the phase-2 report for empirical
// notes.
type ResolveRequest struct {
	Balancer      string
	TMDBID        int
	Type          string // movie|tv
	Season        int
	Episode       int
	Voice         string
	Title         string
	OriginalTitle string
	Year          int
	IMDbID        string
	// TitleRU: the Russian title from TMDB (filled server-side). Several native
	// catalogs are Russian-language and their search knows neither the
	// Ukrainian UI title nor the English original.
	TitleRU string
	// PreferMuxed: client can't play demuxed-audio HLS (old Samsung Tizen,
	// caps demuxed_hls=false) → HLS streams get routed through /remux.
	PreferMuxed bool
}

// OnlineRequest bundles the query params for GET /api/v1/sources/online.
type OnlineRequest struct {
	TMDBID        int
	Type          string // movie|tv
	Title         string
	OriginalTitle string
	Year          int
	IMDbID        string
	TitleRU       string // see ResolveRequest.TitleRU
}
