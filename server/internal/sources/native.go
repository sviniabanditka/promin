package sources

// Native-source aggregation: Promin's OWN scraper providers listed and resolved
// alongside the lampac sidecar (step Ф1 of the lampac-independence plan).
//
// Promin's whole flow is TMDB-first (tmdb_id + title + year), while a scraper
// provider only knows its own catalog ids. Bridging the two is a SEARCH, and
// getting it wrong means playing the wrong film — so matching here is
// deliberately strict (exact normalized title, year within one) and refuses to
// guess. The match is what gets cached: it is the expensive-but-stable half.
// Resolved stream URLs are NOT cached — CDN links carry expiring tokens, and
// serving a stale one looks exactly like a broken source.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sviniabanditka/promin/server/internal/sources/provider"
	"github.com/sviniabanditka/promin/server/internal/store"
)

// matchTTL: how long a tmdb→source-id mapping stays valid. A catalog id is
// effectively permanent, so this is long; a miss (source has no such title) is
// cached briefly so a newly-added title is picked up the same day.
const (
	// matchLogicVersion is part of every cache key. BUMP IT whenever the
	// matching or query-building logic changes: a cached "this source doesn't
	// have the title" verdict produced by the OLD logic would otherwise survive
	// for hours and hide a title the new logic finds. (Learned the hard way —
	// a miss cached before the punctuation fix kept the source hidden after it.)
	matchLogicVersion = 4

	matchTTL = 7 * 24 * time.Hour
	// matchMissTTL is deliberately SHORT: a miss is a negative verdict that may
	// simply mean "our query was wrong", and it is keyed by tmdb id (not by the
	// query), so a bad one must expire quickly rather than linger.
	matchMissTTL = 15 * time.Minute
	// nativeBudget bounds one native provider operation during resolve.
	nativeBudget = 10 * time.Second
	// nativeListBudget bounds the availability search on the LISTING path. It
	// runs concurrently with lampac, so it is not competing for the aggregate
	// budget — but the listing still must not make the user wait forever.
	nativeListBudget = 6 * time.Second
)

// Cache is the small key/JSON store the native layer needs. Implemented over
// the existing generic cache table (see NewStoreCache) — no new schema.
type Cache interface {
	Get(key string) (string, bool)
	Set(key, value string, ttl time.Duration)
}

type storeCache struct{ repo *store.TMDBCacheRepo }

// NewStoreCache adapts the shared cache table to the Cache interface. The table
// is a generic key/json/expires_at KV (already used for TMDB and OMDb), so the
// native layer needs no migration of its own.
func NewStoreCache(repo *store.TMDBCacheRepo) Cache { return &storeCache{repo: repo} }

func (c *storeCache) Get(key string) (string, bool) {
	if c.repo == nil {
		return "", false
	}
	row, err := c.repo.Get(key)
	if err != nil || row.ExpiresAt <= time.Now().Unix() {
		return "", false
	}
	return row.JSON, true
}

func (c *storeCache) Set(key, value string, ttl time.Duration) {
	if c.repo == nil {
		return
	}
	_ = c.repo.Set(key, value, time.Now().Add(ttl).Unix())
}

// NativeProvider is one registered scraper plus the context it runs with.
type NativeProvider struct {
	P       provider.Provider
	Ctx     provider.Ctx
	Name    string // display name shown in the source list
	Enabled bool
}

// --- title matching -------------------------------------------------------

// normalizeTitle lowercases and flattens everything that varies between
// catalogs (punctuation, spacing, the ё/е and і/и spelling splits) so two
// spellings of the same title compare equal without matching genuinely
// different films. Punctuation becomes a SPACE rather than vanishing, so
// "Спайдер-мен" and "Спайдер мен" agree; titlesMatch additionally compares the
// space-free forms, which is what makes "Місто 2.0" and "Місто 20" agree too.
func normalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'ё':
			r = 'е'
		case 'і':
			r = 'и'
		case 'ї':
			r = 'и'
		case '’', '\'', '`':
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		// Punctuation and whitespace alike collapse to one separator.
		b.WriteRune(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// titlesMatch compares two already-normalized titles, also accepting the
// space-free forms so a catalog that writes "2.0" matches a TMDB "20".
func titlesMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return strings.ReplaceAll(a, " ", "") == strings.ReplaceAll(b, " ", "")
}

