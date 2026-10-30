package httpapi

import (
	"context"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"github.com/frangte/mnchat/internal/domain"
)

const (
	wsPingInterval = 30 * time.Second
	wsWriteTimeout = 5 * time.Second
)

// websocket streams workspace events: GET /api/v1/ws?token=<jwt>&workspace_id=<id>.
func (h *Handler) websocket(c *gin.Context) {
	uid, err := h.auth.Authenticate(c.Query("token"))
	if err != nil {
		writeError(c, err)
		return
	}
	wsID, err := strconv.ParseInt(c.Query("workspace_id"), 10, 64)
	if err != nil {
		writeError(c, domain.ErrNotFound)
		return
	}
	if err := h.chat.RequireMember(c, wsID, uid); err != nil {
		writeError(c, err)
		return
	}

	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{OriginPatterns: h.wsOrigins})
	if err != nil {
		return // Accept has already written the HTTP error
	}
	defer conn.CloseNow()

	client := h.hub.Subscribe(wsID, uid)
	defer h.hub.Unsubscribe(client)

	// Clients only receive; CloseRead handles control frames and cancels ctx on disconnect.
	ctx := conn.CloseRead(context.Background())
	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-client.Messages():
			if !ok {
				conn.Close(websocket.StatusGoingAway, "reconnect")
				return
			}
			wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
