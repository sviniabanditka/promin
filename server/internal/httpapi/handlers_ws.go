package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/sviniabanditka/promin/server/internal/sync"
)

// wsHandlers implements GET /api/v1/ws (docs/api.md):
// upgrades to a WebSocket and pushes every sync.Event for the
// authenticated user's account (published from any of their devices) to
// this connection, until it closes.
type wsHandlers struct {
	syncSvc *sync.Service
	logger  *slog.Logger
}

// wsWriteTimeout bounds each individual server->client write so a stalled
// client can't leak the per-connection goroutine forever.
const wsWriteTimeout = 10 * time.Second

type wsClientMessage struct {
	Type string `json:"type"`
}

// serve requires requireAuthMedia to have already populated authInfo (see
// server.go) — the token travels as ?t=<token> since some webview upgrade
// paths can't set arbitrary headers (docs/api.md).
func (h *wsHandlers) serve(w http.ResponseWriter, r *http.Request) {
	info, ok := authFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "потрібна авторизація")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		h.logger.Warn("ws: accept failed", "error", err, "user_id", info.User.ID)
		return
	}
	defer conn.CloseNow()

	events, unsubscribe := h.syncSvc.Hub().Subscribe(info.User.ID, info.Session.ID, info.Session.DeviceName)
	defer unsubscribe()

	writeCtx, cancelWrites := context.WithCancel(r.Context())
	defer cancelWrites()

	go h.pushLoop(writeCtx, conn, events)

	h.logger.Info("ws: connected", "user_id", info.User.ID)
	h.readLoop(r.Context(), conn)
	h.logger.Info("ws: disconnected", "user_id", info.User.ID)
}

// pushLoop forwards hub events to the client as JSON frames until
// writeCtx is cancelled (by readLoop returning) or the channel closes
// (unsubscribe on the way out).
func (h *wsHandlers) pushLoop(writeCtx context.Context, conn *websocket.Conn, events <-chan sync.Event) {
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(writeCtx, wsWriteTimeout)
			err := wsjson.Write(ctx, conn, ev)
			cancel()
			if err != nil {
				return
			}
		case <-writeCtx.Done():
			return
		}
	}
}

// readLoop only exists to service client->server keepalive
// ({"type":"ping"} -> {"type":"pong"}, docs/api.md) and
// to detect the connection closing; sync mutations always go through
// REST, never over this channel.
func (h *wsHandlers) readLoop(ctx context.Context, conn *websocket.Conn) {
	for {
		var msg wsClientMessage
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			var closeErr websocket.CloseError
			if !errors.As(err, &closeErr) {
				h.logger.Debug("ws: read error", "error", err)
			}
			return
		}
		if msg.Type == "ping" {
			wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			_ = wsjson.Write(wctx, conn, wsClientMessage{Type: "pong"})
			cancel()
		}
	}
}