// tokens splits a normalized title into comparable words.
func tokens(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

// dropNumerals removes pure-number tokens. Catalogs often add a sequel number
// the metadata provider leaves out ("Дюна 2: Часть вторая" vs "Дюна: Часть
// вторая"); ignoring ONLY the numbers lets that through while everything else
// still has to match word for word.
func dropNumerals(toks []string) []string {
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		if _, err := strconv.Atoi(t); err == nil {
			continue
		}
		out = append(out, t)
	}
	return out
}

// sameWords compares two token slices for exact sequence equality.
func sameWords(a, b []string) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// match quality, best first. Anything below matchNone is a usable candidate.
type matchRank int

const (
	rankExactTitleAndYear       matchRank = iota // same title, same year (±1)
	rankExactTitleNoYear                         // same title, no year to compare
	rankNumeralVariantExactYear                  // titles differ only by a sequel number, years identical
	rankNone
)

// rankCandidate scores one catalog hit against the request. Deliberately
// conservative: a loose match plays the WRONG film, which is worse than showing
// no native source at all. Every accepting branch requires either an exact
// title or an exact year — never neither.
func rankCandidate(it provider.SearchItem, req OnlineRequest) matchRank {
	got := normalizeTitle(it.Title)
	if got == "" {
		return rankNone
	}
	// Both sides also in their "Episode N"-free form: TMDB's "Star Wars: The
	// Force Awakens" must meet Filmix's "Star Wars: Episode VII – The Force
	// Awakens".
	wants := []string{normalizeTitle(req.Title), normalizeTitle(req.OriginalTitle), normalizeTitle(req.TitleRU),
		normalizeTitle(dropEpisodeSegment(req.Title)), normalizeTitle(dropEpisodeSegment(req.OriginalTitle)), normalizeTitle(dropEpisodeSegment(req.TitleRU))}
	// The candidate's own names: localized title, plus its original title when
	// the source exposes one ("Дюна" / "Dune: Part One" must still match a
	// request whose original title is "Dune" via the localized name).
	gots := []string{got, normalizeTitle(dropEpisodeSegment(it.Title))}
	if o := normalizeTitle(it.OriginalTitle); o != "" {
		gots = append(gots, o, normalizeTitle(dropEpisodeSegment(it.OriginalTitle)))
	}

	for _, want := range wants {
		if want == "" {
			continue
		}
		matched := false
		for _, g := range gots {
			if titlesMatch(g, want) {
				matched = true
				break
			}
		}
		if matched {
			if req.Year > 0 && it.Year > 0 {
				d := it.Year - req.Year
				if d < 0 {
					d = -d
				}
				if d <= 1 {
					return rankExactTitleAndYear
				}
				// A series' SEASON page carries that season's year, not the
				// show's premiere: same title + tv on both sides is still the
				// show (remakes of series under the identical name are rare).
				if req.Type == "tv" && it.Type == "tv" {
					return rankExactTitleNoYear
				}
				// Same title, clearly different year → a different film
				// (remake/reboot). Keep looking.
				continue
			}
			return rankExactTitleNoYear
		}
	}

	// Numeral-only difference, anchored on an EXACT year. Deliberately narrow:
	// a mere subset rule would accept "Берлін кличе" for a request for
	// "Берлін" (same year, different film). Requiring every non-numeric word to
	// match keeps the sequel-number case and nothing else.
	if req.Year > 0 && it.Year == req.Year {
		gotWords := dropNumerals(tokens(got))
		for _, want := range wants {
			if want == "" {
				continue
			}
			if sameWords(gotWords, dropNumerals(tokens(want))) {
				return rankNumeralVariantExactYear
			}
		}
	}
	return rankNone
}

// pickByOriginalQueryYear: the search was for the original title (as typed or
// punctuation-flattened, with or without the "Episode N" chunk), the catalog
// answered with a handful of items, and exactly one of them has the release
// year → accept it even though its localized name is in a language we can't
// compare against.
func pickByOriginalQueryYear(items []provider.SearchItem, query string, req OnlineRequest) (provider.SearchItem, bool) {
	if req.Year <= 0 || req.OriginalTitle == "" || len(items) == 0 || len(items) > 6 {
		return provider.SearchItem{}, false
	}
	q := normalizeTitle(query)
	orig := normalizeTitle(req.OriginalTitle)
	origStripped := normalizeTitle(dropEpisodeSegment(req.OriginalTitle))
	if q != orig && q != origStripped {
		return provider.SearchItem{}, false
	}
	var hit provider.SearchItem
	n := 0
	for _, it := range items {
		if it.Year == req.Year {
			hit = it
			n++
		}
	}
	if n != 1 {
		return provider.SearchItem{}, false
	}
	return hit, true
}

