package sources

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sviniabanditka/promin/server/internal/sources/provider"
)

// --- matching (the dangerous part: a loose match plays the WRONG film) ----

func item(id, title string, year int) provider.SearchItem {
	return provider.SearchItem{ID: id, Title: title, Year: year, Type: "movie"}
}

func TestPickMatchExactTitleAndYear(t *testing.T) {
	items := []provider.SearchItem{
		item("a", "Берлін", 1945),
		item("b", "Берлін", 2023),
		item("c", "Берлін кличе", 2008),
	}
	got, ok := pickMatch(items, OnlineRequest{Title: "Берлін", Year: 2023})
	if !ok || got.ID != "b" {
		t.Fatalf("want b (2023), got %+v ok=%v", got, ok)
	}
}

// A remake must NOT be served for the original (same title, distant year).
func TestPickMatchRejectsWrongYear(t *testing.T) {
	items := []provider.SearchItem{item("old", "Дюна", 1984)}
	if got, ok := pickMatch(items, OnlineRequest{Title: "Дюна", Year: 2021}); ok {
		t.Fatalf("matched a %d film for a 2021 request: %+v", got.Year, got)
	}
}

// Catalogs and TMDB disagree by a year on new-year releases — ±1 is allowed.
func TestPickMatchYearOffByOne(t *testing.T) {
	items := []provider.SearchItem{item("x", "Фільм", 2019)}
	if _, ok := pickMatch(items, OnlineRequest{Title: "Фільм", Year: 2020}); !ok {
		t.Fatal("±1 year should match")
	}
}

// A merely similar title is not a match.
func TestPickMatchRejectsPartialTitle(t *testing.T) {
	items := []provider.SearchItem{item("c", "Берлін кличе", 2023)}
	if got, ok := pickMatch(items, OnlineRequest{Title: "Берлін", Year: 2023}); ok {
		t.Fatalf("partial title matched: %+v", got)
	}
}

// Original-title fallback: catalogs often carry the English name.
func TestPickMatchOriginalTitle(t *testing.T) {
	items := []provider.SearchItem{item("o", "Dune", 2021)}
	got, ok := pickMatch(items, OnlineRequest{Title: "Дюна", OriginalTitle: "Dune", Year: 2021})
	if !ok || got.ID != "o" {
		t.Fatalf("original-title match failed: %+v ok=%v", got, ok)
	}
}

// Punctuation/case/ё-е/і-и spelling splits must not defeat an exact match.
func TestNormalizeTitleSpellingVariants(t *testing.T) {
	pairs := [][2]string{
		{"Ёлки-палки!", "елки палки"},
		{"Місто, 2.0", "мисто 2 0"},
		{"  The   Matrix  ", "the matrix"},
		{"Don’t Look Up", "dont look up"},
	}
	for _, p := range pairs {
		if got := normalizeTitle(p[0]); got != p[1] {
			t.Errorf("normalizeTitle(%q) = %q, want %q", p[0], got, p[1])
		}
	}
}

// Hyphenation and intra-token punctuation must not defeat a match.
func TestPickMatchPunctuationVariants(t *testing.T) {
	cases := []struct{ catalog, tmdb string }{
		{"Спайдер-мен", "Спайдер мен"},
		{"Місто 2.0", "Місто 20"},
		{"Ёлки-палки", "Елки палки"},
	}
	for _, c := range cases {
		items := []provider.SearchItem{item("x", c.catalog, 2020)}
		if _, ok := pickMatch(items, OnlineRequest{Title: c.tmdb, Year: 2020}); !ok {
			t.Errorf("%q should match %q", c.catalog, c.tmdb)
		}
	}
}

func TestPickMatchNoTitleNoMatch(t *testing.T) {
	if _, ok := pickMatch([]provider.SearchItem{item("a", "X", 2020)}, OnlineRequest{}); ok {
		t.Fatal("empty request must not match anything")
	}
}

// --- cache + degradation --------------------------------------------------

type memCache struct {
	data map[string]string
	sets int
}

func newMemCache() *memCache { return &memCache{data: map[string]string{}} }
func (c *memCache) Get(k string) (string, bool) {
	v, ok := c.data[k]
	return v, ok
}
func (c *memCache) Set(k, v string, _ time.Duration) {
	c.data[k] = v
	c.sets++
}

// stubProvider counts searches so the test can prove the match is cached.
type stubProvider struct {
	id       string
	items    []provider.SearchItem
	searches int
	err      error
}

func (s *stubProvider) ID() string { return s.id }
func (s *stubProvider) Search(_ context.Context, _ string, _ int, _ provider.Ctx) ([]provider.SearchItem, error) {
	s.searches++
	return s.items, s.err
}
func (s *stubProvider) Title(_ context.Context, _ string, _ provider.Ctx) (provider.TitleDetails, error) {
	return provider.TitleDetails{}, nil
}
func (s *stubProvider) Resolve(_ context.Context, _ provider.ResolveRequest, _ provider.Ctx) (provider.ResolvedStream, error) {
	return provider.ResolvedStream{URL: "https://cdn.example/x/hls.m3u8", Kind: "hls"}, nil
}

