package catalog

// Random backdrops for the idle screensaver.
//
// TMDB has no "give me a random title" endpoint, so randomness comes from
// asking /discover for a RANDOM PAGE. That draws from the whole catalog rather
// than from whatever happens to be on the home screen.
//
// Two deliberate limits, both about how the result LOOKS on a 10-foot screen:
//   - a vote-count floor, because the deep tail of the catalog is mostly
//     entries with no backdrop, placeholder art or mis-tagged images;
//   - a page ceiling, because past it the same tail dominates.
// Loosen randomPageCeiling / backdropVoteFloor to trade beauty for breadth.

import (
	"context"
	"math/rand"
	"net/url"
	"strconv"
)

const (
	// randomPageCeiling bounds the random page. TMDB itself caps /discover at
	// 500 pages; staying lower keeps titles recognizable.
	randomPageCeiling = 60
	// backdropVoteFloor filters out the untended tail (no/awful artwork).
	backdropVoteFloor = "50"
	// backdropsPerRequest is how many pages we may touch to fill a batch. Each
	// page is a cached list, so repeat activations are usually free.
	backdropsMaxPages = 4
)

// Backdrop is one screensaver image plus the title it belongs to, so the
// screensaver can name what is on screen instead of being anonymous wallpaper.
type Backdrop struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
}

// RandomBackdrops returns up to want backdrops (paths are already /img/... so
// the client fetches them through our own caching proxy) drawn from random
// pages of the whole catalog. Never errors: a failure yields fewer (or no)
// images and the screensaver falls back to clock-on-black.
func (s *Service) RandomBackdrops(ctx context.Context, lang string, want int) []Backdrop {
	if want <= 0 {
		want = 40
	}
	out := make([]Backdrop, 0, want)
	seen := map[string]bool{}

	for i := 0; i < backdropsMaxPages && len(out) < want; i++ {
		mediaType := "movie"
		path := "/discover/movie"
		// Mix in series: their backdrops are just as good and it widens the pool.
		if rand.Intn(3) == 0 {
			mediaType, path = "tv", "/discover/tv"
		}
		page := rand.Intn(randomPageCeiling) + 1

		q := url.Values{
			"sort_by":        {"popularity.desc"},
			"vote_count.gte": {backdropVoteFloor},
			"include_adult":  {"false"},
		}
		key := "list:backdrops:" + mediaType + ":" + strconv.Itoa(page) + ":" + normalizeLang(lang)
		resp, err := s.client.fetchList(ctx, key, path, lang, q, page)
		if err != nil {
			continue // random page failed — try another
		}
		for _, it := range resp.Results {
			if it.BackdropPath == "" {
				continue
			}
			// "original" matches what cards use, so the /img cache is shared
			// with the rest of the app instead of storing a second size.
			// w1280, not original: a real original was 966 KB vs 137 KB, decoded
			// at 4K on a TV GPU every 20s; and the title screen's backdrop layer
			// already uses w1280, so the /img disk cache is shared.
			u := imgPath("w1280", it.BackdropPath)
			if u == "" || seen[u] {
				continue
			}
			seen[u] = true
			name := it.Title
			if name == "" {
				name = it.Name // tv entries carry `name`, movies `title`
			}
			date := it.ReleaseDate
			if date == "" {
				date = it.FirstAirDate
			}
			out = append(out, Backdrop{URL: u, Title: name, Year: yearFromDate(date)})
			if len(out) >= want {
				break
			}
		}
	}

	// Shuffle so the order isn't page order (which is popularity order).
	rand.Shuffle(len(out), func(a, b int) { out[a], out[b] = out[b], out[a] })
	return out
}
