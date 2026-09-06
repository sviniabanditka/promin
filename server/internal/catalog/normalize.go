package catalog

import (
	"sort"
	"strings"
)

// imgPath turns a raw TMDB image path (e.g. "8kSerJrhrJWKLk1LViesGcnrUPE.jpg")
// into a relative path through our own /img proxy, per docs/api.md
// section 6 ("клиент не должен знать про TMDB"). Posters use w500,
// backdrops use original — the exact sizes from the canonical DTO example
// in docs/api.md; the /img/{size}/... proxy itself accepts any TMDB size
// token, so callers/clients needing other widths (e.g. thumbnails) can
// still request them directly.
func imgPath(size, tmdbPath string) string {
	if tmdbPath == "" {
		return ""
	}
	return "/img/" + size + "/" + strings.TrimPrefix(tmdbPath, "/")
}

func yearFromDate(date string) int {
	if len(date) < 4 {
		return 0
	}
	var y int
	for _, c := range date[:4] {
		if c < '0' || c > '9' {
			return 0
		}
		y = y*10 + int(c-'0')
	}
	return y
}

// normalizeListItem converts a compact TMDB list/search result into the
// canonical Title DTO. mediaType overrides item.MediaType when the source
// endpoint doesn't return one (e.g. /movie/popular, /discover/tv).
func normalizeListItem(item tmdbListItem, mediaType string, genreNames map[int]string) Title {
	if mediaType == "" {
		mediaType = item.MediaType
	}

	title := item.Title
	original := item.OriginalTitle
	date := item.ReleaseDate
	if mediaType == "tv" {
		title = item.Name
		original = item.OriginalName
		date = item.FirstAirDate
	}

	return Title{
		TMDBID:        item.ID,
		Type:          mediaType,
		Title:         title,
		OriginalTitle: original,
		Year:          yearFromDate(date),
		Rating:        item.VoteAverage,
		// w342 for list cards: they render at ~156px (234px @1080p). w500 was a
		// 3× oversample — ~13 MB of posters on a cold home, decoded on a TV GPU.
		Poster:   imgPath("w342", item.PosterPath),
		Backdrop: imgPath("original", item.BackdropPath),
		// No overview/genres on list cards: no shelf or grid renders them and
		// overview alone was ~40% of the home JSON (120 cards).
		Overview:    "",
		Genres:      nil,
		Timecode:    nil,
		InBookmarks: false,
		Seasons:     nil,
	}
}

// normalizeDetail converts a full TMDB title detail (append_to_response
// applied) into the canonical Title DTO. requestedSeason, if > 0, is the
// only season whose Episodes are populated (see docs/backend.md "season/N —
// только по запросу" in the task brief); seasonDetail must correspond to
// it when provided. withExtras gates Cast/Similar/Recommendations — set
// for the single-title card (Service.Title) and left off for Service.Card
// (shelves/continue_watching), same scoping as ImdbRating.
func normalizeDetail(d tmdbDetail, mediaType string, requestedSeason int, seasonDetail *tmdbSeasonDetail, withExtras bool) Title {
	title := d.Title
	original := d.OriginalTitle
	date := d.ReleaseDate
	if mediaType == "tv" {
		title = d.Name
		original = d.OriginalName
		date = d.FirstAirDate
	}

	genres := make([]string, 0, len(d.Genres))
	for _, g := range d.Genres {
		genres = append(genres, g.Name)
	}

	runtime := d.Runtime
	if mediaType == "tv" && len(d.EpisodeRunTime) > 0 {
		runtime = d.EpisodeRunTime[0]
	}

	var keywords []string
	if mediaType == "movie" {
		for _, k := range d.Keywords.Keywords {
			keywords = append(keywords, k.Name)
		}
	} else {
		for _, k := range d.Keywords.Results {
			keywords = append(keywords, k.Name)
		}
	}

	var ext *ExternalIDs
	if d.ExternalIDs.ImdbID != "" {
		ext = &ExternalIDs{ImdbID: d.ExternalIDs.ImdbID}
	}

	contentRating := extractContentRating(d, mediaType)

	out := Title{
		TMDBID:         d.ID,
		Type:           mediaType,
		Title:          title,
		OriginalTitle:  original,
		Year:           yearFromDate(date),
		Rating:         d.VoteAverage,
		Poster:         imgPath("w500", d.PosterPath),
		Backdrop:       imgPath("original", d.BackdropPath),
		Overview:       d.Overview,
		Genres:         genres,
		RuntimeMinutes: runtime,
		ContentRating:  contentRating,
		Keywords:       keywords,
		ExternalIDs:    ext,
		Timecode:       nil,
		InBookmarks:    false,
	}

	if mediaType == "tv" {
		out.Seasons = make([]Season, 0, len(d.Seasons))
		for _, s := range d.Seasons {
			season := Season{Season: s.SeasonNumber, Name: s.Name, Episodes: []Episode{}}
			if requestedSeason > 0 && s.SeasonNumber == requestedSeason && seasonDetail != nil {
				for _, e := range seasonDetail.Episodes {
					season.Episodes = append(season.Episodes, Episode{
						Episode:        e.EpisodeNumber,
						Name:           e.Name,
						AirDate:        e.AirDate,
						Still:          imgPath("w300", e.StillPath),
						Overview:       e.Overview,
						RuntimeMinutes: e.Runtime,
						Rating:         e.VoteAverage,
						Timecode:       nil,
					})
				}
			}
			out.Seasons = append(out.Seasons, season)
		}
	}

	if withExtras {
		out.Cast = normalizeCast(d.Credits.Cast)
		out.Similar = normalizeRelated(d.Similar, mediaType)
		out.Recommendations = normalizeRelated(d.Recommendations, mediaType)
		out.Trailers = normalizeTrailers(d.Videos.Results)
	}

	return out
}

