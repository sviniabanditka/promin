// Package weather backs the screensaver's forecast strip.
//
// Provider is open-meteo: no API key, no account, no secret to rotate — which
// is why it wins over the alternatives for a self-hosted household box. Both
// the forecast and the geocoding endpoint are reachable from the VPS.
//
// Everything is cached in the shared KV table, so a screensaver that activates
// twenty times a day makes at most a couple of upstream calls.
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	forecastURL  = "https://api.open-meteo.com/v1/forecast"
	geocodingURL = "https://geocoding-api.open-meteo.com/v1/search"

	// forecastTTL: weather changes slowly enough that half an hour is plenty,
	// and it keeps the screensaver from hammering the API on every activation.
	forecastTTL = 30 * time.Minute
	// placeTTL: a resolved place (name → coordinates) is effectively permanent.
	placeTTL = 30 * 24 * time.Hour

	requestTimeout = 8 * time.Second
	forecastDays   = 7
)

// Cache is the small KV surface this package needs (the shared cache table).
type Cache interface {
	Get(key string) (string, bool)
	Set(key, value string, ttl time.Duration)
}

// Place is a resolved location.
type Place struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// Day is one day of the forecast.
type Day struct {
	Date string  `json:"date"` // ISO yyyy-mm-dd
	Code int     `json:"code"` // WMO weather code
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
}

// Forecast is what the client renders: right now plus the coming week.
type Forecast struct {
	Place string  `json:"place"`
	Now   float64 `json:"now"`
	Code  int     `json:"code"`
	Days  []Day   `json:"days"`
}

type Service struct {
	client *http.Client
	cache  Cache
	logger *slog.Logger
}

func NewService(cache Cache, logger *slog.Logger) *Service {
	return &Service{
		client: &http.Client{Timeout: requestTimeout},
		cache:  cache,
		logger: logger,
	}
}