// pickMatch chooses the catalog hit for a title, or reports none. It scans ALL
// candidates and returns the best-ranked one rather than the first acceptable
// one, so an exact hit always beats a looser fallback that happens to appear
// earlier in the result list.
func pickMatch(items []provider.SearchItem, req OnlineRequest) (provider.SearchItem, bool) {
	if normalizeTitle(req.Title) == "" && normalizeTitle(req.OriginalTitle) == "" {
		return provider.SearchItem{}, false
	}
	best := rankNone
	var bestItem provider.SearchItem
	for _, it := range items {
		if r := rankCandidate(it, req); r < best {
			best, bestItem = r, it
			if best == rankExactTitleAndYear {
				break // cannot do better
			}
		}
	}
	if best == rankNone {
		return provider.SearchItem{}, false
	}
	return bestItem, true
}

// searchQueries builds the query variants to try against the catalog, most
// specific first. A catalog's search is punctuation-sensitive: the full
// "Звёздные войны: Эпизод 1 – Скрытая угроза" returns a single hit (or none)
// while the plainer "Звёздные войны Эпизод 1" returns the whole saga — so if
// the precise query misses we widen it and let the ranked matcher pick. Deduped,
// a handful of requests at most, and only ever on a cache miss.
func searchQueries(req OnlineRequest) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(q string) {
		q = strings.TrimSpace(q)
		if len(q) < 2 || seen[q] {
			return
		}
		seen[q] = true
		out = append(out, q)
	}

	for _, base := range []string{req.Title, req.TitleRU, req.OriginalTitle} {
		if base == "" {
			continue
		}
		// TMDB writes saga entries as "Зоряні війни: Епізод 7 — Пробудження
		// сили" / "Star Wars: Episode VII - The Force Awakens"; catalogs mostly
		// carry "Зоряні війни: Пробудження сили" or just the subtitle. Try the
		// title as is and without the "Episode N" segment; for each, the
		// punctuation-flattened form, the franchise name alone (the ranked
		// matcher then picks by year) and the subtitle alone.
		variants := []string{base}
		if stripped := dropEpisodeSegment(base); stripped != base {
			variants = append(variants, stripped)
		}
		for _, v := range variants {
			add(v)
			add(normalizeTitle(v)) // punctuation flattened — the usual reason a search misses
			parts := splitSubtitle(v)
			if len(parts) == 2 {
				add(normalizeTitle(parts[0]))
				if len(normalizeTitle(parts[1])) >= 4 {
					add(normalizeTitle(parts[1]))
				}
			}
		}
	}
	return out
}

// splitSubtitle splits "Franchise: Subtitle" / "Franchise – Subtitle" at the
// FIRST separator; returns the whole string alone when there is none.
func splitSubtitle(s string) []string {
	i := strings.IndexAny(s, ":–—")
	if i <= 1 {
		if j := strings.Index(s, " - "); j > 1 {
			i = j
		}
	}
	if i <= 1 {
		return []string{s}
	}
	left := strings.TrimSpace(s[:i])
	right := strings.TrimSpace(strings.TrimLeft(s[i:], ":–— -"))
	if right == "" {
		return []string{left}
	}
	return []string{left, right}
}

// episodeSegment matches the "Епізод 7 —" / "Эпизод I –" / "Episode VII -"
// chunk (with its trailing separator) that TMDB inserts into saga titles.
var episodeSegment = regexp.MustCompile(`(?i)\s*(?:епізод|эпизод|episode|part|частина|часть)\s+(?:[0-9]+|[ivx]+)\s*[:–—-]?\s*`)

func dropEpisodeSegment(s string) string {
	out := episodeSegment.ReplaceAllString(s, " ")
	out = strings.ReplaceAll(out, ":  ", ": ")
	return strings.TrimSpace(strings.Join(strings.Fields(out), " "))
}

func matchKey(providerID string, tmdbID int, mediaType string) string {
	return fmt.Sprintf("nsrc:match:v%d:%s:%s:%d", matchLogicVersion, providerID, mediaType, tmdbID)
}

