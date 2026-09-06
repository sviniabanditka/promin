package httpapi

import (
	"net/http"

	"github.com/sviniabanditka/promin/server/internal/telegram"
)

// telegramHandlers implements /api/v1/telegram/* — pairing a Telegram chat
// with the calling profile. bot is nil when the bot is disabled.
type telegramHandlers struct {
	bot *telegram.Bot
}

func (h *telegramHandlers) disabled(w http.ResponseWriter) bool {
	if h.bot == nil {
		writeError(w, http.StatusServiceUnavailable, "telegram_disabled", "Telegram-бот не налаштовано")
		return true
	}
	return false
}

// status: GET /api/v1/telegram/status → {enabled, linked, bot_username}.
func (h *telegramHandlers) status(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	out := map[string]any{"enabled": h.bot != nil, "linked": false, "bot_username": h.bot.Username()}
	if h.bot != nil {
		linked, err := h.bot.IsLinked(info.User.ID)
		if err != nil {
			writeInternal(w, err)
			return
		}
		out["linked"] = linked
	}
	writeJSON(w, http.StatusOK, out)
}

// link: POST /api/v1/telegram/link → {code, deep_link, expires_at}. The code
// is typed (or deep-linked) into the bot within 10 minutes.
func (h *telegramHandlers) link(w http.ResponseWriter, r *http.Request) {
	if h.disabled(w) {
		return
	}
	info, _ := authFrom(r)
	code, expires := h.bot.IssueLinkCode(info.User.ID)
	deepLink := ""
	if u := h.bot.Username(); u != "" {
		deepLink = "https://t.me/" + u + "?start=" + code
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "deep_link": deepLink, "expires_at": expires.Unix()})
}

// unlink: DELETE /api/v1/telegram/link → 204; drops every chat of the profile.
func (h *telegramHandlers) unlink(w http.ResponseWriter, r *http.Request) {
	if h.disabled(w) {
		return
	}
	info, _ := authFrom(r)
	if err := h.bot.Unlink(info.User.ID); err != nil {
		writeInternal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
