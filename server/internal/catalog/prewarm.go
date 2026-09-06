package catalog

// Background prewarm: keep the household's OWN library resident locally.
//
// The cache fills on demand, which means it only ever holds what somebody
// happened to open. Anything favourited or part-watched should be present
// unconditionally: that is the library the user actually returns to, and it is
// what must keep working (and open instantly) when TMDB is slow or down.
//
// This walks a list of titles and ensures each has a cached detail entry. It is
// deliberately slow and bounded — it runs behind the app, never in front of a
// user, and must not look like a scraper to the upstream API.

import (
	"context"
	"time"
)

// PrewarmRef is one title to ensure locally: whatever the caller's data layer
// knows (catalog stays user-agnostic, so the refs come from outside).
type PrewarmRef struct {
	TMDBID    int
	MediaType string
}

const (
	// prewarmGap paces requests so a large library trickles in instead of
	// bursting at the API.
	prewarmGap = 750 * time.Millisecond
	// prewarmMax bounds one pass, so a huge history can't turn into a long
	// unbounded crawl.
	prewarmMax = 300
)

// Prewarm ensures each ref's detail is cached, in order, stopping on ctx
// cancellation. Returns how many entries it fetched from upstream (a warm
// library reports 0 — every ref was already local). Errors per title are
// ignored: a missing/renamed title must not stop the pass.
func (s *Service) Prewarm(ctx context.Context, lang string, refs []PrewarmRef) int {
	fetched := 0
	for i, ref := range refs {
		if i >= prewarmMax {
			break
		}
		if ctx.Err() != nil {
			return fetched
		}
		mediaType := ref.MediaType
		if mediaType != "tv" {
			mediaType = "movie"
		}
		// Already local? CardCached is a pure cache read (stale counts — the
		// entry exists, and getCached refreshes stale copies on real reads).
		if _, ok := s.CardCached(mediaType, ref.TMDBID, lang); ok {
			continue
		}
		if _, err := s.Card(ctx, mediaType, ref.TMDBID, lang); err != nil {
			s.logPrewarm("prewarm: title fetch failed", ref, err)
			continue
		}
		fetched++
		select {
		case <-ctx.Done():
			return fetched
		case <-time.After(prewarmGap):
		}
	}
	return fetched
}

// logPrewarm keeps the noise at debug: a failing prewarm is not a user-visible
// problem, the on-demand path still works.
func (s *Service) logPrewarm(msg string, ref PrewarmRef, err error) {
	if s.client == nil || s.client.logger == nil {
		return
	}
	s.client.logger.Debug(msg, "tmdb_id", ref.TMDBID, "type", ref.MediaType, "error", err)
}
