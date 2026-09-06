package auth

import "testing"

func TestHmacPinDeterministicAndKeyed(t *testing.T) {
	s := &Service{pinSecret: []byte("secret-a")}
	h1 := s.hmacPin("123456")
	h2 := s.hmacPin("123456")
	if h1 != h2 {
		t.Fatalf("hmacPin not deterministic: %s != %s", h1, h2)
	}
	if s.hmacPin("654321") == h1 {
		t.Fatal("different PINs must hash differently")
	}
	// Different key → different hash (a leaked DB can't brute PINs without it).
	other := &Service{pinSecret: []byte("secret-b")}
	if other.hmacPin("123456") == h1 {
		t.Fatal("different pinSecret must produce a different lookup")
	}
	if len(h1) != 64 {
		t.Fatalf("expected hex SHA-256 (64 chars), got %d", len(h1))
	}
}

func TestPinLookupEmptyIsNull(t *testing.T) {
	s := &Service{pinSecret: []byte("k")}
	if s.PinLookup("").Valid {
		t.Fatal("empty PIN must produce a NULL lookup (clears the PIN)")
	}
	if got := s.PinLookup("000000"); !got.Valid || got.String == "" {
		t.Fatal("non-empty PIN must produce a valid lookup")
	}
}