type cachedMatch struct {
	SourceID string `json:"source_id"` // empty = known miss
}

// matchSource maps a TMDB title to this provider's catalog id, caching both
// hits and misses. Returns ok=false when the source doesn't carry the title.
func (s *Service) matchSource(ctx context.Context, np NativeProvider, req OnlineRequest) (string, bool) {
	key := matchKey(np.P.ID(), req.TMDBID, req.Type)
	if s.nativeCache != nil {
		if raw, hit := s.nativeCache.Get(key); hit {
			var cm cachedMatch
			if err := json.Unmarshal([]byte(raw), &cm); err == nil {
				return cm.SourceID, cm.SourceID != ""
			}
		}
	}

	var (
		it       provider.SearchItem
		ok       bool
		lastErr  error
		anyReply bool
	)
	// Exact lookup by imdb id when the provider has an id map — no ranking
	// heuristics can beat a shared identifier.
	if lk, isLk := np.P.(provider.Lookuper); isLk && req.IMDbID != "" {
		found, hit, err := lk.Lookup(ctx, provider.ExternalRef{
			TMDBID: req.TMDBID, IMDbID: req.IMDbID, Title: req.Title, OriginalTitle: req.OriginalTitle, Year: req.Year, Type: req.Type,
		}, np.Ctx)
		if err == nil && hit {
			it, ok, anyReply = found, true, true
		}
	}

	queries := searchQueries(req)
	if len(queries) == 0 && !ok {
		return "", false
	}
	for _, query := range queries {
		if ok {
			break
		}
		sctx, cancel := context.WithTimeout(ctx, nativeBudget)
		items, err := np.P.Search(sctx, query, 1, np.Ctx)
		cancel()
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				break // caller's budget is gone
			}
			continue
		}
		anyReply = true
		s.logger.Debug("native: search", "provider", np.P.ID(), "query", query, "hits", len(items))
		if it, ok = pickMatch(items, req); ok {
			break
		}
		// A catalog in another language (Kinogo is Russian, TMDB gave us the
		// Ukrainian title) can't match by name at all. But when the query was
		// the film's ORIGINAL title, the result set is tiny and exactly one hit
		// carries the release year, that hit is the film.
		if it, ok = pickByOriginalQueryYear(items, query, req); ok {
			s.logger.Info("native: matched by original-title query + year", "provider", np.P.ID(), "tmdb_id", req.TMDBID, "hit", it.Title, "year", it.Year)
			break
		}
	}
	if !anyReply {
		// Source unreachable / markup changed: DON'T cache a miss, so a
		// transient failure doesn't hide the source for hours.
		s.logger.Warn("native: search failed", "provider", np.P.ID(), "tmdb_id", req.TMDBID, "error", lastErr)
		return "", false
	}
	sourceID := ""
	ttl := matchMissTTL
	if ok {
		sourceID = it.ID
		ttl = matchTTL
	}
	if s.nativeCache != nil {
		if b, err := json.Marshal(cachedMatch{SourceID: sourceID}); err == nil {
			s.nativeCache.Set(key, string(b), ttl)
		}
	}
	if !ok {
		// Visible on purpose: "the source doesn't carry this title" is the most
		// common reason a native source is missing from the list, and it is
		// otherwise indistinguishable from a bug.
		s.logger.Info("native: no catalog match", "provider", np.P.ID(), "tmdb_id", req.TMDBID,
			"title", req.Title, "queries", len(queries))
	}
	return sourceID, ok
}

// nativeSources appends the native providers that actually carry this title to
// an existing balancer list. Never fails the listing: a provider that errors or
// has no match is simply absent.
func (s *Service) nativeSources(ctx context.Context, req OnlineRequest) []OnlineSource {
	// Providers are probed concurrently: each match is a search round-trip to
	// a third-party site, and with several providers a serial walk ate the
	// whole listing budget. Output keeps registration order.
	type res struct {
		idx int
		ok  bool
	}
	ch := make(chan res, len(s.native))
	n := 0
	for i, np := range s.native {
		if !np.Enabled {
			continue
		}
		n++
		go func(i int, np NativeProvider) {
			_, ok := s.matchSource(ctx, np, req)
			ch <- res{idx: i, ok: ok}
		}(i, np)
	}
	matched := make([]bool, len(s.native))
	for ; n > 0; n-- {
		r := <-ch
		matched[r.idx] = r.ok
	}
	out := []OnlineSource{}
	for i, np := range s.native {
		if !matched[i] {
			continue
		}
		out = append(out, OnlineSource{
			ID:       np.P.ID(),
			Name:     np.Name,
			Balancer: np.P.ID(),
		})
	}
	return out
}

