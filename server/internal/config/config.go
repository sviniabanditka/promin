// Package config reads Promin server configuration from environment
// variables. Only the settings needed by the current skeleton are defined
// here; the rest of the canonical list in docs/backend.md is added
// phase by phase as the corresponding subsystem is implemented.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration for the promin binary.
type Config struct {
	// HTTPAddr is the address the internal HTTP listener binds to.
	HTTPAddr string
	// MetricsAddr is the separate Prometheus /metrics listener
	// (PROMIN_METRICS_ADDR, default :9100). Empty disables it.
	MetricsAddr string
	// DataDir is the root directory for images/torrents/remux storage.
	DataDir string
	// DBPath is the path to the SQLite database file.
	DBPath string
	// JacRedBaseURL is the torrent-indexer (JacRed-compatible) base URL used
	// for magnet search — GET /api/v1.0/torrents. Default: the public jac.red.
	JacRedBaseURL string
	// JacRedAPIKey is the apikey query param for the indexer (jac.red: empty).
	JacRedAPIKey string
	// TMDBAPIKey is the TMDB v3 API key used by internal/catalog.
	TMDBAPIKey string
	// TMDBBaseURL is the base URL of the TMDB REST API.
	TMDBBaseURL string
	// TMDBFallbackURLs are additional TMDB-compatible base URLs tried in
	// order when TMDBBaseURL fails (mirror/proxy hosts).
	TMDBFallbackURLs []string
	// OMDbAPIKey is the OMDb (omdbapi.com) API key used by internal/catalog
	// to enrich title cards with imdb_rating. Empty disables the feature
	// entirely (no requests made, imdb_rating simply absent from cards) —
	// there is no public working default like TMDB's, so this must be set
	// explicitly in deployment.
	OMDbAPIKey string
	// LogLevel controls slog verbosity (debug|info|warn|error).
	LogLevel string
	// LogsPassword gates the /logs web UI (Basic Auth). Empty disables /logs
	// entirely (404). Set PROMIN_LOGS_PASSWORD to enable.
	LogsPassword string
	// AdminPassword bootstraps the admin (user 1) password. When set and it
	// differs from the stored hash, the admin password is reset on boot
	// (PROMIN_ADMIN_PASSWORD). Empty + no admin user → /admin is unconfigured.
	AdminPassword string
	// PinSecret keys the deterministic PIN lookup hash (PROMIN_PIN_SECRET).
	// Empty → a random 32-byte secret is generated once under DataDir/pin_secret.
	// Rotating it invalidates every profile PIN.
	PinSecret string
	// FFmpegPath is the ffmpeg binary used by internal/remux.
	FFmpegPath string
	// FFprobePath is the ffprobe binary used to detect a source's video
	// codec / HDR before deciding an HEVC→H264 transcode (internal/remux).
	FFprobePath string
	// RemuxMaxTranscodes bounds concurrent HEVC/AV1→H264 transcode jobs
	// (PROMIN_REMUX_MAX_TRANSCODES). Its own semaphore, separate from the
	// cheap copy limiter — transcode is ~2-3 vCPU per 1080p job, so keep this
	// at 1 on the ~3 vCPU stack budget (docs/streaming.md, docs/streaming.md).
	RemuxMaxTranscodes int
	// RemuxMaxCopyJobs bounds concurrent copy_hls/copy_mkv ffmpeg
	// processes. Not in the canonical docs/backend.md table (copy has
	// no dedicated env var there beyond the transcode limit); default
	// matches the "например, до 4" figure from docs/streaming.md
	// section 4.
	RemuxMaxCopyJobs int
	// RemuxJobTTL is how long an untouched remux job survives before its
	// temp directory is cleaned up (docs/backend.md).
	RemuxJobTTL time.Duration
	// TorrentPort is the BitTorrent peer port (PROMIN_TORRENT_PORT, 0 =
	// anacrolix default 42069). Lets a dev instance run beside another.
	TorrentPort int
	// TorrentMaxActive bounds concurrent torrents held by internal/torrent
	// (PROMIN_TORRENT_MAX_ACTIVE, docs/backend.md).
	TorrentMaxActive int
	// TorrentCacheLimitGB bounds the on-disk torrent LRU cache
	// (PROMIN_TORRENT_CACHE_LIMIT_GB).
	TorrentCacheLimitGB int
	// TorrentMetadataTimeout bounds AddMagnet's wait for torrent info
	// (docs/streaming.md, "~20-30 c").
	TorrentMetadataTimeout time.Duration
	// WeatherPlace is the city for the screensaver forecast
	// (PROMIN_WEATHER_PLACE, e.g. "Kyiv"). Empty → guessed from the visitor's
	// country via Cloudflare's CF-IPCountry header.
	WeatherPlace string
	// H1Host is the HTTP/1.1-only hostname a device with "Режим старого ТВ" on
	// moves itself to (old Samsung: Chromium ~47 breaks sustained media over
	// h2); MainHost is the normal (Cloudflare) one it moves back to when the
	// switch is off. Empty → no steering. PROMIN_H1_HOST / PROMIN_MAIN_HOST.
	// docs/streaming.md
	H1Host   string
	MainHost string
	// BackupDir is where nightly SQLite snapshots land (PROMIN_BACKUP_DIR,
	// default DataDir/backups). The cache in the DB is regenerable; the
	// accounts/bookmarks/history are NOT, and the whole file is ~13 MB.
	BackupDir string
	// BackupInterval between snapshots (PROMIN_BACKUP_INTERVAL). 0 disables.
	BackupInterval time.Duration
	// BackupKeep is how many snapshots to retain (PROMIN_BACKUP_KEEP).
	BackupKeep int
	// NativeSourcesEnable turns on Promin's OWN scraper providers
	// lampac-independence plan. Off by default: a deployment without
	// PROMIN_NATIVE_SOURCE_BASE_URL has nothing to scrape.
	NativeSourcesEnable bool
	// NativeProxyURL: HTTP proxy for providers whose catalogs block datacenter
	// IPs (pages only, never video). Secret lampac-proxy/url in k8s.
	NativeProxyURL      string
	OpenSubtitlesAPIKey string
	// providers that replace lampac's RCH-only modules (docs/streaming.md).
	// moved native (docs/streaming.md).
	// (PROMIN_NATIVE_SOURCE_BASE_URL). Deliberately NOT defaulted in code — the
	// source host lives in deployment config/secrets, never in git.

	// TelegramBotToken enables the Telegram companion bot (search from the
	// phone, open on a TV). Empty = bot disabled. From the k8s secret, never a
	// default. TelegramAPIBaseURL overrides the Bot API host (tests / mirrors).
	TelegramBotToken   string
	TelegramAPIBaseURL string
}

