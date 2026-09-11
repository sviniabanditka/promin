package catalog

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A fake TMDB: /search/multi mixes a movie, a series and a person; the typed
// endpoints answer only their own kind; /person/7 has credits with a duplicate
// and a poster-less talk-show entry.
func fakeTMDB(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/genre/"):
			_, _ = w.Write([]byte(`{"genres":[{"id":28,"name":"Action"}]}`))
		case p == "/search/multi":
			_, _ = w.Write([]byte(`{"page":1,"total_pages":3,"results":[
			  {"id":1,"media_type":"movie","title":"Heat","release_date":"1995-12-15","poster_path":"/h.jpg","genre_ids":[28]},
			  {"id":7,"media_type":"person","name":"Al Pacino","profile_path":"/al.jpg","known_for_department":"Acting"},
			  {"id":2,"media_type":"tv","name":"Heat Wave","first_air_date":"2020-01-01","poster_path":"/hw.jpg"}]}`))
		case p == "/search/tv":
			_, _ = w.Write([]byte(`{"page":1,"total_pages":1,"results":[{"id":2,"name":"Heat Wave","first_air_date":"2020-01-01","poster_path":"/hw.jpg"}]}`))
		case p == "/search/movie":
			_, _ = w.Write([]byte(`{"page":1,"total_pages":1,"results":[{"id":1,"title":"Heat","release_date":"1995-12-15","poster_path":"/h.jpg"}]}`))
		case p == "/person/7":
			_, _ = w.Write([]byte(`{"id":7,"name":"Al Pacino","biography":"Actor.","birthday":"1940-04-25","profile_path":"/al.jpg","known_for_department":"Acting"}`))
		case p == "/person/7/combined_credits":
			_, _ = w.Write([]byte(`{"cast":[
			  {"id":1,"media_type":"movie","title":"Heat","poster_path":"/h.jpg","popularity":50},
			  {"id":9,"media_type":"tv","name":"Late Show","popularity":99},
			  {"id":3,"media_type":"movie","title":"Scarface","poster_path":"/s.jpg","popularity":80}],
			 "crew":[{"id":1,"media_type":"movie","title":"Heat","poster_path":"/h.jpg","popularity":50,"job":"Producer"}]}`))
		case p == "/person/404":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status_code":34}`))
		default:
			t.Errorf("unexpected TMDB path %s", p)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestSearchMultiSplitsPeople(t *testing.T) {
	srv := fakeTMDB(t)
	defer srv.Close()
	svc := NewService(NewClient([]string{srv.URL}, "k", newTestCache(t), slog.Default()), nil)

	res, err := svc.Search(context.Background(), "heat", "uk", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 || res.Items[0].Type != "movie" || res.Items[1].Type != "tv" {
		t.Fatalf("items = %+v", res.Items)
	}
	if res.TotalPages != 3 {
		t.Fatalf("total_pages = %d", res.TotalPages)
	}
	if len(res.People) != 1 || res.People[0].Name != "Al Pacino" || res.People[0].Department != "Acting" || res.People[0].Photo != "/img/w185/al.jpg" {
		t.Fatalf("people = %+v", res.People)
	}
}

func TestSearchTypedEndpoints(t *testing.T) {
	srv := fakeTMDB(t)
	defer srv.Close()
	svc := NewService(NewClient([]string{srv.URL}, "k", newTestCache(t), slog.Default()), nil)

	tv, err := svc.Search(context.Background(), "heat", "uk", 1, "tv")
	if err != nil || len(tv.Items) != 1 || tv.Items[0].Type != "tv" || tv.People != nil {
		t.Fatalf("tv: %+v %v", tv, err)
	}
	mv, err := svc.Search(context.Background(), "heat", "uk", 1, "movie")
	if err != nil || len(mv.Items) != 1 || mv.Items[0].Type != "movie" {
		t.Fatalf("movie: %+v %v", mv, err)
	}
	if _, err := svc.Search(context.Background(), "heat", "uk", 1, "person"); err != ErrInvalidType {
		t.Fatalf("bad type: %v", err)
	}
}

func TestPersonCredits(t *testing.T) {
	srv := fakeTMDB(t)
	defer srv.Close()
	svc := NewService(NewClient([]string{srv.URL}, "k", newTestCache(t), slog.Default()), nil)

	p, err := svc.Person(context.Background(), 7, "uk")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Al Pacino" || p.Biography != "Actor." || p.Photo != "/img/w342/al.jpg" {
		t.Fatalf("person = %+v", p.Person)
	}
	// Most popular first, duplicate (Heat as cast + crew) once, poster-less talk show dropped.
	if len(p.Credits) != 2 || p.Credits[0].TMDBID != 3 || p.Credits[1].TMDBID != 1 {
		t.Fatalf("credits = %+v", p.Credits)
	}
	if _, err := svc.Person(context.Background(), 404, "uk"); err != ErrPersonNotFound {
		t.Fatalf("404: %v", err)
	}
}
