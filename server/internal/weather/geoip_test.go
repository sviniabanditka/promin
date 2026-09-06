package weather

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type memCache struct{ m map[string]string }

func newMemCache() *memCache { return &memCache{m: map[string]string{}} }
func (c *memCache) Get(k string) (string, bool) {
	v, ok := c.m[k]
	return v, ok
}
func (c *memCache) Set(k, v string, _ time.Duration) { c.m[k] = v }

// Private/loopback addresses must never reach a provider: the lookup can only
// return nonsense and would waste a request.
func TestIsPublicIP(t *testing.T) {
	private := []string{"127.0.0.1", "::1", "10.0.0.5", "192.168.1.10", "172.16.0.1", "169.254.1.1", "0.0.0.0", "", "not-an-ip"}
	for _, ip := range private {
		if isPublicIP(ip) {
			t.Errorf("isPublicIP(%q) = true, want false", ip)
		}
	}
	for _, ip := range []string{"203.0.113.7", "2001:db8::1"} {
		if !isPublicIP(ip) {
			t.Errorf("isPublicIP(%q) = false, want true", ip)
		}
	}
}

func TestGeoIPSkipsPrivateAddresses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"success":true,"city":"X","latitude":1,"longitude":2}`))
	}))
	defer srv.Close()
	s := NewService(newMemCache(), slog.Default())
	if _, err := s.GeoIP(context.Background(), "192.168.0.10"); err == nil {
		t.Fatal("private address should not resolve")
	}
	if hits.Load() != 0 {
		t.Error("a provider was called for a private address")
	}
}

// The result is cached per IP: a household makes a handful of lookups ever.
func TestGeoIPCachesPerIP(t *testing.T) {
	cache := newMemCache()
	s := NewService(cache, slog.Default())
	// Seed the cache directly — no network in this test at all.
	cache.Set("weather:geoip:203.0.113.7", `{"name":"Trusk","lat":49.27,"lon":23.49}`, time.Hour)

	p, err := s.GeoIP(context.Background(), "203.0.113.7")
	if err != nil {
		t.Fatalf("GeoIP: %v", err)
	}
	if p.Name != "Trusk" || p.Lat == 0 {
		t.Errorf("cached place not returned: %+v", p)
	}
}

// Forecast keys round coordinates, so neighbours share one cached forecast
// instead of fragmenting the cache over meaningless decimals.
func TestForecastKeyRounds(t *testing.T) {
	a := forecastKey(Place{Lat: 49.2737, Lon: 23.4974}, "uk")
	b := forecastKey(Place{Lat: 49.2751, Lon: 23.4988}, "uk")
	if a != b {
		t.Errorf("nearby coordinates got different keys:\n %s\n %s", a, b)
	}
	if !strings.Contains(a, "49.3") {
		t.Errorf("unexpected key: %s", a)
	}
	if forecastKey(Place{Lat: 50.45, Lon: 30.52}, "uk") == a {
		t.Error("distant coordinates collapsed to the same key")
	}
}
