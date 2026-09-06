package sources

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// ErrBalancerNotFound is returned by Resolve when the requested balancer
// code isn't present in this title's /lite/events listing.
// navigationBudget bounds one provider resolve (search + title page + player
// pages, some through the residential proxy).
const navigationBudget = 25 * time.Second

var ErrBalancerNotFound = errors.New("sources: balancer not found for this title")

// Service is the business-logic facade over a Lampac Client, independent
// of HTTP (per docs/backend.md: "каждый бизнес-модуль не
// знает о HTTP").
type Service struct {
	client *Client
	logger *slog.Logger
	// native are Promin's own scraper providers, listed and resolved alongside
	// lampac (see native.go). Empty = lampac-only, the previous behaviour.
	native []NativeProvider
	// nativeCache stores tmdb→source-id matches so the availability check costs
	// one search per title, not one per view. nil = no caching (still correct).
	nativeCache Cache
}

// NewService builds a Service backed by client.
func NewService(client *Client, logger *slog.Logger) *Service {
	return &Service{client: client, logger: logger}
}

// SetNativeProviders registers Promin's own scraper providers (and the cache
// their title matching uses). Called once at boot; empty leaves the service
// lampac-only.
func (s *Service) SetNativeProviders(cache Cache, providers ...NativeProvider) {
	s.nativeCache = cache
	s.native = providers
}

// Online lists the balancers Lampac knows about for a title, without
// resolving any of them into actual streams (see OnlineSource doc comment).
// Never returns an error to the caller: on Lampac failure it reports
// degraded=true with an empty list, per docs/backend.md
func (s *Service) Online(reqCtx context.Context, req OnlineRequest) OnlineResponse {
	return OnlineResponse{Sources: <-s.nativeSourcesAsync(reqCtx, req)}
}

// Resolve queries one provider (by code, as returned in OnlineSource.Balancer)
// and returns playable streams.
func (s *Service) Resolve(ctx context.Context, req ResolveRequest) (ResolveResponse, error) {
	np, ok := s.nativeProvider(req.Balancer)
	if !ok {
		return ResolveResponse{}, ErrBalancerNotFound
	}
	return s.resolveNative(ctx, np, req)
}
