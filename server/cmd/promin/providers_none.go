//go:build !providers

package main

import (
	"context"
	"log/slog"

	"github.com/sviniabanditka/promin/server/internal/sources"
)

// Without the providers submodule (and `-tags providers`) Promin has no online
// sources: catalog, torrents, accounts and sync still work.
func buildProviders(*slog.Logger, string, string) ([]sources.NativeProvider, func(ctx context.Context)) {
	return nil, nil
}