// nativeSourcesAsync starts the native availability lookup immediately and
// returns a channel with the result. Derived from the REQUEST context with its
// own budget, so a slow lampac can neither starve nor delay it (see Online).
// Always sends exactly once; a nil/empty registry sends an empty slice.
func (s *Service) nativeSourcesAsync(reqCtx context.Context, req OnlineRequest) <-chan []OnlineSource {
	ch := make(chan []OnlineSource, 1)
	if len(s.native) == 0 {
		ch <- []OnlineSource{}
		return ch
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), nativeListBudget)
		defer cancel()
		ch <- s.nativeSources(ctx, req)
	}()
	return ch
}

// nativeProvider returns the registered provider for a balancer code.
func (s *Service) nativeProvider(balancer string) (NativeProvider, bool) {
	for _, np := range s.native {
		if np.Enabled && np.P.ID() == balancer {
			return np, true
		}
	}
	return NativeProvider{}, false
}

// resolveNative resolves through one of our own providers. The stream URL is
// wrapped in /relay exactly like a lampac one, so the TV never talks to the
// source directly.
func (s *Service) resolveNative(ctx context.Context, np NativeProvider, req ResolveRequest) (ResolveResponse, error) {
	sourceID, ok := s.matchSource(ctx, np, OnlineRequest{
		TMDBID: req.TMDBID, TitleRU: req.TitleRU,
		Type:          req.Type,
		Title:         req.Title,
		OriginalTitle: req.OriginalTitle,
		Year:          req.Year,
		IMDbID:        req.IMDbID,
	})
	if !ok {
		return ResolveResponse{Streams: []Stream{}, Unresolved: "not_found"}, nil
	}

	rctx, cancel := context.WithTimeout(ctx, navigationBudget)
	defer cancel()

	// Voice list comes from the title page's tree; fetch it so the client can
	// offer dubs even on the first resolve.
	voices := []Voice{}
	if td, err := np.P.Title(rctx, sourceID, np.Ctx); err == nil {
		for _, a := range td.Audio {
			voices = append(voices, Voice{ID: a.ID, Name: a.Label})
		}
	}

	st, err := np.P.Resolve(rctx, provider.ResolveRequest{
		ID:      sourceID,
		Season:  req.Season,
		Episode: req.Episode,
		Audio:   req.Voice,
	}, np.Ctx)
	if err != nil {
		s.logger.Warn("native: resolve failed", "provider", np.P.ID(), "source_id", sourceID, "error", err)
		return ResolveResponse{Streams: []Stream{}, Voices: voices, Unresolved: "resolve_failed"}, nil
	}

	subs := []Subtitle{}
	for _, sb := range st.Subtitles {
		subs = append(subs, Subtitle{URL: EncodeRelayURL(sb.URL), Label: sb.Label, Lang: sb.Lang})
	}
	kind := st.Kind
	if kind == "" {
		kind = "hls"
	}
	streams := []Stream{}
	if len(st.Variants) > 0 {
		for _, v := range st.Variants {
			q := strconv.Itoa(v.Quality) + "p"
			label := v.Label
			if label == "" {
				label = q
			}
			// Same routing rule as the lampac path: an old Samsung (PreferMuxed)
			// can't play demuxed-audio HLS (VeoVeo's grouped.m3u8 carries
			// EXT-X-MEDIA audio groups) → /remux muxes it to one stream.
			streams = append(streams, Stream{URL: wrapStream(v.URL, req.PreferMuxed), Quality: q, Label: label})
		}
	} else {
		streams = append(streams, Stream{URL: wrapStream(st.URL, req.PreferMuxed), Quality: "auto", Label: np.Name})
	}
	voice := req.Voice
	if voice == "" {
		voice = st.Audio
	}
	return ResolveResponse{
		Streams:    streams,
		Subtitles:  subs,
		Voices:     voices,
		Voice:      voice,
		AudioNames: st.AudioNames,
		Type:       kind,
	}, nil
}
