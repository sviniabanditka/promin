package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

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
	"set_voice": true, "set_subtitle": true, "volume": true,
	// Two TVs of one account playing the same frame (docs/player.md): the
	// leading player ticks its position, the follower holds on to it.
	"sync_state": true, "sync_stop": true,
	// D-pad relayed to the TV UI (works on any screen, not just the player).
	"nav_up": true, "nav_down": true, "nav_left": true, "nav_right": true, "nav_ok": true, "nav_back": true,
}

var platformRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

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
	// Telegram's platform names are short lowercase tokens; the field lands in
	// the device name and is the session-reuse key, so anything else is
	// dropped rather than stored.
	if platformRe.MatchString(req.Platform) {
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
	OpenYT *struct {
		VideoID string `json:"video_id"`
	} `json:"open_yt"`
	OpenTV *struct {
		ChannelID string `json:"channel_id"`
		Title     string `json:"title"`
	} `json:"open_tv"`
	Remote *struct {
		Action string  `json:"action"`
		Value  float64 `json:"value"`
		Key    string  `json:"key"`
		Str    string  `json:"str"`
	} `json:"remote"`
}

// send: POST /api/v1/tg/send {device_id, open|remote|open_yt} → 204. Publishes
// open_title, remote or open_yt to the user's sockets; only the named device acts.
func (h *tgAppHandlers) send(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	var req tgSendRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	kinds := 0
	for _, set := range []bool{req.Open != nil, req.Remote != nil, req.OpenYT != nil, req.OpenTV != nil} {
		if set {
			kinds++
		}
	}
	if err != nil || req.DeviceID == "" || kinds != 1 {
		writeBadRequest(w, "потрібні device_id та рівно одне з open | remote | open_yt | open_tv")
		return
	}
	if req.OpenTV != nil && (!tvChannelID.MatchString(req.OpenTV.ChannelID) || len(req.OpenTV.Title) > 200) {
		writeBadRequest(w, "open_tv: невірний channel_id")
		return
	}
	if req.OpenYT != nil && !ytVideoID.MatchString(req.OpenYT.VideoID) {
		writeBadRequest(w, "open_yt: невірний video_id")
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
	if req.Remote != nil && (req.Remote.Action == "set_voice" || req.Remote.Action == "set_subtitle") && (req.Remote.Str == "" || len(req.Remote.Str) > 200) {
		writeBadRequest(w, req.Remote.Action+": str (id) обов'язковий")
		return
	}
	if req.Remote != nil && req.Remote.Action == "volume" && (req.Remote.Value < 0 || req.Remote.Value > 100) {
		writeBadRequest(w, "volume: value 0..100")
		return
	}
	if req.Remote != nil && req.Remote.Action == "sync_state" &&
		(req.Remote.Value < 0 || (req.Remote.Str != "playing" && req.Remote.Str != "paused")) {
		writeBadRequest(w, "sync_state: value ≥ 0, str ∈ {playing, paused}")
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
	if req.OpenYT != nil {
		hub.Publish(info.User.ID, sync.EventOpenYouTube, telegram.OpenYouTubePayload{VideoID: req.OpenYT.VideoID, DeviceID: req.DeviceID})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if req.OpenTV != nil {
		hub.Publish(info.User.ID, sync.EventOpenTV, telegram.OpenTVPayload{ChannelID: req.OpenTV.ChannelID, DeviceID: req.DeviceID, Title: req.OpenTV.Title})
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
