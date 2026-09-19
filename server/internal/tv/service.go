// Package tv is the live-TV section (docs/tv.md): the iptv-org catalogue
// (free-to-air channels, community-maintained) filtered to a few countries,
// synced daily from the project's JSON API, each stream checked for liveness
// on our own schedule, and served to the TV/Mini App as channel lists plus a
// playable URL per channel.
//
// What is stored is data, not code: iptv-org publishes channels.json,
// streams.json, logos.json, categories.json, countries.json and a DMCA/NSFW
// blocklist at https://iptv-org.github.io/api/. Everything here works the
// same for an imported M3U from a paid provider later — same tables.
package tv

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

const (
	apiBase       = "https://iptv-org.github.io/api/"
	syncEvery     = 24 * time.Hour
	checkParallel = 12
	checkTimeout  = 10 * time.Second
	// A stream is "playable in the TV" when it is HLS: hls.js only. Raw
	// MPEG-TS / DASH / RTMP are skipped at import.
	hlsHint = "m3u8"
)

type Country struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Flag     string `json:"flag"`
	Channels int    `json:"channels"`
}

type Category struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Channels int    `json:"channels"`
}

// Play is what the client needs to start a channel.
type Play struct {
	URL     string `json:"url"`     // absolute upstream URL (direct) or our /relay path
	Direct  bool   `json:"direct"`  // true: the TV fetches the upstream itself (residential IP, no relay cost)
	Quality string `json:"quality"` // of the chosen stream
	Streams int    `json:"streams"` // alive alternatives
}

type Service struct {
	repo      *store.TVRepo
	http      *http.Client
	log       *slog.Logger
	countries []string
	relay     func(rawURL, ua, ref string) string // builds the /relay path

	mu         sync.Mutex
	countries_ []Country // names/flags from countries.json, cached
	categories []Category
	busy       sync.Map // admin-triggered resyncs in flight, by kind
}

// Countries configured (upper-case ISO codes).
func (s *Service) Countries() []string {
	out := make([]string, 0, len(s.countries))
	for _, c := range s.countries {
		out = append(out, strings.ToUpper(c))
	}
	return out
}

// Resync runs one maintenance job in the background (admin panel): "catalogue"
// (sync + liveness), "check" (liveness) or "epg". False when already running.
func (s *Service) Resync(kind string) bool {
	if _, running := s.busy.LoadOrStore(kind, true); running {
		return false
	}
	go func() {
		defer s.busy.Delete(kind)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		var err error
		switch kind {
		case "catalogue":
			if err = s.Sync(ctx); err == nil {
				_, _ = s.RematchEPG()
				s.Check(ctx)
			}
		case "check":
			s.Check(ctx)
		case "epg":
			err = s.SyncEPG(ctx)
		}
		if err != nil {
			s.log.Warn("tv: resync failed", "kind", kind, "error", err)
		}
	}()
	return true
}

// New: countries is the ISO list to keep (e.g. UA,RU,GB,US); relay wraps an
// upstream URL into our relay path (httpapi owns the encoding).
func New(repo *store.TVRepo, countries []string, relay func(rawURL, ua, ref string) string, logger *slog.Logger) *Service {
	return &Service{
		repo:      repo,
		http:      &http.Client{Timeout: 90 * time.Second},
		log:       logger,
		countries: countries,
		relay:     relay,
	}
}

func (s *Service) Enabled() bool { return s != nil && len(s.countries) > 0 }

// Repo exposes favourites for the handlers.
func (s *Service) Repo() *store.TVRepo { return s.repo }

