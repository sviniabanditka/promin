package subtitles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const searchJSON = `{"total_count":2,"data":[
 {"attributes":{"language":"uk","release":"Film.2023.1080p","download_count":50,"hearing_impaired":false,"fps":23.976,"uploader":{"name":"x"},"files":[{"file_id":111}]}},
 {"attributes":{"language":"en","release":"Film.2023.WEB","download_count":900,"files":[{"file_id":222}]}},
 {"attributes":{"language":"ru","release":"nofiles","files":[]}}]}`

func TestParseSearch(t *testing.T) {
	res, err := ParseSearch([]byte(searchJSON))
	if err != nil || len(res) != 2 || res[0].FileID != 111 || res[0].Lang != "uk" || res[1].Downloads != 900 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestVTTDownloadsOnceAndCaches(t *testing.T) {
	downloads := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download":
			if r.Header.Get("Api-Key") != "k" {
				t.Errorf("api key header missing")
			}
			downloads++
			w.Write([]byte(`{"link":"` + srv.URL + `/file.srt","remaining":4}`))
		case "/file.srt":
			w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	c := New("k", srv.URL, dir, func(b []byte) []byte { return append([]byte("WEBVTT\n\n"), b...) })
	v1, err := c.VTT(context.Background(), 5)
	if err != nil || string(v1[:6]) != "WEBVTT" {
		t.Fatalf("first: %v %q", err, v1)
	}
	if _, err := os.Stat(filepath.Join(dir, "5.vtt")); err != nil {
		t.Fatal("not cached")
	}
	if _, err := c.VTT(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if downloads != 1 {
		t.Fatalf("expected one download, got %d", downloads)
	}
	if _, err := New("", "", dir, nil).Search(context.Background(), Query{IMDbID: "tt1"}); err != ErrDisabled {
		t.Fatalf("disabled client must refuse: %v", err)
	}
}
