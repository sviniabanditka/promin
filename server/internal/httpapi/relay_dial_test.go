package httpapi

import "testing"

// SSRF is decided on the resolved address, not the URL literal: a hostname
// pointing at a cluster service, the node or loopback must be refused.
func TestGuardDialBlocksPrivateResolvedAddresses(t *testing.T) {
	prev := relayAllowLoopback
	relayAllowLoopback = false
	defer func() { relayAllowLoopback = prev }()

	for _, addr := range []string{"127.0.0.1:80", "10.43.0.1:443", "10.42.0.7:8080", "172.16.5.5:80", "192.168.1.1:80", "169.254.169.254:80", "0.0.0.0:80", "[::1]:80", "[fd00::1]:80"} {
		if err := guardDial("tcp", addr, nil); err == nil {
			t.Errorf("%s: expected blocked", addr)
		}
	}
	for _, addr := range []string{"1.1.1.1:443", "93.184.216.34:80", "[2606:4700::1111]:443"} {
		if err := guardDial("tcp", addr, nil); err != nil {
			t.Errorf("%s: unexpected block: %v", addr, err)
		}
	}
}
