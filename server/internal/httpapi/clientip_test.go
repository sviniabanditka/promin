package httpapi

import (
	"net/http/httptest"
	"testing"
)

// The rate-limit key must not be forgeable: CF-Connecting-IP counts only when
// the peer our front saw is a Cloudflare edge; otherwise the peer is the client.
func TestClientIPTrustsCloudflareOnlyFromCloudflare(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.42.0.7:443" // traefik pod
	if got := clientIP(r); got != "10.42.0.7" {
		t.Fatalf("plain RemoteAddr: %q", got)
	}
	// Direct visitor: traefik writes the real peer as the (only) XFF hop.
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.Header.Set("CF-Connecting-IP", "198.51.100.4") // forged by the visitor
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("forged CF header must be ignored: %q", got)
	}
	// Through Cloudflare: the peer is an edge address → CF-Connecting-IP wins.
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 172.70.1.2")
	if got := clientIP(r); got != "198.51.100.4" {
		t.Fatalf("CF header must win behind an edge: %q", got)
	}
	// Edge without the header (should not happen) → the edge itself.
	r.Header.Del("CF-Connecting-IP")
	if got := clientIP(r); got != "172.70.1.2" {
		t.Fatalf("edge fallback: %q", got)
	}
}