func newTestService(p provider.Provider, cache Cache) *Service {
	svc := NewService(nil, slog.Default())
	svc.SetNativeProviders(cache, NativeProvider{P: p, Name: "Promin", Enabled: true})
	return svc
}

// A hit is cached: the second lookup performs no search.
func TestMatchSourceCachesHit(t *testing.T) {
	p := &stubProvider{id: "test", items: []provider.SearchItem{item("id-1", "Фільм", 2020)}}
	cache := newMemCache()
	svc := newTestService(p, cache)
	req := OnlineRequest{TMDBID: 7, Type: "movie", Title: "Фільм", Year: 2020}

	for i := 0; i < 3; i++ {
		id, ok := svc.matchSource(context.Background(), svc.native[0], req)
		if !ok || id != "id-1" {
			t.Fatalf("iteration %d: id=%q ok=%v", i, id, ok)
		}
	}
	if p.searches != 1 {
		t.Errorf("want 1 search (rest cached), got %d", p.searches)
	}
}

// A miss is cached too, so a title the source lacks doesn't re-search per view.
func TestMatchSourceCachesMiss(t *testing.T) {
	p := &stubProvider{id: "test", items: []provider.SearchItem{item("other", "Інше", 1999)}}
	svc := newTestService(p, newMemCache())
	req := OnlineRequest{TMDBID: 8, Type: "movie", Title: "Фільм", Year: 2020}

	if _, ok := svc.matchSource(context.Background(), svc.native[0], req); ok {
		t.Fatal("should not match")
	}
	// A miss exhausts every query variant (the catalog may only answer a
	// widened one), so the count is not fixed — what matters is that the miss
	// is CACHED: the second call must not search at all.
	afterFirst := p.searches
	if afterFirst == 0 {
		t.Fatal("no search attempted")
	}
	if _, ok := svc.matchSource(context.Background(), svc.native[0], req); ok {
		t.Fatal("should not match")
	}
	if p.searches != afterFirst {
		t.Errorf("miss was not cached: %d searches after the second call (was %d)", p.searches, afterFirst)
	}
}

// A transient failure must NOT be cached — otherwise a blip hides the source
// for hours.
func TestMatchSourceDoesNotCacheErrors(t *testing.T) {
	p := &stubProvider{id: "test", err: provider.ErrSourceUnavailable}
	cache := newMemCache()
	svc := newTestService(p, cache)
	req := OnlineRequest{TMDBID: 9, Type: "movie", Title: "Фільм", Year: 2020}

	if _, ok := svc.matchSource(context.Background(), svc.native[0], req); ok {
		t.Fatal("errored search must not match")
	}
	afterFirst := p.searches
	if afterFirst == 0 {
		t.Fatal("no search attempted")
	}
	// The point: a FAILED lookup is not cached, so the next call really searches
	// again (each call may try several query variants — the count is not fixed).
	if _, ok := svc.matchSource(context.Background(), svc.native[0], req); ok {
		t.Fatal("errored search must not match")
	}
	if p.searches <= afterFirst {
		t.Errorf("second call served from cache after an error (%d searches)", p.searches)
	}
	if cache.sets != 0 {
		t.Errorf("error was cached (%d sets)", cache.sets)
	}
}

// The native source is listed only when the title actually matched.
func TestNativeSourcesListedOnlyOnMatch(t *testing.T) {
	hit := newTestService(&stubProvider{id: "test", items: []provider.SearchItem{item("i", "Фільм", 2020)}}, newMemCache())
	got := hit.nativeSources(context.Background(), OnlineRequest{TMDBID: 1, Type: "movie", Title: "Фільм", Year: 2020})
	if len(got) != 1 || got[0].Balancer != "test" {
		t.Fatalf("expected one non-RCH native source, got %+v", got)
	}

	miss := newTestService(&stubProvider{id: "test"}, newMemCache())
	if got := miss.nativeSources(context.Background(), OnlineRequest{TMDBID: 2, Type: "movie", Title: "Нема", Year: 2020}); len(got) != 0 {
		t.Fatalf("unmatched title must not be listed: %+v", got)
	}
}

