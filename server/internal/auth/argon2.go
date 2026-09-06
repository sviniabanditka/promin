// Package auth implements registration, login, opaque session tokens and
// login rate-limiting, per docs/backend.md
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2Params are the argon2id parameters used for every new hash, per
// docs/backend.md They're embedded in the PHC string itself
// so they can change later without invalidating existing hashes.
var argon2Params = struct {
	time    uint32
	memory  uint32 // KiB
	threads uint8
	keyLen  uint32
	saltLen int
}{
	time:    1,
	memory:  64 * 1024, // 64 MB
	threads: 4,
	keyLen:  32,
	saltLen: 16,
}

// errMalformedHash is returned by verifyPassword when the stored hash
// isn't a well-formed PHC argon2id string.
var errMalformedHash = errors.New("auth: malformed password hash")

// hashPassword returns a PHC-formatted argon2id hash of password:
// $argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>
func hashPassword(password string) (string, error) {
	salt := make([]byte, argon2Params.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Params.time, argon2Params.memory, argon2Params.threads, argon2Params.keyLen)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argon2Params.memory, argon2Params.time, argon2Params.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// verifyPassword checks password against a PHC argon2id hash produced by
// hashPassword, in constant time.
func verifyPassword(phc, password string) (bool, error) {
	parts := strings.Split(phc, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errMalformedHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, errMalformedHash
	}

	var mem uint64
	var timeCost uint64
	var threads uint64
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return false, errMalformedHash
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return false, errMalformedHash
		}
		switch k {
		case "m":
			mem = n
		case "t":
			timeCost = n
		case "p":
			threads = n
		default:
			return false, errMalformedHash
		}
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errMalformedHash
	}

	got := argon2.IDKey([]byte(password), salt, uint32(timeCost), uint32(mem), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
