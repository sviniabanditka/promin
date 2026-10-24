package sources

import (
	"context"
	"log/slog"
	"testing"

	"github.com/sviniabanditka/promin/server/internal/sources/provider"
)

// seasonStub carries seasons 1 and 2 of its only title.
type seasonStub struct {
	stubProvider
	titles int
}

func (s *seasonStub) Title(_ context.Context, _ string, _ provider.Ctx) (provider.TitleDetails, error) {
	s.titles++
	return provider.TitleDetails{Type: "tv", Seasons: []provider.Season{{Number: 1}, {Number: 2}}}, nil
}

// A series listing hides the provider when the requested season is not in its
// catalog, keeps it otherwise, and caches the season list across listings.
func TestListingHidesMissingSeason(t *testing.T) {
	p := &seasonStub{stubProvider: stubProvider{id: "test", items: []provider.SearchItem{item("id-1", "Серіал", 2020)}}}
	svc := NewService(nil, slog.Default())
	svc.SetNativeProviders(newMemCache(), NativeProvider{P: p, Name: "Promin", Enabled: true})
	base := OnlineRequest{TMDBID: 11, Type: "tv", Title: "Серіал", Year: 2020}

	req := base
	req.Season = 2
	if got := svc.nativeSources(context.Background(), req); len(got) != 1 {
		t.Fatalf("season 2 exists: want listed, got %v", got)
	}
	req.Season = 3
	if got := svc.nativeSources(context.Background(), req); len(got) != 0 {
		t.Fatalf("season 3 missing: want hidden, got %v", got)
	}
	if p.titles != 1 {
		t.Fatalf("season list must be cached: Title called %d times", p.titles)
	}
	req.Season = 0
	if got := svc.nativeSources(context.Background(), req); len(got) != 1 {
		t.Fatalf("no season asked: want listed, got %v", got)
	}
}
