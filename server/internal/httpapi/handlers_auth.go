package httpapi

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/sviniabanditka/promin/server/internal/auth"
)

// authHandlers wires POST /api/v1/auth/{pin,logout} and GET/DELETE
// /api/v1/auth/devices. Password login + device pairing were removed with the
// PIN rework (the only way in is a PIN or the admin panel).
type authHandlers struct {
	svc *auth.Service
}

type pinRequest struct {
	PIN        string `json:"pin"`
	DeviceName string `json:"device_name"`
	DeviceType string `json:"device_type"`
}

// pinLogin implements POST /api/v1/auth/pin — the TV gate. Open pre-gate: it's
// how a device gets a session in the first place.
func (h *authHandlers) pinLogin(w http.ResponseWriter, r *http.Request) {
	var req pinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	if !isSixDigits(req.PIN) {
		writeBadRequest(w, "PIN має бути 6 цифр")
		return
	}
	result, err := h.svc.LoginPIN(clientIP(r), req.PIN, firstNonEmpty(req.DeviceName, deviceTypeFromUA(r)))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": result.Token,
		"user": map[string]any{
			"id":       result.User.ID,
			"login":    result.User.Login,
			"is_admin": result.User.IsAdmin(),
		},
	})
}

func isSixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (h *authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	info, ok := authFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "потрібна авторизація")
		return
	}
	if err := h.svc.Logout(info.Session.Token); err != nil {
		writeInternal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *authHandlers) listDevices(w http.ResponseWriter, r *http.Request) {
	info, ok := authFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "потрібна авторизація")
		return
	}
	devices, err := h.svc.ListDevices(info.User.ID, info.Session.Token)
	if err != nil {
		writeInternal(w, err)
		return
	}

	out := make([]map[string]any, 0, len(devices))
	for _, d := range devices {
		out = append(out, map[string]any{
			"token_id":    d.TokenID,
			"device_name": d.DeviceName,
			"device_type": d.DeviceType,
			"created_at":  d.CreatedAt,
			"last_seen":   d.LastSeen,
			"current":     d.Current,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (h *authHandlers) revokeDevice(w http.ResponseWriter, r *http.Request) {
	info, ok := authFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "потрібна авторизація")
		return
	}
	tokenID := r.PathValue("token_id")
	force := r.URL.Query().Get("force") == "true"

	err := h.svc.RevokeDevice(info.User.ID, tokenID, info.Session.Token, force)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrDeviceNotFound):
			writeNotFound(w, "device_not_found", "пристрій не знайдено")
		case errors.Is(err, auth.ErrCannotRevokeCurrent):
			writeError(w, http.StatusForbidden, "forbidden", "не можна відкликати поточну сесію без ?force=true")
		default:
			writeInternal(w, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAuthError(w http.ResponseWriter, err error) {
	var rateLimited auth.ErrRateLimited
	switch {
	case errors.Is(err, auth.ErrLoginTaken):
		writeError(w, http.StatusConflict, "login_taken", "цей логін вже зайнятий")
	case errors.Is(err, auth.ErrRegistrationClosed):
		writeError(w, http.StatusForbidden, "registration_closed", "реєстрація закрита")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "логін або пароль невірні")
	case errors.As(err, &rateLimited):
		w.Header().Set("Retry-After", strconv.Itoa(int(rateLimited.RetryAfter.Seconds())))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "забагато спроб входу, спробуйте пізніше")
	default:
		writeInternal(w, err)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// deviceTypeFromUA is a best-effort fallback when the client doesn't send
// device_type explicitly (docs/backend.md task brief: "device_name/
// device_type из тела/UA").
func deviceTypeFromUA(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	switch {
	case strings.Contains(ua, "Tizen"):
		return "tizen"
	case strings.Contains(ua, "SmartTV") || strings.Contains(ua, "webOS"):
		return "webos"
	case strings.Contains(ua, "AndroidTV") || strings.Contains(ua, "Android TV"):
		return "androidtv"
	case ua != "":
		return "browser"
	default:
		return ""
	}
}

// clientIP returns the visitor's address: for the PIN/admin rate limiter and
// for GeoIP. Production sits behind Cloudflare, whose CF-Connecting-IP is the
// trustworthy one; keying the limiter on RemoteAddr meant every user behind
// one CF edge node shared a bucket (false lockouts) while an attacker could
// rotate edges. X-Forwarded-For's first hop and RemoteAddr cover direct/LAN use.
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
