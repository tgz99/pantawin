package realtime

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/redis/go-redis/v9"
)

// Handler upgrades GET /v1/ws to a WebSocket, then forwards every event
// published to the authenticated user's Redis channel. Authentication is
// performed by the caller (the router injects the user id); this handler
// only needs the id.
type Handler struct {
	redis  *redis.Client
	logger *slog.Logger
}

func NewHandler(redisClient *redis.Client, logger *slog.Logger) *Handler {
	return &Handler{redis: redisClient, logger: logger}
}

// Serve runs the WebSocket for one authenticated user until the client
// disconnects or ctx is cancelled.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request, userID int64) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The API and the app are same-origin via nginx; the Android client
		// isn't a browser so CORS/origin checks don't apply, but we keep the
		// default origin check for any future browser dashboard on the same host.
		OriginPatterns: []string{"api.pantawin.gratisaja.com", "localhost:*", "127.0.0.1:*"},
	})
	if err != nil {
		h.logger.Debug("ws: accept failed", "error", err)
		return
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Team-monitor events are broadcast on a shared channel (M6) — every
	// dashboard sees them alongside its own personal-monitor events.
	sub := h.redis.Subscribe(ctx, userChannel(userID), teamChannel)
	defer sub.Close()
	ch := sub.Channel()

	// Reader goroutine: detect client disconnect / handle close frames.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	// Keepalive ping so dead connections are reaped and intermediaries don't
	// idle-timeout the socket.
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			pingCtx, pc := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingCtx)
			pc()
			if err != nil {
				return
			}
		case msg, ok := <-ch:
			if !ok {
				return
			}
			writeCtx, wc := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(writeCtx, websocket.MessageText, []byte(msg.Payload))
			wc()
			if err != nil {
				h.logger.Debug("ws: write failed, closing", "error", err)
				return
			}
		}
	}
}