// maxRelatedItems / maxCastMembers cap the "similar"/"recommendations"/
// "cast" lists at a Lampa-parity size — TMDB can return dozens of
// results, most below relevance and not worth the payload weight.
const (
	maxCastMembers  = 20
	maxRelatedItems = 20
	// maxTrailers caps the "trailers" list at a reasonable card size — TMDB
	// videos can include many teasers/clips/featurettes in every language
	// it has entries for, most not worth surfacing.
	maxTrailers = 12
)

// normalizeCast converts TMDB's credits.cast (already order-sorted by
// TMDB) into the top maxCastMembers Person entries for the card.
func normalizeCast(cast []tmdbCastMember) []Person {
	if len(cast) == 0 {
		return nil
	}
	n := len(cast)
	if n > maxCastMembers {
		n = maxCastMembers
	}
	out := make([]Person, 0, n)
	for _, c := range cast[:n] {
		out = append(out, Person{
			ID:        c.ID,
			Name:      c.Name,
			Character: c.Character,
			Photo:     imgPath("w185", c.ProfilePath),
		})
	}
	return out
}

// normalizeRelated converts a similar/recommendations tmdbListResponse
// into lightweight Title cards. mediaType is the requested title's type —
// TMDB's /movie/{id}/similar and /movie/{id}/recommendations only ever
// return movies (mirrored for /tv/{id}), and result items don't reliably
// carry media_type here, so the requested type is used directly rather
// than item.MediaType.
func normalizeRelated(resp tmdbListResponse, mediaType string) []Title {
	if len(resp.Results) == 0 {
		return nil
	}
	n := len(resp.Results)
	if n > maxRelatedItems {
		n = maxRelatedItems
	}
	out := make([]Title, 0, n)
	for _, it := range resp.Results[:n] {
		// No genre-map round-trip for similar/recommendations (task brief
		// item 4: one request, no extra round-trips) — Genres comes back
		// as an empty (non-nil) slice via mapGenreNames(ids, nil), same as
		// every other Title-typed field that has no source data.
		out = append(out, normalizeListItem(it, mediaType, nil))
	}
	return out
}

// normalizeTrailers converts TMDB's videos.results (append_to_response=
// videos, for the requested language) into the top maxTrailers YouTube
// Trailer entries. Only site=="YouTube" entries are considered — TMDB
// also returns Vimeo/other-site videos we have no player story for.
// Official trailers sort first, then teasers, then everything else
// (clips/featurettes/etc.), each group keeping TMDB's own result order.
// No extra request is made for a fallback language — this only uses
// whatever the single detail request (already scoped to the requested
// language) returned (task brief item 4).
func normalizeTrailers(videos []tmdbVideo) []Trailer {
	yt := make([]tmdbVideo, 0, len(videos))
	for _, v := range videos {
		if v.Site == "YouTube" && v.Key != "" {
			yt = append(yt, v)
		}
	}
	if len(yt) == 0 {
		return nil
	}

	sort.SliceStable(yt, func(i, j int) bool {
		return trailerRank(yt[i]) < trailerRank(yt[j])
	})

	n := len(yt)
	if n > maxTrailers {
		n = maxTrailers
	}
	out := make([]Trailer, 0, n)
	for _, v := range yt[:n] {
		out = append(out, Trailer{
			Key:        v.Key,
			Name:       v.Name,
			Type:       v.Type,
			Lang:       v.Iso6391,
			YoutubeURL: "https://www.youtube.com/watch?v=" + v.Key,
		})
	}
	return out
}

// trailerRank orders official trailers first, then any trailer, then
// teasers, then everything else — lower ranks first.
func trailerRank(v tmdbVideo) int {
	switch {
	case v.Type == "Trailer" && v.Official:
		return 0
	case v.Type == "Trailer":
		return 1
	case v.Type == "Teaser" && v.Official:
		return 2
	case v.Type == "Teaser":
		return 3
	default:
		return 4
	}
}

// extractContentRating picks a certification/rating string, preferring UA
// then US, falling back to the first non-empty entry found. TMDB doesn't
// return Ukrainian certifications for most titles, so this usually falls
// through to US or empty — acceptable for Phase 1 (see README deviations).
func extractContentRating(d tmdbDetail, mediaType string) string {
	if mediaType == "movie" {
		var us, first string
		for _, r := range d.ReleaseDates.Results {
			for _, rd := range r.ReleaseDates {
				if rd.Certification == "" {
					continue
				}
				if r.ISO31661 == "UA" {
					return rd.Certification
				}
				if r.ISO31661 == "US" && us == "" {
					us = rd.Certification
				}
				if first == "" {
					first = rd.Certification
				}
			}
		}
		if us != "" {
			return us
		}
		return first
	}

	var us, first string
	for _, r := range d.ContentRatings.Results {
		if r.Rating == "" {
			continue
		}
		if r.ISO31661 == "UA" {
			return r.Rating
		}
		if r.ISO31661 == "US" && us == "" {
			us = r.Rating
		}
		if first == "" {
			first = r.Rating
		}
	}
	if us != "" {
		return us
	}
	return first
}
