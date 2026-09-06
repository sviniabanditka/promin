package sources

import "testing"

func TestMapTorrentsMergesSameInfohash(t *testing.T) {
	svc := &Service{}
	items := []jacredItem{
		{Tracker: "rutor", Title: "A", Magnet: "magnet:?xt=urn:btih:ABCDEF0123", Seeders: 5, Peers: 1},
		{Tracker: "kinozal", Title: "A", Magnet: "magnet:?xt=urn:btih:abcdef0123&dn=x", Seeders: 40, Peers: 3},
		{Tracker: "rutor", Title: "B", Magnet: "magnet:?xt=urn:btih:FFFF", Seeders: 1},
	}
	out := svc.mapTorrents(items, TorrentsRequest{Type: "movie"})
	if len(out) != 2 {
		t.Fatalf("want 2 rows, got %d", len(out))
	}
	if out[0].Tracker != "rutor, kinozal" || out[0].Seeders != 40 || out[0].Peers != 3 {
		t.Fatalf("merged row: %+v", out[0])
	}
}
