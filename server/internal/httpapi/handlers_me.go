package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/sviniabanditka/promin/server/internal/auth"
	"github.com/sviniabanditka/promin/server/internal/sync"
)

// meHandlers: Settings → Danger zone.
type meHandlers struct {
	svc    *sync.Service
	auth   *auth.Service
	logger *slog.Logger
}

// DELETE /api/v1/me/history — watch history + resume positions.
func (h *meHandlers) clearHistory(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	if err := h.svc.ClearHistory(info.User.ID); err != nil {
		writeInternal(w, err)
		return
	}
	h.logger.Info("danger zone: history cleared", "user", info.User.ID)
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/me/data — everything but the profile and its PIN; every
// device (this one included) is signed out and lands on the PIN screen.
func (h *meHandlers) deleteData(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	if err := h.svc.ClearAll(info.User.ID); err != nil {
		writeInternal(w, err)
		return
	}
	if err := h.auth.RevokeAll(info.User.ID); err != nil {
		writeInternal(w, err)
		return
	}
	h.logger.Info("danger zone: all user data deleted, sessions revoked", "user", info.User.ID)
	w.WriteHeader(http.StatusNoContent)
}