func (s *Service) get(ctx context.Context, base string, q url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather: status %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// Geocode resolves a place NAME to coordinates, cached ~forever. Used for the
// configured city and for the country-level fallback.
func (s *Service) Geocode(ctx context.Context, name, lang string) (Place, error) {
	if name == "" {
		return Place{}, fmt.Errorf("weather: empty place")
	}
	key := "weather:place:" + lang + ":" + name
	if s.cache != nil {
		if raw, ok := s.cache.Get(key); ok {
			var p Place
			if json.Unmarshal([]byte(raw), &p) == nil && p.Lat != 0 {
				return p, nil
			}
		}
	}

	q := url.Values{"name": {name}, "count": {"1"}, "language": {lang}}
	body, err := s.get(ctx, geocodingURL, q)
	if err != nil {
		return Place{}, err
	}
	var out struct {
		Results []struct {
			Name string  `json:"name"`
			Lat  float64 `json:"latitude"`
			Lon  float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Place{}, err
	}
	if len(out.Results) == 0 {
		return Place{}, fmt.Errorf("weather: place %q not found", name)
	}
	p := Place{Name: out.Results[0].Name, Lat: out.Results[0].Lat, Lon: out.Results[0].Lon}
	if s.cache != nil {
		if b, err := json.Marshal(p); err == nil {
			s.cache.Set(key, string(b), placeTTL)
		}
	}
	return p, nil
}

// forecastKey rounds coordinates: neighbours share a forecast, and it keeps the
// cache from fragmenting over meaningless decimals.
func forecastKey(p Place, lang string) string {
	return "weather:fc:" + lang + ":" +
		strconv.FormatFloat(math.Round(p.Lat*10)/10, 'f', 1, 64) + "," +
		strconv.FormatFloat(math.Round(p.Lon*10)/10, 'f', 1, 64)
}

// Forecast returns current conditions + the coming week for a place.
func (s *Service) Forecast(ctx context.Context, p Place, lang string) (Forecast, error) {
	key := forecastKey(p, lang)
	if s.cache != nil {
		if raw, ok := s.cache.Get(key); ok {
			var f Forecast
			if json.Unmarshal([]byte(raw), &f) == nil && len(f.Days) > 0 {
				return f, nil
			}
		}
	}

	q := url.Values{
		"latitude":  {strconv.FormatFloat(p.Lat, 'f', 4, 64)},
		"longitude": {strconv.FormatFloat(p.Lon, 'f', 4, 64)},
		"current":   {"temperature_2m,weather_code"},
		"daily":     {"weather_code,temperature_2m_max,temperature_2m_min"},
		// timezone=auto so "today" is the household's today, not UTC's.
		"timezone":      {"auto"},
		"forecast_days": {strconv.Itoa(forecastDays)},
	}
	body, err := s.get(ctx, forecastURL, q)
	if err != nil {
		return Forecast{}, err
	}
	var raw struct {
		Current struct {
			Temp float64 `json:"temperature_2m"`
			Code int     `json:"weather_code"`
		} `json:"current"`
		Daily struct {
			Time []string  `json:"time"`
			Code []int     `json:"weather_code"`
			Max  []float64 `json:"temperature_2m_max"`
			Min  []float64 `json:"temperature_2m_min"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Forecast{}, err
	}

	f := Forecast{Place: p.Name, Now: raw.Current.Temp, Code: raw.Current.Code}
	for i := range raw.Daily.Time {
		if i >= len(raw.Daily.Code) || i >= len(raw.Daily.Max) || i >= len(raw.Daily.Min) {
			break
		}
		f.Days = append(f.Days, Day{
			Date: raw.Daily.Time[i],
			Code: raw.Daily.Code[i],
			Min:  raw.Daily.Min[i],
			Max:  raw.Daily.Max[i],
		})
	}
	if len(f.Days) == 0 {
		return Forecast{}, fmt.Errorf("weather: empty forecast")
	}
	if s.cache != nil {
		if b, err := json.Marshal(f); err == nil {
			s.cache.Set(key, string(b), forecastTTL)
		}
	}
	return f, nil
}

// --- GeoIP ------------------------------------------------------------------
//
// City-level location from the visitor's IP. Two keyless HTTPS-capable
// providers, tried in order, so one being down or rate-limited doesn't kill the
// feature — and no account or secret to manage either way.
//
// Privacy: this sends the household's public IP to a third party. Results are
// cached per IP for a week, so a home box makes a handful of calls ever.

const (
	geoPrimary  = "https://ipwho.is/"       // has an explicit success flag
	geoFallback = "http://ip-api.com/json/" // free tier is HTTP-only
	geoIPTTL    = 7 * 24 * time.Hour
)

// GeoIP resolves a public IP to a city + coordinates. Private/loopback
// addresses (dev, direct LAN hits) return an error rather than a bogus
// location.
func (s *Service) GeoIP(ctx context.Context, ip string) (Place, error) {
	if !isPublicIP(ip) {
		return Place{}, fmt.Errorf("weather: %q is not a public address", ip)
	}
	key := "weather:geoip:" + ip
	if s.cache != nil {
		if raw, ok := s.cache.Get(key); ok {
			var p Place
			if json.Unmarshal([]byte(raw), &p) == nil && (p.Lat != 0 || p.Lon != 0) {
				return p, nil
			}
		}
	}

	p, err := s.geoLookup(ctx, ip)
	if err != nil {
		return Place{}, err
	}
	if s.cache != nil {
		if b, mErr := json.Marshal(p); mErr == nil {
			s.cache.Set(key, string(b), geoIPTTL)
		}
	}
	return p, nil
}

func (s *Service) geoLookup(ctx context.Context, ip string) (Place, error) {
	// ipwho.is — {"success":true,"city":"…","latitude":…,"longitude":…}
	if body, err := s.get(ctx, geoPrimary+ip, url.Values{}); err == nil {
		var r struct {
			Success *bool   `json:"success"`
			City    string  `json:"city"`
			Lat     float64 `json:"latitude"`
			Lon     float64 `json:"longitude"`
		}
		if json.Unmarshal(body, &r) == nil && (r.Success == nil || *r.Success) && (r.Lat != 0 || r.Lon != 0) {
			return Place{Name: r.City, Lat: r.Lat, Lon: r.Lon}, nil
		}
	}

	// ip-api.com — {"status":"success","city":"…","lat":…,"lon":…}
	q := url.Values{"fields": {"status,city,lat,lon"}}
	body, err := s.get(ctx, geoFallback+ip, q)
	if err != nil {
		return Place{}, err
	}
	var r struct {
		Status string  `json:"status"`
		City   string  `json:"city"`
		Lat    float64 `json:"lat"`
		Lon    float64 `json:"lon"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Place{}, err
	}
	if r.Status != "success" || (r.Lat == 0 && r.Lon == 0) {
		return Place{}, fmt.Errorf("weather: geoip lookup failed for %q", ip)
	}
	return Place{Name: r.City, Lat: r.Lat, Lon: r.Lon}, nil
}

// isPublicIP keeps private/loopback/link-local addresses out of geo lookups:
// they'd waste a request and can only ever return nonsense.
func isPublicIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
}
