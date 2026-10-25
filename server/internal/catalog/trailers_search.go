package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Trailers in a given language beyond what TMDB has: an ANONYMOUS YouTube
// results-page search ("<title> трейлер українською"), parsed from the
// embedded ytInitialData. No account, no API key, no relation to the YouTube
// section's signed-in client — playback stays the same iframe embed the
// trailers modal always used. YouTube serves the results page to the VPS IP
// (verified 2026-10-25); a block just yields an empty list.

const (
	trailerSearchTTL     = 7 * 24 * time.Hour
	trailerSearchTimeout = 8 * time.Second
	maxSearchedTrailers  = 6
	ytResultsURL         = "https://www.youtube.com/results"
	// sp=EgIQAQ== is YouTube's "type: video" filter.
	ytVideoFilter = "EgIQAQ%3D%3D"
	browserUA     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
)

var (
	ytInitialDataRe = regexp.MustCompile(`ytInitialData\s*=\s*(\{.*?\});</script>`)
	trailerWordRe   = regexp.MustCompile(`(?i)трейлер|тизер|trailer|teaser`)
)

// trailerQuery builds the search phrase: the title in that language plus the
// language's own word for "trailer" (so uk results are actually dubbed uk).
func trailerQuery(title string, year int, lang string) string {
	var suffix string
	switch normalizeLang(lang) {
	case "ru":
		suffix = "трейлер на русском"
	case "en":
		suffix = "official trailer"
	default:
		suffix = "трейлер українською"
	}
	q := title
	if year > 0 {
		q += " " + strconv.Itoa(year)
	}
	return q + " " + suffix
}

// SearchTrailers returns up to maxSearchedTrailers YouTube trailers for the
// title in lang, cached for trailerSearchTTL (an empty result is cached too —
// no point hammering YouTube for a title nobody dubbed).
func (s *Service) SearchTrailers(ctx context.Context, mediaType string, id int, lang string) ([]Trailer, error) {
	lang = normalizeLang(lang)
	key := fmt.Sprintf("yt-trailers:v1:%s:%d:%s", mediaType, id, lang)
	now := time.Now().Unix()
	if cached, err := s.client.cache.Get(key); err == nil && cached.ExpiresAt > now {
		var out []Trailer
		if json.Unmarshal([]byte(cached.JSON), &out) == nil {
			return out, nil
		}
	}

	t, err := s.title(ctx, mediaType, id, 0, lang, false)
	if err != nil {
		return nil, err
	}
	title := t.Title
	if title == "" {
		title = t.OriginalTitle
	}
	if title == "" {
		return nil, nil
	}

	cctx, cancel := context.WithTimeout(ctx, trailerSearchTimeout)
	defer cancel()
	page, err := fetchYouTubeResults(cctx, s.client.httpClient, trailerQuery(title, t.Year, lang), lang)
	if err != nil {
		s.client.logger.Warn("trailers: youtube search failed", "tmdb_id", id, "lang", lang, "error", err)
		return nil, nil
	}
	out := parseYouTubeTrailers(page, lang)
	if raw, e := json.Marshal(out); e == nil {
		_ = s.client.cache.Set(key, string(raw), now+int64(trailerSearchTTL.Seconds()))
	}
	return out, nil
}

func fetchYouTubeResults(ctx context.Context, hc *http.Client, query, lang string) ([]byte, error) {
	u := ytResultsURL + "?search_query=" + url.QueryEscape(query) + "&sp=" + ytVideoFilter + "&hl=" + lang
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept-Language", lang)
	// Skip the EU consent interstitial.
	req.Header.Set("Cookie", "CONSENT=YES+; SOCS=CAI")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// parseYouTubeTrailers walks ytInitialData for videoRenderer nodes and keeps
// the ones that look like trailers (title word, under 10 minutes).
func parseYouTubeTrailers(page []byte, lang string) []Trailer {
	m := ytInitialDataRe.FindSubmatch(page)
	if m == nil {
		return nil
	}
	var root any
	if json.Unmarshal(m[1], &root) != nil {
		return nil
	}
	out := make([]Trailer, 0, maxSearchedTrailers)
	seen := map[string]bool{}
	var walk func(any)
	walk = func(n any) {
		if len(out) >= maxSearchedTrailers {
			return
		}
		switch v := n.(type) {
		case map[string]any:
			if vr, ok := v["videoRenderer"].(map[string]any); ok {
				if tr, ok := trailerFromRenderer(vr, lang); ok && !seen[tr.Key] {
					seen[tr.Key] = true
					out = append(out, tr)
				}
				return
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(root)
	return out
}

func trailerFromRenderer(vr map[string]any, lang string) (Trailer, bool) {
	id, _ := vr["videoId"].(string)
	if len(id) != 11 {
		return Trailer{}, false
	}
	var name string
	if t, ok := vr["title"].(map[string]any); ok {
		if runs, ok := t["runs"].([]any); ok {
			for _, r := range runs {
				if rm, ok := r.(map[string]any); ok {
					s, _ := rm["text"].(string)
					name += s
				}
			}
		}
	}
	if !trailerWordRe.MatchString(name) {
		return Trailer{}, false
	}
	if lt, ok := vr["lengthText"].(map[string]any); ok {
		if st, ok := lt["simpleText"].(string); ok && !shortEnough(st) {
			return Trailer{}, false
		}
	}
	return Trailer{Key: id, Name: name, Type: "Trailer", Lang: lang, YoutubeURL: "https://www.youtube.com/watch?v=" + id}, true
}

// shortEnough: "2:21" / "12:05" ok, "1:02:10" (hours) or ≥10 minutes not.
func shortEnough(length string) bool {
	parts := strings.Split(length, ":")
	if len(parts) > 2 {
		return false
	}
	if len(parts) == 2 {
		min, err := strconv.Atoi(parts[0])
		return err == nil && min < 10
	}
	return true
}
