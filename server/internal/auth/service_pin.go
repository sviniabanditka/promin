package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// hmacPin is the deterministic PIN lookup hash: hex(HMAC-SHA256(pinSecret, pin)).
// Keyed so a stolen DB can't brute PINs offline without pinSecret; deterministic
// so a PIN resolves to a profile in O(1) via the unique index.
func (s *Service) hmacPin(pin string) string {
	m := hmac.New(sha256.New, s.pinSecret)
	m.Write([]byte(pin))
	return hex.EncodeToString(m.Sum(nil))
}

// PinLookup exposes the lookup hash for the admin panel (setting/clearing a
// profile's PIN). Returns a NULL NullString for an empty pin (clears the PIN).
func (s *Service) PinLookup(pin string) sql.NullString {
	if pin == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s.hmacPin(pin), Valid: true}
}

// LoginPIN authenticates a 6-digit PIN into its profile and mints a "tv"
// session. ip keys the per-IP limiter. Caller validates the PIN is 6 digits.
func (s *Service) LoginPIN(ip, pin, deviceName string) (AuthResult, error) {
	if ok, ra := s.pinPerIP.Allow("pin|" + ip); !ok {
		return AuthResult{}, ErrRateLimited{RetryAfter: ra}
	}
	if ok, ra := s.pinGlobal.Allow("pin"); !ok {
		return AuthResult{}, ErrRateLimited{RetryAfter: ra}
	}
	user, err := s.users.GetByPinLookup(s.hmacPin(pin))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.pinPerIP.RecordFailure("pin|" + ip)
			s.pinGlobal.RecordFailure("pin") // global never cleared by success — bounds a distributed sweep
			return AuthResult{}, ErrInvalidCredentials
		}
		return AuthResult{}, err
	}
	s.pinPerIP.RecordSuccess("pin|" + ip) // clear this IP; global bleeds by design
	token, err := s.newSession(user, deviceName, "tv")
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{Token: token, User: user}, nil
}

// AdminLogin verifies the admin (user 1) argon2 password and mints an "admin"
// session. ip keys the per-IP limiter.
func (s *Service) AdminLogin(ip, password string) (AuthResult, error) {
	if s.adminGlobal != nil {
		if ok, ra := s.adminGlobal.Allow("admin"); !ok {
			return AuthResult{}, ErrRateLimited{RetryAfter: ra}
		}
	}
	if ok, ra := s.adminPerIP.Allow("admin|" + ip); !ok {
		return AuthResult{}, ErrRateLimited{RetryAfter: ra}
	}
	user, err := s.users.GetByID(1)
	if err != nil {
		return AuthResult{}, ErrInvalidCredentials
	}
	ok, err := verifyPassword(user.PassHash, password)
	if err != nil || !ok {
		s.adminPerIP.RecordFailure("admin|" + ip)
		if s.adminGlobal != nil {
			s.adminGlobal.RecordFailure("admin") // never cleared by success
		}
		return AuthResult{}, ErrInvalidCredentials
	}
	s.adminPerIP.RecordSuccess("admin|" + ip)
	token, err := s.newSession(user, "admin", "admin")
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{Token: token, User: user}, nil
}

// SetProfilePIN sets or clears (empty pin) a profile's PIN. Wrapper over the
// repo that hashes the PIN. Returns store.ErrPinTaken on collision.
func (s *Service) SetProfilePIN(id int64, pin string) error {
	return s.users.SetPIN(id, s.PinLookup(pin))
}

// EnsureAdmin bootstraps the admin (user 1): creates it if absent, or resets its
// password when PROMIN_ADMIN_PASSWORD is set and differs. login is the admin's
// display login. Returns (created, error).
func (s *Service) EnsureAdmin(login, password string) (bool, error) {
	if password == "" {
		return false, nil // no env password → leave as-is (may be admin_unconfigured)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return false, err
	}
	u, err := s.users.GetByID(1)
	if errors.Is(err, store.ErrNotFound) {
		_, err := s.users.Create(login, hash, s.now().Unix())
		return true, err
	}
	if err != nil {
		return false, err
	}
	// User 1 exists: reset password if the env one no longer matches.
	if ok, _ := verifyPassword(u.PassHash, password); !ok {
		return false, s.users.UpdatePassHash(1, hash)
	}
	return false, nil
}

// Users exposes the repo for the admin panel (roster + CRUD).
func (s *Service) Users() *store.UsersRepo { return s.users }
