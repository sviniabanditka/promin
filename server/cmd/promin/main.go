// Command promin is the Promin backend: a single Go binary serving the
// web UI and REST/WS API. This is the phase-0 skeleton — auth, tmdb,
// torrent, remux and lampac integration are added in later phases.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sviniabanditka/promin/server/internal/auth"
	"github.com/sviniabanditka/promin/server/internal/catalog"
	"github.com/sviniabanditka/promin/server/internal/config"
	"github.com/sviniabanditka/promin/server/internal/httpapi"
	"github.com/sviniabanditka/promin/server/internal/logbuf"
	"github.com/sviniabanditka/promin/server/internal/metrics"
	"github.com/sviniabanditka/promin/server/internal/proxymon"
	"github.com/sviniabanditka/promin/server/internal/remux"
	"github.com/sviniabanditka/promin/server/internal/sources"
	"github.com/sviniabanditka/promin/server/internal/store"
	"github.com/sviniabanditka/promin/server/internal/subtitles"
	"github.com/sviniabanditka/promin/server/internal/sync"
	"github.com/sviniabanditka/promin/server/internal/telegram"
	"github.com/sviniabanditka/promin/server/internal/torrent"
	"github.com/sviniabanditka/promin/server/internal/weather"
)

// version is set via -ldflags "-X main.version=...".
var version = "dev"

const shutdownTimeout = 15 * time.Second

