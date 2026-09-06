package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// Sentinel errors mapped to API error codes by internal/httpapi (see
// docs/api.md).
var (
	ErrLoginTaken          = store.ErrLoginTaken
	ErrRegistrationClosed  = errors.New("auth: registration closed")
	ErrInvalidCredentials  = errors.New("auth: invalid credentials")
	ErrInvalidToken        = errors.New("auth: invalid or expired token")
	ErrDeviceNotFound      = errors.New("auth: device not found")
	ErrCannotRevokeCurrent = errors.New("auth: cannot revoke the current session without ?force=true")
)

// ErrRateLimited is returned by Login when the login+IP key has exceeded
// its failed-attempt budget (docs/backend.md).
type ErrRateLimited struct {
	RetryAfter time.Duration
}

func (e ErrRateLimited) Error() string {
	return fmt.Sprintf("auth: rate limited, retry after %s", e.RetryAfter)
}

// lastSeenThrottle bounds how often ResolveToken writes last_seen back to
// SQLite, per docs/backend.md ("не чаще раза в 5 минут на
// устройство") — avoids a write on every single authenticated request.
const lastSeenThrottle = 5 * time.Minute

// Config controls registration policy; the rate limiter's own knobs are
// passed to NewService separately since they're not part of the "is
// registration open" decision.
type Config struct {
}

// Service implements registration, login, session resolution and device
// management. It knows nothing about HTTP — internal/httpapi translates
// its errors to the API error envelope.
type Service struct {
	users    *store.UsersRepo
	sessions *store.SessionsRepo
	cfg      Config
	now      func() time.Time

	// PIN / admin auth (wired by SetPINAuth). pinSecret keys the deterministic
	// PIN lookup hash; the limiters bound brute-force; adminTTL expires the
	// admin panel session.
	pinSecret  []byte
	pinPerIP   *RateLimiter
	pinGlobal  *RateLimiter
	adminPerIP *RateLimiter
	adminTTL   time.Duration
}

// SetPINAuth wires the PIN/admin state (main.go, after NewService).
func (s *Service) SetPINAuth(pinSecret []byte, pinPerIP, pinGlobal, adminPerIP *RateLimiter, adminTTL time.Duration) {
	s.pinSecret = pinSecret
	s.pinPerIP = pinPerIP
	s.pinGlobal = pinGlobal
	s.adminPerIP = adminPerIP
	s.adminTTL = adminTTL
}

// NewService builds a Service. limiter may be shared or dedicated; a
// reasonable default per docs/backend.md is 5 attempts / 15
// minutes / 15 minute block — see config.Config for the env-configurable
// values wired in by cmd/promin/main.go.
func NewService(db *store.DB, cfg Config) *Service {
	return &Service{
		users:    db.Users,
		sessions: db.Sessions,
		cfg:      cfg,
		now:      time.Now,
	}
}

// AuthResult is what Register/Login return: a fresh opaque token and the
// user it belongs to.
type AuthResult struct {
	Token string
	User  store.User
}

func (s *Service) newSession(user store.User, deviceName, deviceType string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	now := s.now().Unix()
	err = s.sessions.Create(store.Session{
		Token:      token,
		UserID:     user.ID,
		DeviceName: deviceName,
		DeviceType: deviceType,
		CreatedAt:  now,
		LastSeen:   now,
	})
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

// Logout revokes token (POST /api/v1/auth/logout).
func (s *Service) Logout(token string) error {
	return s.sessions.Delete(token)
}

// ResolveToken looks up the session+user behind an opaque token, for the
// auth middleware. It throttles the last_seen write per lastSeenThrottle.
func (s *Service) ResolveToken(token string) (store.Session, store.User, error) {
	session, err := s.sessions.Get(token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Session{}, store.User{}, ErrInvalidToken
		}
		return store.Session{}, store.User{}, err
	}

	now := s.now()
	// Admin panel sessions expire (living-room TV sessions are immortal by
	// design). Pure created_at clock math — no schema.
	if session.DeviceType == "admin" && s.adminTTL > 0 && now.Unix()-session.CreatedAt > int64(s.adminTTL.Seconds()) {
		_ = s.sessions.Delete(token)
		return store.Session{}, store.User{}, ErrInvalidToken
	}
	if now.Sub(time.Unix(session.LastSeen, 0)) >= lastSeenThrottle {
		_ = s.sessions.TouchLastSeen(token, now.Unix()) // best-effort; not fatal to the request
		session.LastSeen = now.Unix()
	}

	user, err := s.users.GetByID(session.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Session{}, store.User{}, ErrInvalidToken
		}
		return store.Session{}, store.User{}, err
	}
	return session, user, nil
}

// Device is the API-facing view of a session (docs/api.md
// section 1, GET /api/v1/auth/devices).
type Device struct {
	TokenID    string
	DeviceName string
	DeviceType string
	CreatedAt  int64
	LastSeen   int64
	Current    bool
}

// ListDevices returns userID's sessions, current marked against
// currentToken.
func (s *Service) ListDevices(userID int64, currentToken string) ([]Device, error) {
	sessions, err := s.sessions.ListByUser(userID)
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, Device{
			TokenID:    tokenID(sess.Token),
			DeviceName: sess.DeviceName,
			DeviceType: sess.DeviceType,
			CreatedAt:  sess.CreatedAt,
			LastSeen:   sess.LastSeen,
			Current:    sess.Token == currentToken,
		})
	}
	return out, nil
}

// RevokeDevice implements DELETE /api/v1/auth/devices/{token_id}. Revoking
// the caller's own current session requires force=true (guard against an
// accidental self-lockout, docs/api.md).
func (s *Service) RevokeDevice(userID int64, tokenIDStr, currentToken string, force bool) error {
	sessions, err := s.sessions.ListByUser(userID)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if tokenID(sess.Token) != tokenIDStr {
			continue
		}
		if sess.Token == currentToken && !force {
			return ErrCannotRevokeCurrent
		}
		return s.sessions.Delete(sess.Token)
	}
	return ErrDeviceNotFound
}
