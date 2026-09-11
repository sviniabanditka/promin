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
// cloudflareNets are Cloudflare's published edge ranges (cloudflare.com/ips).
// CF-Connecting-IP is only believed when the request really came through one
// of them; the node's 443 and the h1 front are reachable directly, and a
// direct client could otherwise forge the header and rotate the rate-limit key.
var cloudflareNets = func() []*net.IPNet {
	cidrs := []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
		"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
		"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
		"2a06:98c0::/29", "2c0f:f248::/32",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

func isCloudflare(ip string) bool {
	p := net.ParseIP(ip)
	if p == nil {
		return false
	}
	for _, n := range cloudflareNets {
		if n.Contains(p) {
			return true
		}
	}
	return false
}

// clientIP is the address rate limits are keyed on.
//
// The immediate peer of our front (Traefik, or nginx on the h1 host) is the
// LAST X-Forwarded-For hop: both fronts drop what the client sent and write
// their own view (Traefik strips untrusted forwarded headers; nginx sets
// $remote_addr). When that peer is a Cloudflare edge, the visitor is in
// CF-Connecting-IP; otherwise the peer IS the visitor and any CF-Connecting-IP
// it sent is a forgery and is ignored.
func clientIP(r *http.Request) string {
	peer := ""
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.LastIndexByte(xff, ','); i >= 0 {
			peer = strings.TrimSpace(xff[i+1:])
		} else {
			peer = strings.TrimSpace(xff)
		}
	}
	if peer == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			peer = r.RemoteAddr
		} else {
			peer = host
		}
	}
	if isCloudflare(peer) {
		if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
			return ip
		}
	}
	return peer
}

// revokeOthers: DELETE /api/v1/auth/devices — every session but the caller's.
func (h *authHandlers) revokeOthers(w http.ResponseWriter, r *http.Request) {
	info, ok := authFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "потрібна авторизація")
		return
	}
	n, err := h.svc.RevokeOthers(info.User.ID, info.Session.Token)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"revoked": n})
}
