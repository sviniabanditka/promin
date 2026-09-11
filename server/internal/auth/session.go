package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// tokenBytes is the size of the random opaque session token, per
// docs/backend.md ("32 случайных байта в base64url").
const tokenBytes = 32

// generateToken returns a fresh cryptographically random opaque token
// (base64url, no padding), never a JWT — all state lives server-side in
// the sessions table.
func generateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// tokenID is the truncated, display-safe identifier for a session token
// used in GET/DELETE /api/v1/auth/devices (docs/api.md:
// "усечённый/хэшированный идентификатор для отображения (не сам токен)").
// It's just a prefix of the opaque token: still meaningless without the
// full token, and the full session list is already access-controlled by
// the caller's own auth, so a prefix is enough to disambiguate.
const tokenIDLen = 12

func tokenID(token string) string {
	if len(token) < tokenIDLen {
		return token
	}
	return token[:tokenIDLen]
}

// TokenID is the exported tokenID: the sync hub identifies a connected
// device by it (docs/api.md devices, open_title.device_id).
func TokenID(token string) string { return tokenID(token) }

// hashToken is what the sessions table stores instead of the bearer: a
// database dump or a backup then holds nothing a client could present.
// SHA-256 without a salt is enough — the input is 256 random bits.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// HashToken is the exported hashToken (startup migration of legacy rows).
func HashToken(raw string) string { return hashToken(raw) }