// A resolved native stream must be relay-wrapped — the TV never gets a raw
// upstream URL.
func TestResolveNativeWrapsRelay(t *testing.T) {
	p := &stubProvider{id: "test", items: []provider.SearchItem{item("i", "Фільм", 2020)}}
	svc := newTestService(p, newMemCache())
	resp, err := svc.Resolve(context.Background(), ResolveRequest{
		Balancer: "test", TMDBID: 3, Type: "movie", Title: "Фільм", Year: 2020,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resp.Streams) != 1 {
		t.Fatalf("want one stream, got %+v", resp.Streams)
	}
	if !strings.HasPrefix(resp.Streams[0].URL, "/relay?u=") {
		t.Errorf("stream url not relay-wrapped: %q", resp.Streams[0].URL)
	}
	if strings.Contains(resp.Streams[0].URL, "cdn.example") {
		t.Errorf("raw upstream leaked into the stream url: %q", resp.Streams[0].URL)
	}
	if resp.Type != "hls" {
		t.Errorf("type = %q", resp.Type)
	}
}

// Old Samsung (PreferMuxed): a native HLS stream must take the /remux path —
// VeoVeo's grouped.m3u8 carries EXT-X-MEDIA audio groups that webview can't play.
func TestResolveNativePreferMuxedRoutesRemux(t *testing.T) {
	p := &stubProvider{id: "test", items: []provider.SearchItem{item("i", "Фільм", 2020)}}
	svc := newTestService(p, newMemCache())
	resp, err := svc.Resolve(context.Background(), ResolveRequest{
		Balancer: "test", TMDBID: 3, Type: "movie", Title: "Фільм", Year: 2020, PreferMuxed: true,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resp.Streams) != 1 || !strings.HasPrefix(resp.Streams[0].URL, "/remux?") {
		t.Fatalf("old-Samsung HLS must be /remux-wrapped, got %+v", resp.Streams)
	}
}

// An unmatched title resolves to a typed unresolved state, not an error.
func TestResolveNativeNotFound(t *testing.T) {
	svc := newTestService(&stubProvider{id: "test"}, newMemCache())
	resp, err := svc.Resolve(context.Background(), ResolveRequest{
		Balancer: "test", TMDBID: 4, Type: "movie", Title: "Нема", Year: 2020,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Unresolved != "not_found" || len(resp.Streams) != 0 {
		t.Fatalf("want not_found with no streams, got %+v", resp)
	}
}

// Nil cache must still work (correctness must not depend on caching).
func TestMatchSourceWorksWithoutCache(t *testing.T) {
	p := &stubProvider{id: "test", items: []provider.SearchItem{item("i", "Фільм", 2020)}}
	svc := NewService(nil, slog.Default())
	svc.SetNativeProviders(nil, NativeProvider{P: p, Name: "Promin", Enabled: true})
	if id, ok := svc.matchSource(context.Background(), svc.native[0], OnlineRequest{TMDBID: 5, Type: "movie", Title: "Фільм", Year: 2020}); !ok || id != "i" {
		t.Fatalf("id=%q ok=%v", id, ok)
	}
}

// slowProvider simulates a real network search that takes a moment.
type slowProvider struct {
	stubProvider
	delay time.Duration
}

func (s *slowProvider) Search(ctx context.Context, q string, page int, pc provider.Ctx) ([]provider.SearchItem, error) {
	select {
	case <-time.After(s.delay):
		return s.stubProvider.Search(ctx, q, page, pc)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// REGRESSION: a caller context that is ALREADY past its deadline (what a
// timed-out lampac leaves behind) must not starve the native lookup. Before the
// fix the native search inherited the exhausted aggregate budget and died
// milliseconds later, so "lampac down → serve ours" never worked.
func TestNativeSourcesSurviveExhaustedParentContext(t *testing.T) {
	p := &slowProvider{
		stubProvider: stubProvider{id: "test", items: []provider.SearchItem{item("i", "Фільм", 2020)}},
		delay:        150 * time.Millisecond,
	}
	svc := newTestService(p, newMemCache())

	// A context whose deadline has already passed.
	dead, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)
	if dead.Err() == nil {
		t.Fatal("test setup: context should be expired")
	}

	got := <-svc.nativeSourcesAsync(dead, OnlineRequest{TMDBID: 11, Type: "movie", Title: "Фільм", Year: 2020})
	if len(got) != 1 {
		t.Fatalf("native lookup starved by the dead parent context: %+v", got)
	}
}

// The async wrapper must always deliver exactly once, even with no providers.
func TestNativeSourcesAsyncAlwaysSends(t *testing.T) {
	svc := NewService(nil, slog.Default())
	select {
	case got := <-svc.nativeSourcesAsync(context.Background(), OnlineRequest{Title: "X"}):
		if len(got) != 0 {
			t.Fatalf("want empty, got %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no value sent for an empty registry")
	}
}

// A punctuation-heavy title must fall back to widened queries: the catalog's
// search is punctuation-sensitive, so the exact string can miss a title it
// actually carries ("Звёздные войны: Эпизод 1 – Скрытая угроза").
func TestSearchQueriesWidenOnPunctuation(t *testing.T) {
	qs := searchQueries(OnlineRequest{
		Title:         "Звёздные войны: Эпизод 1 – Скрытая угроза",
		OriginalTitle: "Star Wars: Episode I - The Phantom Menace",
	})
	if len(qs) < 3 {
		t.Fatalf("expected several query variants, got %v", qs)
	}
	if qs[0] != "Звёздные войны: Эпизод 1 – Скрытая угроза" {
		t.Errorf("most specific query must come first, got %q", qs[0])
	}
	joined := strings.Join(qs, "|")
	for _, want := range []string{"звездные войны эпизод 1 скрытая угроза", "звездные войны"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing widened query %q in %v", want, qs)
		}
	}
	// No duplicates, nothing trivially short.
	seen := map[string]bool{}
	for _, q := range qs {
		if seen[q] {
			t.Errorf("duplicate query %q", q)
		}
		seen[q] = true
		if len(q) < 2 {
			t.Errorf("degenerate query %q", q)
		}
	}
}

// The widened query is what finds the title: the exact one returns nothing.
func TestMatchSourceUsesWidenedQuery(t *testing.T) {
	target := provider.SearchItem{ID: "sw1", Title: "Звёздные войны: Эпизод 1 – Скрытая угроза", Year: 1999, Type: "movie"}
	p := &queryAwareProvider{hits: map[string][]provider.SearchItem{
		"звездные войны": {target},
	}}
	svc := newTestService(p, newMemCache())
	id, ok := svc.matchSource(context.Background(), svc.native[0], OnlineRequest{
		TMDBID: 1893, Type: "movie",
		Title: "Звёздные войны: Эпизод 1 – Скрытая угроза", Year: 1999,
	})
	if !ok || id != "sw1" {
		t.Fatalf("widened query did not rescue the match: id=%q ok=%v queries=%v", id, ok, p.asked)
	}
	if len(p.asked) < 2 {
		t.Errorf("expected the exact query to be tried first: %v", p.asked)
	}
}

// queryAwareProvider answers only for the exact queries it was configured with.
type queryAwareProvider struct {
	stubProvider
	hits  map[string][]provider.SearchItem
	asked []string
}

func (q *queryAwareProvider) ID() string { return "test" }
func (q *queryAwareProvider) Search(_ context.Context, query string, _ int, _ provider.Ctx) ([]provider.SearchItem, error) {
	q.asked = append(q.asked, query)
	return q.hits[query], nil
}

func TestSearchQueriesSagaTitles(t *testing.T) {
	qs := searchQueries(OnlineRequest{Title: "Зоряні війни: Епізод 7 — Пробудження сили", OriginalTitle: "Star Wars: Episode VII - The Force Awakens"})
	want := []string{"Зоряні війни: Пробудження сили", "Пробудження сили", "Star Wars: The Force Awakens", "The Force Awakens"}
	for _, w := range want {
		found := false
		for _, q := range qs {
			if q == w || q == normalizeTitle(w) {
				found = true
			}
		}
		if !found {
			t.Errorf("missing query variant %q in %v", w, qs)
		}
	}
	if len(qs) > 14 {
		t.Errorf("too many query variants: %d", len(qs))
	}
}

func TestRankCandidateIgnoresEpisodeSegment(t *testing.T) {
	req := OnlineRequest{Title: "Зоряні війни: Епізод 7 — Пробудження сили", OriginalTitle: "Star Wars: The Force Awakens", Year: 2015}
	it := provider.SearchItem{Title: "Звёздные войны: Эпизод 7 – Пробуждение силы", OriginalTitle: "Star Wars: Episode VII – The Force Awakens", Year: 2015}
	if r := rankCandidate(it, req); r != rankExactTitleAndYear {
		t.Fatalf("rank = %v, want exact title+year", r)
	}
}

func TestPickByOriginalQueryYear(t *testing.T) {
	req := OnlineRequest{Title: "Зоряні війни: Епізод 7 — Пробудження сили", OriginalTitle: "Star Wars: The Force Awakens", Year: 2015}
	items := []provider.SearchItem{{ID: "a", Title: "Звёздные войны: Пробуждение силы", Year: 2015}}
	if it, ok := pickByOriginalQueryYear(items, "star wars the force awakens", req); !ok || it.ID != "a" {
		t.Fatalf("expected the single same-year hit for an original-title query, got %v %+v", ok, it)
	}
	// Franchise-wide query must not qualify (not the original title).
	if _, ok := pickByOriginalQueryYear(items, "star wars", req); ok {
		t.Fatal("franchise query must not match by year alone")
	}
	// Two same-year hits → ambiguous → no.
	two := append(items, provider.SearchItem{ID: "b", Title: "Другой фильм", Year: 2015})
	if _, ok := pickByOriginalQueryYear(two, "star wars the force awakens", req); ok {
		t.Fatal("ambiguous same-year hits must not match")
	}
}
