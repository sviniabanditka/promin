//go:build providers

package main

import (
	"context"
	"log/slog"

	"github.com/sviniabanditka/promin/server/internal/sources"
	"github.com/sviniabanditka/promin/server/providers"
)

// buildProviders: the private provider set (git submodule server/providers),
// compiled in with `-tags providers`.
func buildProviders(logger *slog.Logger, dataDir, proxyURL string) ([]sources.NativeProvider, func(ctx context.Context)) {
	return providers.Build(providers.Deps{Logger: logger, DataDir: dataDir, ProxyURL: proxyURL})
}
