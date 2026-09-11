package catalog

// Raw TMDB response shapes (only the fields Promin actually uses). See
// https://developer.themoviedb.org/reference for the full schemas.

type tmdbListItem struct {
	ID            int     `json:"id"`
	MediaType     string  `json:"media_type"` // present on /trending and /search/multi results
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	VoteAverage   float64 `json:"vote_average"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	Overview      string  `json:"overview"`
	GenreIDs      []int   `json:"genre_ids"`
	Popularity    float64 `json:"popularity"`
	// person results of /search/multi
	ProfilePath        string `json:"profile_path"`
	KnownForDepartment string `json:"known_for_department"`
}

type tmdbListResponse struct {
	Page         int            `json:"page"`
	Results      []tmdbListItem `json:"results"`
	TotalPages   int            `json:"total_pages"`
	TotalResults int            `json:"total_results"`
}

type tmdbGenre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type tmdbGenreListResponse struct {
	Genres []tmdbGenre `json:"genres"`
}

type tmdbDetail struct {
	ID            int         `json:"id"`
	Title         string      `json:"title"`
	Name          string      `json:"name"`
	OriginalTitle string      `json:"original_title"`
	OriginalName  string      `json:"original_name"`
	ReleaseDate   string      `json:"release_date"`
	FirstAirDate  string      `json:"first_air_date"`
	VoteAverage   float64     `json:"vote_average"`
	PosterPath    string      `json:"poster_path"`
	BackdropPath  string      `json:"backdrop_path"`
	Overview      string      `json:"overview"`
	Genres        []tmdbGenre `json:"genres"`

	Runtime        int   `json:"runtime"`          // movie
	EpisodeRunTime []int `json:"episode_run_time"` // tv

	Keywords struct {
		Keywords []tmdbGenre `json:"keywords"` // movie shape
		Results  []tmdbGenre `json:"results"`  // tv shape
	} `json:"keywords"`

	ExternalIDs struct {
		ImdbID string `json:"imdb_id"`
	} `json:"external_ids"`

	ReleaseDates struct { // movie: append_to_response=release_dates
		Results []struct {
			ISO31661     string `json:"iso_3166_1"`
			ReleaseDates []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`

	ContentRatings struct { // tv: append_to_response=content_ratings
		Results []struct {
			ISO31661 string `json:"iso_3166_1"`
			Rating   string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`

	Seasons []struct { // tv
		SeasonNumber int    `json:"season_number"`
		Name         string `json:"name"`
	} `json:"seasons"`

	Credits struct { // append_to_response=credits
		Cast []tmdbCastMember `json:"cast"`
		Crew []tmdbCrewMember `json:"crew"`
	} `json:"credits"`

	Similar         tmdbListResponse `json:"similar"`         // append_to_response=similar
	Recommendations tmdbListResponse `json:"recommendations"` // append_to_response=recommendations

	Videos struct { // append_to_response=videos
		Results []tmdbVideo `json:"results"`
	} `json:"videos"`
}

type tmdbVideo struct {
	Key      string `json:"key"`  // YouTube video id
	Site     string `json:"site"` // "YouTube"
	Type     string `json:"type"` // "Trailer" | "Teaser" | ...
	Name     string `json:"name"`
	Official bool   `json:"official"`
	Size     int    `json:"size"`
	Iso6391  string `json:"iso_639_1"` // video language (uk/ru/en/…)
}

type tmdbCastMember struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
	Order       int    `json:"order"`
}

type tmdbCrewMember struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Job         string `json:"job"`
	Department  string `json:"department"`
	ProfilePath string `json:"profile_path"`
}

type tmdbSeasonDetail struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	Episodes     []struct {
		EpisodeNumber int     `json:"episode_number"`
		Name          string  `json:"name"`
		AirDate       string  `json:"air_date"`
		StillPath     string  `json:"still_path"`
		Overview      string  `json:"overview"`
		Runtime       int     `json:"runtime"`
		VoteAverage   float64 `json:"vote_average"`
	} `json:"episodes"`
}

// /person/{id}
type tmdbPersonDetail struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Biography          string `json:"biography"`
	Birthday           string `json:"birthday"`
	Deathday           string `json:"deathday"`
	PlaceOfBirth       string `json:"place_of_birth"`
	ProfilePath        string `json:"profile_path"`
	KnownForDepartment string `json:"known_for_department"`
}

// /person/{id}/combined_credits — movie and tv items with media_type set.
type tmdbCreditsResponse struct {
	Cast []tmdbListItem `json:"cast"`
	Crew []tmdbListItem `json:"crew"`
}
