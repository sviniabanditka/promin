package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sviniabanditka/promin/server/internal/auth"
	"github.com/sviniabanditka/promin/server/internal/store"
)

// authInfo is what auth middleware attaches to the request context: the
// resolved user and the session (device/token) that authenticated it.
type authInfo struct {
	User    store.User
	Session store.Session
}

type ctxKey int

const ctxKeyAuth ctxKey = iota

// authFrom returns the authInfo attached by requireAuth/optionalAuth, if
// any.
func authFrom(r *http.Request) (authInfo, bool) {
	v, ok := r.Context().Value(ctxKeyAuth).(authInfo)
	return v, ok
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if strings.HasPrefix(h, prefix) {
		return strings.TrimPrefix(h, prefix)
	}
	return ""
}

// tokenFromRequest resolves the caller's token. Per docs/api.md
// section 0: regular API routes only accept the Authorization header;
// media routes (/stream, /relay, /remux, /img) and the WS upgrade also
// accept ?t=<token> since <video>/<img>/the WS handshake can't always set
// headers.
func tokenFromRequest(r *http.Request, allowQuery bool) string {
	if t := bearerToken(r); t != "" {
		return t
	}
	if allowQuery {
		return r.URL.Query().Get("t")
	}
	return ""
}

// requireAuth wraps a handler that must have a valid session; it resolves
// the token strictly from the Authorization header, per
// docs/api.md ("Авторизация ... на всех эндпоинтах кроме
// register/login").
func requireAuth(svc *auth.Service, next http.HandlerFunc) http.HandlerFunc {
	return authMiddleware(svc, false, true, next)
}

// requireAuthMedia is requireAuth but also accepts ?t=<token>, for media
// endpoints per docs/api.md
func requireAuthMedia(svc *auth.Service, next http.HandlerFunc) http.HandlerFunc {
	return authMiddleware(svc, true, true, next)
}

func authMiddleware(svc *auth.Service, allowQuery, required bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := tokenFromRequest(r, allowQuery)
		if token == "" {
			if required {
				writeError(w, http.StatusUnauthorized, "unauthorized", "потрібна авторизація")
				return
			}
			next(w, r)
			return
		}

		session, user, err := svc.ResolveToken(token)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidToken) {
				if required {
					writeError(w, http.StatusUnauthorized, "unauthorized", "невірний або протермінований токен")
					return
				}
				next(w, r)
				return
			}
			writeInternal(w, err)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeyAuth, authInfo{User: user, Session: session})
		next(w, r.WithContext(ctx))
	}
}