func main() {
	cfg := config.Load()
	logBuf := logbuf.NewBuffer(5000)
	logger := newLogger(cfg.LogLevel, logBuf)
	slog.SetDefault(logger)

	logger.Info("starting promin", "version", version, "addr", cfg.HTTPAddr, "data_dir", cfg.DataDir, "db_path", cfg.DBPath)

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if cfg.TMDBAPIKey == "" {
		logger.Error("PROMIN_TMDB_API_KEY is not set — the catalog cannot work without a TMDB v3 key")
		os.Exit(2)
	}
	tmdbClient := catalog.NewClient(append([]string{cfg.TMDBBaseURL}, cfg.TMDBFallbackURLs...), cfg.TMDBAPIKey, db.TMDBCache, logger)
	omdbClient := catalog.NewOMDbClient(cfg.OMDbAPIKey, db.TMDBCache, logger)
	catalogSvc := catalog.NewService(tmdbClient, omdbClient)

	indexerClient := sources.NewClient(cfg.JacRedBaseURL, cfg.JacRedAPIKey)
	sourcesSvc := sources.NewService(indexerClient, logger)

	// Online-source providers live in the private submodule server/providers and
	// are compiled in with -tags providers (see providers_private.go); the
	// aggregator caches title matching in the shared cache table.
	var startProviders func(context.Context)
	if cfg.NativeSourcesEnable {
		natives, bg := buildProviders(logger, cfg.DataDir, cfg.NativeProxyURL)
		startProviders = bg
		if len(natives) > 0 {
			sourcesSvc.SetNativeProviders(sources.NewStoreCache(db.TMDBCache), natives...)
			logger.Info("native sources enabled", "providers", len(natives))
		} else {
			logger.Warn("PROMIN_NATIVE_SOURCES=true but no providers compiled in (build with -tags providers)")
		}
	}

	authSvc := auth.NewService(db, auth.Config{})

	// PIN / admin auth: secret (env or generated once), brute-force limiters,
	// admin session TTL, then bootstrap the admin from PROMIN_ADMIN_PASSWORD.
	pinSecret, err := loadOrCreatePinSecret(cfg.PinSecret, cfg.DataDir)
	if err != nil {
		logger.Error("pin secret", "error", err)
		os.Exit(1)
	}
	authSvc.SetPINAuth(
		pinSecret,
		auth.NewRateLimiter(5, 15*time.Minute, 15*time.Minute),  // pin per-IP
		auth.NewRateLimiter(30, 15*time.Minute, 15*time.Minute), // pin global (distributed-sweep bound)
		auth.NewRateLimiter(5, 15*time.Minute, 15*time.Minute),  // admin per-IP
		2*time.Hour,
	)
	if created, err := authSvc.EnsureAdmin("admin", cfg.AdminPassword); err != nil {
		logger.Error("ensure admin", "error", err)
		os.Exit(1)
	} else if created {
		logger.Info("admin user created from PROMIN_ADMIN_PASSWORD")
	}

	hub := sync.NewHub()
	syncSvc := sync.NewService(db, hub)

	// ffmpeg/ffprobe availability is Warn-only diagnostics: probe them in the
	// background instead of spawning processes (up to 5 s timeout each) before
	// the socket is even listening. The queue itself probes zscale once and
	// logs it.
	go func() {
		if err := remux.CheckFFmpeg(cfg.FFmpegPath); err != nil {
			logger.Warn("ffmpeg not available, remux endpoints will fail jobs", "ffmpeg_path", cfg.FFmpegPath, "error", err)
		}
		if err := remux.CheckFFprobe(cfg.FFprobePath); err != nil {
			logger.Warn("ffprobe not available, HEVC auto-transcode won't trigger", "ffprobe_path", cfg.FFprobePath, "error", err)
		}
	}()
	remuxQueue, err := remux.NewQueue(remux.Config{
		DataDir:       cfg.DataDir,
		FFmpegPath:    cfg.FFmpegPath,
		FFprobePath:   cfg.FFprobePath,
		MaxTranscodes: cfg.RemuxMaxTranscodes,
		MaxCopyJobs:   cfg.RemuxMaxCopyJobs,
		JobTTL:        cfg.RemuxJobTTL,
		Logger:        logger,
	})
	if err != nil {
		logger.Error("failed to init remux queue", "error", err)
		os.Exit(1)
	}

	torrentMgr, err := torrent.NewManager(torrent.Config{
		DataDir:         cfg.DataDir,
		MaxActive:       cfg.TorrentMaxActive,
		CacheLimitBytes: int64(cfg.TorrentCacheLimitGB) * 1_000_000_000,
		MetadataTimeout: cfg.TorrentMetadataTimeout,
		ListenPort:      cfg.TorrentPort,
		Repo:            db.TorrentCache,
		Logger:          logger,
	})
	if err != nil {
		logger.Error("failed to init torrent manager", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if startProviders != nil {
		go startProviders(ctx) // e.g. the VeoVeo id-map refresh, off the request path
	}

	go remuxQueue.StartCleanup(ctx)
	go torrentMgr.StartBackgroundWorkers(ctx)

	// Daily prune of expired list-shaped TMDB cache rows (search/discover pages
	// pile up one row per query and were never deleted). Detail rows stay.
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		first := time.After(5 * time.Minute)
		for {
			select {
			case <-ctx.Done():
				return
			case <-first:
			case <-t.C:
			}
			if n, err := db.TMDBCache.PruneExpiredLists(time.Now().Unix()); err != nil {
				logger.Warn("tmdb cache prune failed", "error", err)
			} else if n > 0 {
				logger.Info("tmdb cache pruned", "rows", n)
			}
		}
	}()

	// Nightly snapshot of the database. Cache is regenerable, accounts/history
	// are not — and a consistent copy of a 13 MB file is essentially free.
	db.StartBackups(ctx, cfg.BackupDir, cfg.BackupInterval, cfg.BackupKeep, logger)

	// Keep the household's OWN library resident locally: everything favourited,
	// part-watched or recently played gets its TMDB detail cached whether or not
	// anyone reopened it. The on-demand cache only ever holds what somebody
	// happened to click; this makes the library that actually matters instant
	// and outage-proof. Paced and bounded — it runs behind the app.
	go func() {
		// Let boot settle before touching the network.
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Minute):
		}
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			refs, err := db.LibraryRefs(0)
			if err != nil {
				logger.Debug("prewarm: library query failed", "error", err)
			} else if len(refs) > 0 {
				out := make([]catalog.PrewarmRef, 0, len(refs))
				for _, r := range refs {
					out = append(out, catalog.PrewarmRef{TMDBID: int(r.TMDBID), MediaType: r.MediaType})
				}
				if n := catalogSvc.Prewarm(ctx, "", out); n > 0 {
					logger.Info("prewarm: library cached", "fetched", n, "of", len(out))
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	logger.Info("sync hub ready", "component", "sync")

	weatherSvc := weather.NewService(sources.NewStoreCache(db.TMDBCache), logger)

	// Telegram companion bot: long polling, only when the token is set.
	var tgBot *telegram.Bot
	if cfg.TelegramBotToken != "" {
		tgBot = telegram.New(telegram.NewClient(cfg.TelegramAPIBaseURL, cfg.TelegramBotToken), db.Telegram, catalogSvc, syncSvc, logger)
		go tgBot.Run(ctx)
	}

	// Residential proxy watch: liveness + the vendor's traffic package, both as
	// promin_proxy_* gauges. No proxy configured → nil monitor, Run is a no-op.
	go proxymon.New(cfg.NativeProxyURL, cfg.StableProxyToken, logger).Run(ctx)

	// Prometheus on its own listener: never on the public mux (the ingress
	// would expose it). PROMIN_METRICS_ADDR="" turns it off.
	if cfg.MetricsAddr != "" {
		metrics.SetBuildInfo(version)
		go func() {
			if err := metrics.ListenAndServe(cfg.MetricsAddr); err != nil {
				logger.Warn("metrics listener failed", "addr", cfg.MetricsAddr, "error", err)
			}
		}()
	}

	subsClient := subtitles.New(cfg.OpenSubtitlesAPIKey, "", filepath.Join(cfg.DataDir, "subs"), httpapi.ToWebVTT)
	subsClient.SetAccount(cfg.OpenSubtitlesUser, cfg.OpenSubtitlesPassword)
	if subsClient.Enabled() {
		logger.Info("external subtitles enabled (OpenSubtitles)")
	}
	handler := httpapi.NewServer(version, logger, cfg.DataDir, catalogSvc, sourcesSvc, remuxQueue, torrentMgr, authSvc, syncSvc, cfg.HTTPAddr, logBuf, cfg.LogsPassword, weatherSvc, cfg.WeatherPlace, cfg.H1Host, cfg.MainHost, tgBot, subsClient)
	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: handler,
		// Bound header-read and idle-keepalive so a slow/idle client can't tie up
		// a connection forever. NO WriteTimeout: /stream + HLS segments are
		// long-lived responses that must not be cut mid-write.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	if err := torrentMgr.Close(); err != nil {
		logger.Warn("torrent manager close failed", "error", err)
	}

	logger.Info("promin stopped")
}

func newLogger(level string, buf *logbuf.Buffer) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	// Wrap the stdout handler so every emitted record is also captured into the
	// ring buffer for the /logs UI. Capture level == stdout level (`lvl`); raise
	// PROMIN_LOG_LEVEL=debug to see debug in both.
	inner := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(logbuf.NewHandler(inner, buf))
}

// loadOrCreatePinSecret returns the PIN HMAC key: the env value if set, else the
// bytes at <dataDir>/pin_secret, generating a random 32-byte file (0600) the
// first time. Rotating it invalidates every profile PIN.
func loadOrCreatePinSecret(envVal, dataDir string) ([]byte, error) {
	if envVal != "" {
		return []byte(envVal), nil
	}
	path := filepath.Join(dataDir, "pin_secret")
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return b, nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, secret, 0o600); err != nil {
		return nil, err
	}
	return secret, nil
}
