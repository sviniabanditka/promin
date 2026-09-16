package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sviniabanditka/promin/server/internal/auth"
	"github.com/sviniabanditka/promin/server/internal/store"
	"github.com/sviniabanditka/promin/server/internal/tv"
)

// adminHandlers serve the phone-facing admin panel at /admin: a FORM login
// (password → real session cookie, NOT Basic Auth), profile CRUD with
// per-profile feature access, a profile's devices and Telegram chats, and the
// live-TV maintenance (status, resync, manual EPG mapping). The admin is user 1
// (argon2 password). Every /admin/* JSON route requires an "admin" session.
// The HTML/JS target is modern mobile Chrome — no ES5 constraint here.
type adminHandlers struct {
	svc       *auth.Service
	logger    *slog.Logger
	tg        *store.TelegramRepo // nil-safe: no Telegram → empty lists
	tv        *tv.Service         // nil / disabled → TV routes answer 503
	version   string
	youtubeOn bool
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

// ---- profiles ---------------------------------------------------------------------

type profileView struct {
	ID       int64          `json:"id"`
	Login    string         `json:"login"`
	IsAdmin  bool           `json:"is_admin"`
	HasPIN   bool           `json:"has_pin"`
	Features store.Features `json:"features"`
	// Restricted: users.features is set (not the "everything" default).
	Restricted bool  `json:"restricted"`
	Devices    int   `json:"devices"`
	LastSeen   int64 `json:"last_seen"` // newest session activity, 0 = never
	Telegram   int   `json:"telegram"`
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
		v := profileView{ID: u.ID, Login: u.Login, IsAdmin: u.IsAdmin(), HasPIN: u.PinLookup.Valid, Features: u.Features(), Restricted: strings.TrimSpace(u.FeaturesRaw) != ""}
		if devs, err := h.svc.ListDevices(u.ID, ""); err == nil {
			for _, d := range devs {
				if d.DeviceType == "admin" {
					continue
				}
				v.Devices++
				if d.LastSeen > v.LastSeen {
					v.LastSeen = d.LastSeen
				}
			}
		}
		if h.tg != nil {
			if links, err := h.tg.List(u.ID); err == nil {
				v.Telegram = len(links)
			}
		}
		out = append(out, v)
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
		Login    *string         `json:"login"`
		PIN      *string         `json:"pin"`      // "" clears the PIN, 6 digits sets it, absent = untouched
		Features *store.Features `json:"features"` // whole object; absent = untouched
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
	if req.Features != nil {
		if id == 1 {
			writeError(w, http.StatusForbidden, "forbidden", "адмін має все")
			return
		}
		f := *req.Features
		if h.tv != nil {
			// Keep only configured countries; bad codes would silently hide everything.
			var keep []string
			for _, c := range f.TVCountries {
				for _, have := range h.tv.Countries() {
					if strings.EqualFold(c, have) {
						keep = append(keep, have)
					}
				}
			}
			f.TVCountries = keep
		}
		if f.TVCountries == nil {
			f.TVCountries = []string{}
		}
		if err := h.svc.Users().UpdateFeatures(id, f.JSON()); err != nil {
			writeInternal(w, err)
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

// ---- devices / telegram of a profile ----------------------------------------------

func (h *adminHandlers) listDevices(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, ok := adminID(w, r)
	if !ok {
		return
	}
	devs, err := h.svc.ListDevices(id, "")
	if err != nil {
		writeInternal(w, err)
		return
	}
	out := []map[string]any{}
	for _, d := range devs {
		if d.DeviceType == "admin" {
			continue
		}
		out = append(out, map[string]any{"token_id": d.TokenID, "device_name": d.DeviceName, "device_type": d.DeviceType, "created_at": d.CreatedAt, "last_seen": d.LastSeen})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (h *adminHandlers) revokeDevice(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, ok := adminID(w, r)
	if !ok {
		return
	}
	if err := h.svc.RevokeDevice(id, r.PathValue("token_id"), "", true); err != nil {
		writeAuthError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokeAll: every session of the profile (TV, Mini App) is logged out.
func (h *adminHandlers) revokeAll(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, ok := adminID(w, r)
	if !ok || id == 1 {
		if ok {
			writeError(w, http.StatusForbidden, "forbidden", "не для адміна")
		}
		return
	}
	if err := h.svc.RevokeAll(id); err != nil {
		writeInternal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandlers) listTelegram(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, ok := adminID(w, r)
	if !ok {
		return
	}
	links := []store.TelegramLink{}
	if h.tg != nil {
		if l, err := h.tg.List(id); err == nil && l != nil {
			links = l
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

func (h *adminHandlers) unlinkTelegram(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, ok := adminID(w, r)
	if !ok {
		return
	}
	chat, err := strconv.ParseInt(r.PathValue("chat_id"), 10, 64)
	if err != nil || h.tg == nil {
		writeBadRequest(w, "невірний chat_id")
		return
	}
	if err := h.tg.UnlinkChatOfUser(id, chat); err != nil {
		writeInternal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- status / live TV -----------------------------------------------------------------

func (h *adminHandlers) status(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	out := map[string]any{"version": h.version, "youtube": h.youtubeOn, "tv": h.tv != nil && h.tv.Enabled(), "now": time.Now().Unix()}
	if h.tv != nil && h.tv.Enabled() {
		out["countries"] = h.tv.Countries()
		if st, err := h.tv.Repo().Stats(); err == nil {
			out["tv_stats"] = st
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *adminHandlers) tvOn(w http.ResponseWriter) bool {
	if h.tv == nil || !h.tv.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "tv_disabled", "розділ ТВ вимкнено")
		return false
	}
	return true
}

// tvResync: POST /admin/tv/resync {kind: "catalogue"|"check"|"epg"}
func (h *adminHandlers) tvResync(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !h.tvOn(w) {
		return
	}
	var req struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Kind != "catalogue" && req.Kind != "check" && req.Kind != "epg") {
		writeBadRequest(w, "kind: catalogue | check | epg")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": h.tv.Resync(req.Kind)})
}

// tvChannels: GET /admin/tv/channels?q=&country=&noepg=1
func (h *adminHandlers) tvChannels(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !h.tvOn(w) {
		return
	}
	q := r.URL.Query()
	items, err := h.tv.Repo().AdminChannels(q.Get("q"), strings.ToUpper(q.Get("country")), q.Get("noepg") == "1", 200)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// tvEpgSearch: GET /admin/tv/epg/search?q= — feed channels by display name.
func (h *adminHandlers) tvEpgSearch(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !h.tvOn(w) {
		return
	}
	items, err := h.tv.Repo().SearchEPGChannels(r.URL.Query().Get("q"), 40)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// tvSetEpg: PUT /admin/tv/channels/{cid}/epg {source, xmltv_id} — xmltv_id ""
// pins "no guide". Applies immediately.
func (h *adminHandlers) tvSetEpg(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !h.tvOn(w) {
		return
	}
	cid := r.PathValue("cid")
	var req struct {
		Source  string `json:"source"`
		XMLTVID string `json:"xmltv_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !tvChannelID.MatchString(cid) {
		writeBadRequest(w, "невірне тіло")
		return
	}
	if req.XMLTVID != "" && req.Source == "" {
		writeBadRequest(w, "потрібне джерело")
		return
	}
	if _, err := h.tv.Repo().Channel(cid); err != nil {
		writeNotFound(w, "not_found", "каналу немає")
		return
	}
	if err := h.tv.Repo().SetEPGOverride(store.EPGOverride{ChannelID: cid, Source: req.Source, XMLTVID: req.XMLTVID}); err != nil {
		writeInternal(w, err)
		return
	}
	// The guide is stored per feed channel, so the mapping applies at once.
	key := ""
	if req.XMLTVID != "" {
		key = req.Source + ":" + req.XMLTVID
	}
	if err := h.tv.Repo().SetEPGIDs(map[string]string{cid: key}); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"epg_id": key})
}

// tvSetTitle: PATCH /admin/tv/channels/{cid} {title} — our display name, "" resets.
func (h *adminHandlers) tvSetTitle(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !h.tvOn(w) {
		return
	}
	cid := r.PathValue("cid")
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !tvChannelID.MatchString(cid) {
		writeBadRequest(w, "невірне тіло")
		return
	}
	title := strings.TrimSpace(req.Title)
	if len([]rune(title)) > 60 {
		writeBadRequest(w, "назва до 60 символів")
		return
	}
	if err := h.tv.Repo().SetTitle(cid, title); err != nil {
		writeInternal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandlers) tvClearEpg(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !h.tvOn(w) {
		return
	}
	cid := r.PathValue("cid")
	if !tvChannelID.MatchString(cid) {
		writeBadRequest(w, "невірний id")
		return
	}
	if err := h.tv.Repo().DeleteEPGOverride(cid); err != nil {
		writeInternal(w, err)
		return
	}
	// Back to name matching, recomputed from the stored feed channel lists.
	if _, err := h.tv.RematchEPG(); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"epg_id": h.tv.Repo().EPGID(cid)})
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
// 401 → login form, 200 → panel.
func (h *adminHandlers) page(w http.ResponseWriter, r *http.Request) {
	noFraming(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(adminHTML))
}
