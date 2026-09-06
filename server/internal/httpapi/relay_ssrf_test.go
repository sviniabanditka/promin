package httpapi

import (
	"net/url"
	"testing"
)

// The relay fetches arbitrary balancer URLs from inside the cluster: anything
// that is not a public http(s) host must be refused before a byte is sent.
func TestValidateUpstreamBlocksNonPublicTargets(t *testing.T) {
	blocked := []string{
		"ftp://example.com/x",
		"file:///etc/passwd",
		"http://127.0.0.1:8080/api",
		"http://[::1]/",
		"http://0.0.0.0/",
		"http://169.254.169.254/latest/meta-data", // cloud metadata
		"http://10.43.0.1/",                       // k8s service range
		"http://192.168.1.10/",
		"http://172.16.5.5/",
	}
	for _, raw := range blocked {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if err := validateUpstream(u); err == nil {
			t.Errorf("%s must be blocked", raw)
		}
	}
	for _, raw := range []string{"https://cdn.example.com/hls/a.m3u8", "http://93.184.216.34/x.mp4"} {
		u, _ := url.Parse(raw)
		if err := validateUpstream(u); err != nil {
			t.Errorf("%s must be allowed: %v", raw, err)
		}
	}
}
