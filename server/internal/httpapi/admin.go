package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/sviniabanditka/promin/server/internal/auth"
	"github.com/sviniabanditka/promin/server/internal/store"
)

// adminHandlers serve the phone-facing admin panel at /admin: a FORM login
// (password → real session cookie, NOT Basic Auth) and profile CRUD. The admin
// is user 1 (argon2 password). Every /admin/* JSON route requires an "admin"
// session. The HTML/JS target is modern mobile Chrome — no ES5 constraint here.
type adminHandlers struct {
	svc    *auth.Service
	logger *slog.Logger
}

const adminCookie = "promin_admin"

// gate resolves the admin cookie and returns the session, or writes 401.
func (h *adminHandlers) gate(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie(adminCookie)
	if err != nil || c.Value == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "потрібен вхід")
		return false
	}
	session, user, err := h.svc.ResolveToken(c.Value)
	if err != nil || session.DeviceType != "admin" || !user.IsAdmin() {
		writeError(w, http.StatusUnauthorized, "unauthorized", "потрібен вхід")
		return false
	}
	return true
}

func (h *adminHandlers) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		writeBadRequest(w, "потрібен пароль")
		return
	}
	result, err := h.svc.AdminLogin(clientIP(r), req.Password)
	if err != nil {
		writeAuthError(w, err) // maps invalid_credentials/rate_limited
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: result.Token, Path: "/admin",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: 7200,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *adminHandlers) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(adminCookie); err == nil {
		_ = h.svc.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "", Path: "/admin", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

type profileView struct {
	ID      int64  `json:"id"`
	Login   string `json:"login"`
	IsAdmin bool   `json:"is_admin"`
	HasPIN  bool   `json:"has_pin"`
}

func (h *adminHandlers) listProfiles(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	users, err := h.svc.Users().List()
	if err != nil {
		writeInternal(w, err)
		return
	}
	out := make([]profileView, 0, len(users))
	for _, u := range users {
		out = append(out, profileView{ID: u.ID, Login: u.Login, IsAdmin: u.IsAdmin(), HasPIN: u.PinLookup.Valid})
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

func (h *adminHandlers) createProfile(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req struct {
		Login string `json:"login"`
		PIN   string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Login == "" {
		writeBadRequest(w, "потрібне ім'я профілю")
		return
	}
	if req.PIN != "" && !isSixDigits(req.PIN) {
		writeBadRequest(w, "PIN має бути 6 цифр")
		return
	}
	user, err := h.svc.Users().Create(req.Login, "", time.Now().Unix())
	if err != nil {
		h.mapWriteErr(w, err)
		return
	}
	if req.PIN != "" {
		if err := h.svc.SetProfilePIN(user.ID, req.PIN); err != nil {
			// roll back the just-created profile so a taken PIN doesn't orphan it
			_ = h.svc.Users().Delete(user.ID)
			h.mapWriteErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": user.ID})
}

func (h *adminHandlers) patchProfile(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, ok := adminID(w, r)
	if !ok {
		return
	}
	var req struct {
		Login *string `json:"login"`
		PIN   *string `json:"pin"` // "" clears the PIN, 6 digits sets it, absent = untouched
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, "невірне тіло")
		return
	}
	if req.Login != nil {
		if *req.Login == "" {
			writeBadRequest(w, "ім'я не може бути порожнім")
			return
		}
		if err := h.svc.Users().UpdateLogin(id, *req.Login); err != nil {
			h.mapWriteErr(w, err)
			return
		}
	}
	if req.PIN != nil {
		if *req.PIN != "" && !isSixDigits(*req.PIN) {
			writeBadRequest(w, "PIN має бути 6 цифр")
			return
		}
		if err := h.svc.SetProfilePIN(id, *req.PIN); err != nil {
			h.mapWriteErr(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandlers) deleteProfile(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, ok := adminID(w, r)
	if !ok {
		return
	}
	if id == 1 {
		writeError(w, http.StatusForbidden, "forbidden", "не можна видалити адміна")
		return
	}
	if err := h.svc.Users().Delete(id); err != nil {
		writeInternal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandlers) mapWriteErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrLoginTaken):
		writeError(w, http.StatusConflict, "login_taken", "таке ім'я вже є")
	case errors.Is(err, store.ErrPinTaken):
		writeError(w, http.StatusConflict, "pin_taken", "такий PIN вже зайнятий")
	default:
		writeInternal(w, err)
	}
}

func adminID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeBadRequest(w, "невірний id")
		return 0, false
	}
	return id, true
}

// page serves the always-open admin SPA shell. Its JS probes /admin/profiles:
// 401 → login form, 200 → roster.
func (h *adminHandlers) page(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(adminHTML))
}