// Load reads configuration from environment variables, applying the
// defaults documented in docs/backend.md.
func Load() Config {
	dataDir := getenv("PROMIN_DATA_DIR", "/data")
	return Config{
		HTTPAddr:    getenv("PROMIN_HTTP_ADDR", ":8080"),
		MetricsAddr: getenv("PROMIN_METRICS_ADDR", ":9100"),
		DataDir:     dataDir,
		DBPath:      getenv("PROMIN_DB_PATH", dataDir+"/promin.db"),
		// Public JacRed API — the same upstream lampac's own JacRed module was
		// proxying ("redapi": "http://jac.red"), minus lampac. Ф0 of docs/lampac-independence.
		JacRedBaseURL: getenv("PROMIN_JACRED_BASE_URL", "http://jac.red"),
		JacRedAPIKey:  getenv("PROMIN_JACRED_APIKEY", ""),
		// Default is Lampa's public TMDB key (verified working); override
		// with a private key in production via PROMIN_TMDB_API_KEY.
		TMDBAPIKey:  getenv("PROMIN_TMDB_API_KEY", ""), // required; no built-in key (secret promin-secrets/tmdb-api-key)
		TMDBBaseURL: getenv("PROMIN_TMDB_BASE_URL", "https://api.themoviedb.org/3"),
		// Fallback TMDB hosts tried (in order) when the primary fails —
		// comma-separated, empty by default. Only set this to a FULL TMDB
		// mirror (one that proxies /trending, /discover, /genre, … not just
		// /movie/{id}): a partial mirror 404s the list endpoints home needs,
		// and a 404 reads as "title absent" → breaks home instead of helping.
		// tmdb.cub.red is such a partial mirror, so it is NOT the default.
		// Outage resilience comes from the client's breaker + stale cache,
		// not from a mirror (mirrors proxy TMDB live and die with it).
		TMDBFallbackURLs:       getenvList("PROMIN_TMDB_FALLBACK_URLS", "-"),
		OMDbAPIKey:             getenv("PROMIN_OMDB_KEY", ""),
		LogLevel:               getenv("PROMIN_LOG_LEVEL", "info"),
		LogsPassword:           getenv("PROMIN_LOGS_PASSWORD", ""),
		AdminPassword:          getenv("PROMIN_ADMIN_PASSWORD", ""),
		PinSecret:              getenv("PROMIN_PIN_SECRET", ""),
		FFmpegPath:             getenv("PROMIN_FFMPEG_PATH", "ffmpeg"),
		FFprobePath:            getenv("PROMIN_FFPROBE_PATH", "ffprobe"),
		RemuxMaxTranscodes:     getenvInt("PROMIN_REMUX_MAX_TRANSCODES", 1),
		RemuxMaxCopyJobs:       getenvInt("PROMIN_REMUX_MAX_COPY", 4),
		RemuxJobTTL:            getenvDuration("PROMIN_REMUX_JOB_TTL", 30*time.Minute),
		TorrentPort:            getenvInt("PROMIN_TORRENT_PORT", 0),
		TorrentMaxActive:       getenvInt("PROMIN_TORRENT_MAX_ACTIVE", 5),
		TorrentCacheLimitGB:    getenvInt("PROMIN_TORRENT_CACHE_LIMIT_GB", 80), // ~80 of 200 GB disk; LRU evicts over this
		TorrentMetadataTimeout: getenvDuration("PROMIN_TORRENT_METADATA_TIMEOUT", 30*time.Second),
		WeatherPlace:           getenv("PROMIN_WEATHER_PLACE", ""),
		H1Host:                 getenv("PROMIN_H1_HOST", ""),
		MainHost:               getenv("PROMIN_MAIN_HOST", ""),
		BackupDir:              getenv("PROMIN_BACKUP_DIR", dataDir+"/backups"),
		BackupInterval:         getenvDuration("PROMIN_BACKUP_INTERVAL", 24*time.Hour),
		BackupKeep:             getenvInt("PROMIN_BACKUP_KEEP", 7),
		NativeSourcesEnable:    getenvBool("PROMIN_NATIVE_SOURCES", false),
		NativeProxyURL:         getenv("PROMIN_NATIVE_PROXY_URL", ""),
		OpenSubtitlesAPIKey:    getenv("PROMIN_OPENSUBTITLES_API_KEY", ""),
		TelegramBotToken:       getenv("PROMIN_TELEGRAM_BOT_TOKEN", ""),
		TelegramAPIBaseURL:     getenv("PROMIN_TELEGRAM_API_BASE_URL", "https://api.telegram.org"),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getenvList reads a comma-separated env var into a trimmed, non-empty
// slice. An env value of "-" (or "") means "no entries" so the fallback
// mirror can be disabled explicitly.
func getenvList(key, fallback string) []string {
	v := os.Getenv(key)
	if v == "" {
		v = fallback
	}
	if v == "-" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func getenvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getenvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

// splitList parses a comma-separated env value into trimmed, non-empty items.
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
