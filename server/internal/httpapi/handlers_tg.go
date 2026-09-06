package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sviniabanditka/promin/server/internal/auth"
	"github.com/sviniabanditka/promin/server/internal/catalog"
	"github.com/sviniabanditka/promin/server/internal/sync"
	"github.com/sviniabanditka/promin/server/internal/telegram"
)

// tgAppHandlers implements /api/v1/tg/* — the Telegram Mini App surface
// (docs/miniapp.md): initData login, device list with player state, and
// "send to TV". bot is nil when the bot is disabled; cat may be nil in tests.
type tgAppHandlers struct {
	bot  *telegram.Bot
	auth *auth.Service
	sync *sync.Service
	cat  *catalog.Service
}

// remoteActions is the EventRemote action set the Mini App may send.
var remoteActions = map[string]bool{
	"toggle_play": true, "seek": true, "seek_to": true, "prev": true, "next": true, "set_local": true,
	"mute": true, "night": true, "sleep": true,
}

// login: POST /api/v1/tg/auth {init_data, platform?} → {token, user:{id, login}}.
func (h *tgAppHandlers) login(w http.ResponseWriter, r *http.Request) {
	if h.bot == nil {
		writeError(w, http.StatusServiceUnavailable, "telegram_disabled", "Telegram-бот не налаштовано")
		return
	}
	var req struct {
		InitData string `json:"init_data"`
		Platform string `json:"platform"` // Telegram.WebApp.platform, for the device name
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InitData == "" {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	userID, u, err := h.bot.Auth(req.InitData)
	switch {
	case errors.Is(err, telegram.ErrInitData):
		writeError(w, http.StatusUnauthorized, "tg_invalid", "невірні або застарілі дані Telegram")
		return
	case errors.Is(err, telegram.ErrNotLinked):
		writeError(w, http.StatusForbidden, "tg_not_linked", "цей Telegram не прив'язано до профілю")
		return
	case err != nil:
		writeInternal(w, err)
		return
	}
	name := "Telegram"
	if u.FirstName != "" {
		name = u.FirstName + " · Telegram"
	}
	if req.Platform != "" {
		name += " (" + req.Platform + ")"
	}
	res, err := h.auth.LoginTelegram(userID, name)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": res.Token,
		"user":  map[string]any{"id": res.User.ID, "login": res.User.Login},
	})
}

// devices: GET /api/v1/tg/devices → every session of the profile with
// online (live socket) and state (last player report or null).
func (h *tgAppHandlers) devices(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	devs, err := h.auth.ListDevices(info.User.ID, info.Session.Token)
	if err != nil {
		writeInternal(w, err)
		return
	}
	hub := h.sync.Hub()
	online := map[string]bool{}
	for _, d := range hub.OnlineDevices(info.User.ID) {
		online[d.ID] = true
	}
	out := make([]map[string]any, 0, len(devs))
	for _, d := range devs {
		out = append(out, map[string]any{
			"id":       d.TokenID,
			"name":     d.DeviceName,
			"type":     d.DeviceType,
			"current":  d.Current,
			"online":   online[d.TokenID],
			"state":    hub.PlayerState(info.User.ID, d.TokenID),
			"settings": hub.DeviceSettings(info.User.ID, d.TokenID),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

type tgSendRequest struct {
	DeviceID string `json:"device_id"`
	Open     *struct {
		TMDBID    int    `json:"tmdb_id"`
		MediaType string `json:"media_type"`
		Season    int    `json:"season"`
		Episode   int    `json:"episode"`
		Resume    bool   `json:"resume"`
	} `json:"open"`
	Remote *struct {
		Action string  `json:"action"`
		Value  float64 `json:"value"`
		Key    string  `json:"key"`
		Str    string  `json:"str"`
	} `json:"remote"`
}

// send: POST /api/v1/tg/send {device_id, open|remote} → 204. Publishes
// open_title or remote to the user's sockets; only the named device acts.
func (h *tgAppHandlers) send(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	var req tgSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeviceID == "" || (req.Open == nil) == (req.Remote == nil) {
		writeBadRequest(w, "потрібні device_id та рівно одне з open | remote")
		return
	}
	if req.Open != nil && (req.Open.TMDBID <= 0 || (req.Open.MediaType != "movie" && req.Open.MediaType != "tv") || req.Open.Season < 0 || req.Open.Episode < 0) {
		writeBadRequest(w, "open: tmdb_id і media_type (movie|tv) обов'язкові")
		return
	}
	if req.Remote != nil && !remoteActions[req.Remote.Action] {
		writeBadRequest(w, "remote: невідома дія")
		return
	}
	if req.Remote != nil && req.Remote.Action == "set_local" && (!sync.DeviceSettingKeys[req.Remote.Key] || (req.Remote.Str != "true" && req.Remote.Str != "false")) {
		writeBadRequest(w, "set_local: key ∈ {legacy_tv_mode, reduce_motion, debug_mode}, str ∈ {true, false}")
		return
	}
	hub := h.sync.Hub()
	online := false
	for _, d := range hub.OnlineDevices(info.User.ID) {
		online = online || d.ID == req.DeviceID
	}
	if !online {
		writeNotFound(w, "device_offline", "пристрій офлайн")
		return
	}
	if req.Remote != nil {
		hub.Publish(info.User.ID, sync.EventRemote, telegram.RemotePayload{DeviceID: req.DeviceID, Action: req.Remote.Action, Value: req.Remote.Value, Key: req.Remote.Key, Str: req.Remote.Str})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	title := ""
	if h.cat != nil {
		lang := "uk"
		if s, err := h.sync.GetSettings(info.User.ID); err == nil && s["lang"] != "" {
			lang = s["lang"]
		}
		if t, ok := h.cat.CardCached(req.Open.MediaType, req.Open.TMDBID, lang); ok {
			title = t.Title
		}
	}
	hub.Publish(info.User.ID, sync.EventOpenTitle, telegram.OpenTitlePayload{
		TMDBID: req.Open.TMDBID, MediaType: req.Open.MediaType, DeviceID: req.DeviceID, Title: title,
		Resume: req.Open.Resume, Season: req.Open.Season, Episode: req.Open.Episode,
	})
	w.WriteHeader(http.StatusNoContent)
}
