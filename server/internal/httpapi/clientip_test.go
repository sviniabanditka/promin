package httpapi

import (
	"net/http/httptest"
	"testing"
)

// Behind Cloudflare the visitor is CF-Connecting-IP, never the edge's RemoteAddr:
// keying the PIN limiter on the edge address lumped every user on that edge into
// one bucket.
func TestClientIPPrefersCloudflareThenXFF(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.70.1.2:443"
	if got := clientIP(r); got != "172.70.1.2" {
		t.Fatalf("plain RemoteAddr: %q", got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("XFF first hop: %q", got)
	}
	r.Header.Set("CF-Connecting-IP", "198.51.100.4")
	if got := clientIP(r); got != "198.51.100.4" {
		t.Fatalf("CF header must win: %q", got)
	}
}
