package sources

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// jacredTimeout bounds the JacRed search round-trip through Lampac's
// built-in module (docs/backend.md applies the same
// "promin applies its own deadline" policy here as for /lite/events).
const jacredTimeout = 12 * time.Second

// jacredItem mirrors one entry of Lampac's built-in JacRed
// GET /api/v1.0/torrents?search=... response, verified against the
// production instance (see task brief): tracker, url, title, size (bytes,
// float), createTime, sid (seeders), pir (peers), magnet, quality (int),
// voices[], seasons[].
// createTime is deliberately omitted: the production Lampac instance
// returns it as an ISO8601 string ("2026-05-22T08:39:40"), not the
// unix-int the task brief described. Promin doesn't use the field, so it's
// left off the struct entirely — json.Unmarshal ignores JSON keys with no
// matching Go field, which sidesteps depending on its exact shape.
type jacredItem struct {
	Tracker string   `json:"tracker"`
	URL     string   `json:"url"`
	Title   string   `json:"title"`
	Size    float64  `json:"size"`
	Seeders int      `json:"sid"`
	Peers   int      `json:"pir"`
	Magnet  string   `json:"magnet"`
	Quality int      `json:"quality"`
	Voices  []string `json:"voices"`
	Seasons []int    `json:"seasons"`
}

// TorrentResult is one normalized entry of GET /api/v1/sources/torrents,
// per docs/api.md MagnetID is an opaque token the
// client passes back to POST /api/v1/torrents/add — the raw magnet link is
// never exposed to the client (task brief item 3).
type TorrentResult struct {
	Title     string   `json:"title"`
	Tracker   string   `json:"tracker"`
	Size      int64    `json:"size"`
	SizeHuman string   `json:"size_human"`
	Seeders   int      `json:"seeders"`
	Peers     int      `json:"peers"`
	Quality   string   `json:"quality"`
	Voices    []string `json:"voices,omitempty"`
	// opaque id the client passes to /torrents/add?id= (never the raw magnet)
	MagnetID string `json:"id"`
}

// TorrentsRequest bundles the query params for GET /api/v1/sources/torrents.
type TorrentsRequest struct {
	TMDBID        int
	Type          string // movie|tv
	Title         string
	OriginalTitle string
	Year          int
	Season        int
	Episode       int
}

// TorrentsResponse is the body of GET /api/v1/sources/torrents.
type TorrentsResponse struct {
	Degraded bool            `json:"degraded"`
	Torrents []TorrentResult `json:"torrents"`
}

// EncodeMagnetID wraps a raw magnet URI into an opaque token, same
// base64url scheme as EncodeRelayURL — "opaque" here just means "not a
// bare magnet: link a client could act on directly without going through
// our /api/v1/torrents/add", not cryptographically hidden.
func EncodeMagnetID(magnet string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(magnet))
}

// DecodeMagnetID reverses EncodeMagnetID.
func DecodeMagnetID(id string) (string, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(id); err == nil {
		return string(decoded), nil
	}
	decoded, err := base64.URLEncoding.DecodeString(id)
	if err != nil {
		return "", fmt.Errorf("sources: invalid magnet_id: %w", err)
	}
	return string(decoded), nil
}

// Torrents queries JacRed (via Lampac) for magnet results matching the
// title, normalizes and sorts them by seeders (docs/api.md section
// 3), and filters obvious junk (zero-seed results sink to the bottom
// rather than being dropped outright — a stale/rare release with zero
// current seeders can still be the only copy around).
func (s *Service) Torrents(ctx context.Context, req TorrentsRequest) TorrentsResponse {
	ctx, cancel := context.WithTimeout(ctx, jacredTimeout)
	defer cancel()

	query := req.Title
	if query == "" {
		query = req.OriginalTitle
	}

	// Deliberately NOT appending req.Year to the query: empirically (see
	// task verification run against the production Lampac instance),
	// JacRed's own search does fuzzy title matching across its synced
	// index and "<title> <year>" as one string returns zero hits even
	// when "<title>" alone matches plenty — the index doesn't consistently
	// have the year embedded in indexed titles. Year isn't otherwise used
	// to filter/rank results here (this Phase doesn't have a reliable
	// signal for it per-result), but is kept in TorrentsRequest for a
	// future ranking pass.
	items, err := s.searchTorrentsCached(ctx, query)
	if err != nil {
		s.logger.Warn("sources: jacred search failed", "query", query, "error", err)
		return TorrentsResponse{Degraded: true, Torrents: []TorrentResult{}}
	}

	out := s.mapTorrents(items, req)

	// JacRed indexes original/Russian release names; a localized (e.g. uk)
	// title often returns nothing. If the localized search came back empty and
	// the original title differs, retry with it and prefer the non-empty set.
	if len(out) == 0 && req.OriginalTitle != "" && req.OriginalTitle != query {
		if alt, altErr := s.searchTorrentsCached(ctx, req.OriginalTitle); altErr == nil {
			out = s.mapTorrents(alt, req)
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Seeders > out[j].Seeders })
	return TorrentsResponse{Degraded: false, Torrents: out}
}