// Run syncs when the catalogue is missing or older than a day, then checks
// liveness; repeats daily. Nil-safe.
func (s *Service) Run(ctx context.Context) {
	if !s.Enabled() {
		return
	}
	for {
		if s.stale() {
			if err := s.Sync(ctx); err != nil {
				s.log.Warn("tv: sync failed", "error", err)
			} else {
				_, _ = s.RematchEPG() // the catalogue was rebuilt: re-point channels at their guides
				s.Check(ctx)
			}
		} else if s.checkStale() {
			s.Check(ctx)
		}
		if s.epgStale() {
			if err := s.SyncEPG(ctx); err != nil {
				s.log.Warn("tv: epg sync failed", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

func (s *Service) stale() bool {
	var at int64
	fmt.Sscan(s.repo.Meta("synced_at"), &at)
	return time.Since(time.Unix(at, 0)) > syncEvery
}

func (s *Service) checkStale() bool {
	var at int64
	fmt.Sscan(s.repo.Meta("checked_at"), &at)
	return time.Since(time.Unix(at, 0)) > syncEvery
}

// ---- sync --------------------------------------------------------------------

type apiChannel struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Network    *string  `json:"network"`
	Country    string   `json:"country"`
	Categories []string `json:"categories"`
	IsNSFW     bool     `json:"is_nsfw"`
	Closed     *string  `json:"closed"`
	ReplacedBy *string  `json:"replaced_by"`
	Website    *string  `json:"website"`
	AltNames   []string `json:"alt_names"`
}

type apiStream struct {
	Channel   *string `json:"channel"`
	URL       string  `json:"url"`
	Quality   *string `json:"quality"`
	UserAgent *string `json:"user_agent"`
	Referrer  *string `json:"referrer"`
}

type apiLogo struct {
	Channel string  `json:"channel"`
	Feed    *string `json:"feed"`
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	Format  string  `json:"format"`
	URL     string  `json:"url"`
}

type apiCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type apiCountry struct {
	Name string `json:"name"`
	Code string `json:"code"`
	Flag string `json:"flag"`
}

type apiBlock struct {
	Channel string `json:"channel"`
}

func (s *Service) fetch(ctx context.Context, name string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+name, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "promin (+https://promin.club)")
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d", name, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Sync pulls the iptv-org dumps and replaces the catalogue for the configured
// countries: open channels only, no NSFW, nothing on the blocklist, HLS streams.
func (s *Service) Sync(ctx context.Context) error {
	t0 := time.Now()
	var channels []apiChannel
	var streams []apiStream
	var logos []apiLogo
	var blocklist []apiBlock
	var cats []apiCategory
	var countries []apiCountry
	for name, out := range map[string]any{"channels.json": &channels, "streams.json": &streams, "logos.json": &logos, "blocklist.json": &blocklist, "categories.json": &cats, "countries.json": &countries} {
		if err := s.fetch(ctx, name, out); err != nil {
			return err
		}
	}
	want := map[string]bool{}
	for _, c := range s.countries {
		want[strings.ToUpper(c)] = true
	}
	blocked := map[string]bool{}
	for _, b := range blocklist {
		blocked[b.Channel] = true
	}
	keep := map[string]*store.TVChannel{}
	for _, c := range channels {
		if !want[c.Country] || c.IsNSFW || c.Closed != nil || c.ReplacedBy != nil || blocked[c.ID] {
			continue
		}
		ch := &store.TVChannel{ID: c.ID, Name: c.Name, Country: c.Country, Categories: c.Categories, AltNames: c.AltNames}
		if c.Website != nil {
			ch.Website = *c.Website
		}
		if c.Network != nil {
			ch.Network = *c.Network
		}
		if ch.Categories == nil {
			ch.Categories = []string{}
		}
		keep[c.ID] = ch
	}
	// Best logo per channel: channel-level (no feed) PNG/SVG, widest.
	bestLogo := map[string]apiLogo{}
	for _, l := range logos {
		ch, ok := keep[l.Channel]
		if !ok || l.URL == "" {
			continue
		}
		cur, has := bestLogo[l.Channel]
		score := func(x apiLogo) int {
			v := x.Width
			if x.Feed == nil {
				v += 10000
			}
			if strings.EqualFold(x.Format, "SVG") {
				v += 5000
			}
			return v
		}
		if !has || score(l) > score(cur) {
			bestLogo[l.Channel] = l
		}
		_ = ch
	}
	var chs []store.TVChannel
	for id, ch := range keep {
		if l, ok := bestLogo[id]; ok {
			ch.Logo = l.URL
		}
		chs = append(chs, *ch)
	}
	sort.Slice(chs, func(i, j int) bool { return chs[i].ID < chs[j].ID })
	var sts []store.TVStream
	withStream := map[string]bool{}
	for _, st := range streams {
		if st.Channel == nil {
			continue
		}
		if _, ok := keep[*st.Channel]; !ok {
			continue
		}
		if !strings.Contains(strings.ToLower(st.URL), hlsHint) {
			continue
		}
		if _, err := url.Parse(st.URL); err != nil {
			continue
		}
		row := store.TVStream{ChannelID: *st.Channel, URL: st.URL}
		if st.Quality != nil {
			row.Quality = *st.Quality
		}
		if st.UserAgent != nil {
			row.UserAgent = *st.UserAgent
		}
		if st.Referrer != nil {
			row.Referrer = *st.Referrer
		}
		sts = append(sts, row)
		withStream[*st.Channel] = true
	}
	// Channels without any HLS stream are noise in the grid.
	filtered := chs[:0]
	for _, c := range chs {
		if withStream[c.ID] {
			filtered = append(filtered, c)
		}
	}
	if err := s.repo.Replace(filtered, sts); err != nil {
		return err
	}
	s.mu.Lock()
	s.countries_ = nil
	for _, c := range countries {
		if want[c.Code] {
			s.countries_ = append(s.countries_, Country{Code: c.Code, Name: c.Name, Flag: c.Flag})
		}
	}
	s.categories = nil
	for _, c := range cats {
		s.categories = append(s.categories, Category{ID: c.ID, Name: c.Name})
	}
	s.mu.Unlock()
	_ = s.repo.SetMeta("countries", mustJSON(s.countries_))
	_ = s.repo.SetMeta("categories", mustJSON(s.categories))
	s.log.Info("tv: catalogue synced", "channels", len(filtered), "streams", len(sts), "ms", time.Since(t0).Milliseconds())
	return nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---- liveness -----------------------------------------------------------------

// Check fetches the first bytes of every stream (12 at a time) and records the
// verdict: HLS must answer 2xx with a playlist body.
func (s *Service) Check(ctx context.Context) {
	all, err := s.repo.AllStreams()
	if err != nil {
		s.log.Warn("tv: check: list streams", "error", err)
		return
	}
	t0 := time.Now()
	var alive, dead int64
	var mu sync.Mutex
	sem := make(chan struct{}, checkParallel)
	var wg sync.WaitGroup
	client := &http.Client{Timeout: checkTimeout}
	for _, st := range all {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(st store.TVStream) {
			defer wg.Done()
			defer func() { <-sem }()
			ok, cors := probe(ctx, client, st)
			_ = s.repo.MarkStream(st.ID, ok, cors)
			mu.Lock()
			if ok {
				alive++
			} else {
				dead++
			}
			mu.Unlock()
		}(st)
	}
	wg.Wait()
	_ = s.repo.SetMeta("checked_at", fmt.Sprint(time.Now().Unix()))
	s.log.Info("tv: streams checked", "alive", alive, "dead", dead, "ms", time.Since(t0).Milliseconds())
}

// probe: alive = 2xx + "#EXTM3U"; cors = the upstream allows any origin, so
// hls.js in the TV's browser may fetch it directly (otherwise /relay).
func probe(ctx context.Context, c *http.Client, st store.TVStream) (alive, cors bool) {
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, st.URL, nil)
	if err != nil {
		return false, false
	}
	req.Header.Set("User-Agent", firstNonEmpty(st.UserAgent, "Mozilla/5.0 (SMART-TV; Linux; Tizen 5.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/69.0.3497.106 Safari/537.36"))
	if st.Referrer != "" {
		req.Header.Set("Referer", st.Referrer)
	}
	resp, err := c.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, false
	}
	head, _ := bufio.NewReader(io.LimitReader(resp.Body, 4096)).Peek(4096)
	alive = strings.HasPrefix(strings.TrimSpace(string(head)), "#EXTM3U")
	return alive, resp.Header.Get("Access-Control-Allow-Origin") == "*"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---- queries ------------------------------------------------------------------

// Meta lists countries and categories with alive-channel counts.
// allowed = the profile's country restriction (nil = none).
func (s *Service) Meta(allowed []string) ([]Country, []Category, error) {
	byCountry, byCat, err := s.repo.Counts(allowed)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	if s.countries_ == nil {
		_ = json.Unmarshal([]byte(s.repo.Meta("countries")), &s.countries_)
		_ = json.Unmarshal([]byte(s.repo.Meta("categories")), &s.categories)
	}
	countries := append([]Country(nil), s.countries_...)
	cats := append([]Category(nil), s.categories...)
	s.mu.Unlock()
	// Countries in the configured order; only the ones with channels.
	out := []Country{}
	for _, code := range s.countries {
		for _, c := range countries {
			if strings.EqualFold(c.Code, code) && byCountry[c.Code] > 0 {
				c.Channels = byCountry[c.Code]
				out = append(out, c)
			}
		}
	}
	outCats := []Category{}
	for _, c := range cats {
		if byCat[c.ID] > 0 {
			c.Channels = byCat[c.ID]
			outCats = append(outCats, c)
		}
	}
	sort.Slice(outCats, func(i, j int) bool { return outCats[i].Channels > outCats[j].Channels })
	return out, outCats, nil
}

func (s *Service) Channels(f store.TVFilter) ([]store.TVChannel, error) {
	if f.Country != "" {
		ok := false
		for _, c := range s.countries {
			ok = ok || strings.EqualFold(c, f.Country)
		}
		if !ok {
			return []store.TVChannel{}, nil
		}
		f.Country = strings.ToUpper(f.Country)
	}
	items, err := s.repo.Channels(f)
	for i := range items {
		// Served through our cache (httpapi /img/tv/{id}); the raw URL stays in
		// the store for the proxy to fetch.
		if items[i].Logo != "" {
			items[i].Logo = "/img/tv/" + url.PathEscape(items[i].ID)
		}
	}
	return items, err
}

// Play picks the best alive stream. Direct when the TV can fetch it itself
// (https, no special headers — the page is https, so http would be mixed
// content); otherwise through our relay with the stream's UA/Referer.
func (s *Service) Play(userID int64, channelID string) (Play, error) {
	sts, err := s.repo.Streams(channelID)
	if err != nil {
		return Play{}, err
	}
	var pick *store.TVStream
	alive := 0
	for i := range sts {
		if sts[i].Alive {
			alive++
			if pick == nil {
				pick = &sts[i]
			}
		}
	}
	if pick == nil && len(sts) > 0 {
		pick = &sts[0] // all marked dead: still let the user try
	}
	if pick == nil {
		return Play{}, ErrNoStream
	}
	_ = s.repo.Touch(userID, channelID)
	direct := pick.CORS && strings.HasPrefix(pick.URL, "https://") && pick.UserAgent == "" && pick.Referrer == ""
	p := Play{Direct: direct, Quality: pick.Quality, Streams: alive}
	if direct {
		p.URL = pick.URL
	} else {
		p.URL = s.relay(pick.URL, pick.UserAgent, pick.Referrer)
	}
	return p, nil
}

// StreamFor hands the recorder (internal/dvr) the raw upstream URL of a
// channel plus its headers. Unlike Play this never wraps the URL in /relay:
// ffmpeg runs on the server, so it fetches the origin itself — one hop less
// and no media token to mint for a job nobody is watching.
func (s *Service) StreamFor(channelID string) (url, userAgent, referer string, err error) {
	sts, err := s.repo.Streams(channelID)
	if err != nil {
		return "", "", "", err
	}
	var pick *store.TVStream
	for i := range sts {
		if sts[i].Alive {
			pick = &sts[i]
			break
		}
	}
	if pick == nil && len(sts) > 0 {
		pick = &sts[0]
	}
	if pick == nil {
		return "", "", "", ErrNoStream
	}
	return pick.URL, pick.UserAgent, pick.Referrer, nil
}

// Report marks the stream the player could not start; the next Check decides.
func (s *Service) Report(channelID string) {
	sts, err := s.repo.Streams(channelID)
	if err != nil {
		return
	}
	for _, st := range sts {
		if st.Alive {
			_ = s.repo.MarkStream(st.ID, false, st.CORS)
			return
		}
	}
}

type tvError string

func (e tvError) Error() string { return string(e) }

const ErrNoStream = tvError("tv: channel has no stream")

// Guide: a channel's programmes from 12 h ago to 36 h ahead.
func (s *Service) Guide(channelID string) ([]store.TVProgram, error) {
	now := time.Now()
	return s.repo.Programs(channelID, now.Add(-12*time.Hour).Unix(), now.Add(36*time.Hour).Unix())
}

// NowNext for every channel with a guide.
func (s *Service) NowNext() (map[string]*store.TVNowNext, error) {
	return s.repo.NowNext(time.Now().Unix())
}
