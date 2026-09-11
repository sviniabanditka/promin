package catalog

// Title is the canonical normalized card DTO, verbatim per
// docs/api.md List responses omit Seasons/Keywords and
// use a nil Timecode (Phase 1 has no auth yet, see README deviations).
type Title struct {
	TMDBID        int     `json:"tmdb_id"`
	Type          string  `json:"type"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Year          int     `json:"year,omitempty"`
	Rating        float64 `json:"rating"`
	// ImdbRating is the IMDB rating fetched from OMDb (omdbapi.com), keyed
	// off ExternalIDs.ImdbID. Only populated for the single-title card
	// (GET /catalog/title/{id}), and only when PROMIN_OMDB_KEY is set —
	// see internal/catalog/omdb_client.go. Zero/omitted everywhere else
	// (list/search/home rows, continue_watching cards).
	ImdbRating     float64      `json:"imdb_rating,omitempty"`
	Poster         string       `json:"poster,omitempty"`
	Backdrop       string       `json:"backdrop,omitempty"`
	Overview       string       `json:"overview,omitempty"`
	Genres         []string     `json:"genres"`
	RuntimeMinutes int          `json:"runtime_minutes,omitempty"`
	ContentRating  string       `json:"content_rating,omitempty"`
	Keywords       []string     `json:"keywords,omitempty"`
	ExternalIDs    *ExternalIDs `json:"external_ids,omitempty"`
	Timecode       *Timecode    `json:"timecode"`
	InBookmarks    bool         `json:"in_bookmarks"`
	Seasons        []Season     `json:"seasons"`

	// Cast, Similar and Recommendations are only populated for the
	// single-title card (GET /catalog/title/{id}, see Service.Title) —
	// list/search/home rows and continue_watching cards (Service.Card)
	// leave them nil, mirroring ImdbRating's scoping above. Sourced from
	// TMDB append_to_response=credits,similar,recommendations on the same
	// detail request (no extra round-trips).
	Cast            []Person  `json:"cast,omitempty"`
	Similar         []Title   `json:"similar,omitempty"`
	Recommendations []Title   `json:"recommendations,omitempty"`
	Trailers        []Trailer `json:"trailers,omitempty"`
}

// Trailer is a YouTube trailer/teaser entry on a title card, sourced from
// TMDB append_to_response=videos (filtered to site=="YouTube"). The
// backend never plays or extracts the YouTube stream itself — that's a
// separate, larger task; this just hands the client a key/url to work
// with (see docs/backend.md task brief item 5).
type Trailer struct {
	Key        string `json:"key"` // YouTube video id
	Name       string `json:"name"`
	Type       string `json:"type"`           // "Trailer" | "Teaser"
	Lang       string `json:"lang,omitempty"` // iso_639_1
	YoutubeURL string `json:"youtube_url"`
}

// Person is a cast member entry on a title card (docs/api.md
// doesn't cover this — Lampa-parity extension per the task brief).
type Person struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Character string `json:"character,omitempty"`
	Photo     string `json:"photo,omitempty"`
	// Department is TMDB's known_for_department ("Acting", "Directing") — set
	// on search hits so the UI can label a person row.
	Department string `json:"department,omitempty"`
}

// PersonDetail is the payload for GET /api/v1/catalog/person/{id}: the person
// plus their filmography as ordinary cards (movies and series they acted in
// or directed/wrote, most popular first).
type PersonDetail struct {
	Person
	Biography    string  `json:"biography,omitempty"`
	Birthday     string  `json:"birthday,omitempty"`
	Deathday     string  `json:"deathday,omitempty"`
	PlaceOfBirth string  `json:"place_of_birth,omitempty"`
	Credits      []Title `json:"credits"`
}

type ExternalIDs struct {
	ImdbID string `json:"imdb_id,omitempty"`
}

type Timecode struct {
	PositionSec float64 `json:"position_sec"`
	DurationSec float64 `json:"duration_sec"`
	// Which episode the spot belongs to (tv only) — lets a shelf card resume
	// even when the client's local timecode cache has evicted the record.
	Season  int `json:"season,omitempty"`
	Episode int `json:"episode,omitempty"`
}

type Season struct {
	Season   int       `json:"season"`
	Name     string    `json:"name"`
	Episodes []Episode `json:"episodes"`
}

type Episode struct {
	Episode        int       `json:"episode"`
	Name           string    `json:"name"`
	AirDate        string    `json:"air_date"`
	Still          string    `json:"still,omitempty"`
	Overview       string    `json:"overview,omitempty"`
	RuntimeMinutes int       `json:"runtime_minutes,omitempty"`
	Rating         float64   `json:"rating,omitempty"`
	Timecode       *Timecode `json:"timecode"`
}

// Row is a single home-page shelf: an id, a localized title and its items.
type Row struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Items []Title `json:"items"`
}

// HomeResponse is the payload for GET /api/v1/catalog/home.
type HomeResponse struct {
	Rows []Row `json:"rows"`
}

// ListResponse is the payload for GET /api/v1/catalog/list and
// GET /api/v1/catalog/search (same shape, per docs/api.md).
type ListResponse struct {
	Page       int     `json:"page"`
	TotalPages int     `json:"total_pages"`
	Items      []Title `json:"items"`
	// People are the person hits of a /search/multi page (search only, in
	// TMDB's relevance order). Absent for movie/tv-typed searches and lists.
	People []Person `json:"people,omitempty"`
}

// GenresResponse is the payload for GET /api/v1/catalog/genres (Promin
// extension, not in the docs/api.md canonical list — see README deviations).
type GenresResponse struct {
	Genres []GenreDTO `json:"genres"`
}

type GenreDTO struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