// mapTorrents filters and converts raw JacRed items into TorrentResults, applying
// the series-season filter (only when the release declares a season list).
const torrentsCacheTTL = 30 * time.Minute

// searchTorrentsCached: jac.red rate-limits per IP (429 on the second query
// within a second — a title page fires two: UA title, then original). Cache
// by query and back off once on 429; lampac's JacRed did the caching for us.
func (s *Service) searchTorrentsCached(ctx context.Context, query string) ([]jacredItem, error) {
	key := "jacred:" + strings.ToLower(strings.TrimSpace(query))
	if s.nativeCache != nil {
		if raw, ok := s.nativeCache.Get(key); ok {
			var items []jacredItem
			if json.Unmarshal([]byte(raw), &items) == nil {
				return items, nil
			}
		}
	}
	items, err := s.client.SearchTorrents(ctx, query)
	if err != nil && strings.Contains(err.Error(), "status 429") {
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(2 * time.Second):
		}
		items, err = s.client.SearchTorrents(ctx, query)
	}
	if err != nil {
		return nil, err
	}
	if s.nativeCache != nil {
		if raw, e := json.Marshal(items); e == nil {
			s.nativeCache.Set(key, string(raw), torrentsCacheTTL)
		}
	}
	return items, nil
}

var btihRe = regexp.MustCompile(`(?i)urn:btih:([0-9a-z]+)`)

// mapTorrents converts and de-duplicates. A public JacRed (jac.red) returns
// the same release once per tracker; lampac used to merge those for us
// ("mergeduplicates"). Same infohash → one row, trackers joined, best seeders.
func (s *Service) mapTorrents(items []jacredItem, req TorrentsRequest) []TorrentResult {
	out := make([]TorrentResult, 0, len(items))
	byHash := map[string]int{} // btih → index in out
	for _, it := range items {
		if it.Magnet == "" {
			continue
		}
		if req.Type == "tv" && req.Season > 0 && len(it.Seasons) > 0 && !containsInt(it.Seasons, req.Season) {
			continue
		}
		if m := btihRe.FindStringSubmatch(it.Magnet); m != nil {
			hash := strings.ToLower(m[1])
			if i, dup := byHash[hash]; dup {
				prev := &out[i]
				if !strings.Contains(prev.Tracker, it.Tracker) {
					prev.Tracker += ", " + it.Tracker
				}
				if it.Seeders > prev.Seeders {
					prev.Seeders, prev.Peers = it.Seeders, it.Peers
				}
				continue
			}
			byHash[hash] = len(out)
		}
		out = append(out, TorrentResult{
			Title:     it.Title,
			Tracker:   it.Tracker,
			Size:      int64(it.Size),
			SizeHuman: humanSize(int64(it.Size)),
			Seeders:   it.Seeders,
			Peers:     it.Peers,
			Quality:   qualityLabel(it.Quality),
			Voices:    it.Voices,
			MagnetID:  EncodeMagnetID(it.Magnet),
		})
	}
	return out
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func qualityLabel(q int) string {
	if q <= 0 {
		return ""
	}
	return strconv.Itoa(q) + "p"
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// SearchTorrents calls Lampac's built-in JacRed endpoint,
// GET /api/v1.0/torrents?search=<query>&apikey=, per the task brief
// (verified against the production Lampac instance).
func (c *Client) SearchTorrents(ctx context.Context, query string) ([]jacredItem, error) {
	q := url.Values{}
	q.Set("search", query)
	q.Set("apikey", c.jacredKey)

	body, err := c.get(ctx, c.jacredURL+"/api/v1.0/torrents", q)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}

	var items []jacredItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("sources: decode jacred response: %w", err)
	}
	return items, nil
}
