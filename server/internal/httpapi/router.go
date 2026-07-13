package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
	"github.com/tgz99/pantawin/server/internal/auth"
	"github.com/tgz99/pantawin/server/internal/device"
	"github.com/tgz99/pantawin/server/internal/monitor"
	"github.com/tgz99/pantawin/server/internal/realtime"
	"github.com/tgz99/pantawin/server/internal/ssrf"
)

type RouterDeps struct {
	AuthService *auth.Service
	Issuer      *auth.TokenIssuer
	MonitorRepo *monitor.Repository
	DeviceRepo  *device.Repository
	Guard       *ssrf.Guard
	Scheduler   SchedulerControl
	Realtime    *realtime.Handler
	Redis       *redis.Client
}

func NewRouter(deps RouterDeps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	authH := &authHandlers{service: deps.AuthService}
	monitorH := &monitorHandlers{repo: deps.MonitorRepo, guard: deps.Guard, sched: deps.Scheduler}
	deviceH := &deviceHandlers{repo: deps.DeviceRepo}

	r.Route("/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			// Spec section 8: rate limiting on auth endpoints (brute-force guard).
			r.Use(rateLimit(deps.Redis, "auth", 10, time.Minute))
			r.Post("/register", authH.register)
			r.Post("/login", authH.login)
			r.Post("/refresh", authH.refresh)
			r.Post("/google", authH.googleLogin)
		})

		// WebSocket handshake authenticates via header OR ?access_token=
		// query param (browsers can't set an Authorization header on the WS
		// upgrade). Kept outside the requireAuth group for that reason.
		r.Get("/ws", wsHandler(deps.Issuer, deps.Realtime))

		r.Group(func(r chi.Router) {
			r.Use(requireAuth(deps.Issuer))

			// Authenticated but still rate-limited: current-password
			// verification is a brute-force target like login.
			r.With(rateLimit(deps.Redis, "change-password", 5, time.Minute)).
				Post("/auth/change-password", authH.changePassword)

			r.Get("/monitors", monitorH.listMonitors)
			r.With(rateLimit(deps.Redis, "monitor-create", 20, time.Minute)).
				Post("/monitors", monitorH.createMonitor)

			r.Route("/monitors/{id}", func(r chi.Router) {
				r.Get("/", monitorH.getMonitor)
				r.Patch("/", monitorH.updateMonitor)
				r.Delete("/", monitorH.deleteMonitor)
				r.Post("/pause", monitorH.pauseMonitor)
				r.Post("/resume", monitorH.resumeMonitor)
			})

			r.Post("/devices", deviceH.register)
		})
	})

	return r
}

// wsHandler authenticates the WebSocket upgrade (Authorization: Bearer, or
// ?access_token=) then hands the connection to the realtime handler.
func wsHandler(issuer *auth.TokenIssuer, rt *realtime.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || token == r.Header.Get("Authorization") {
			token = r.URL.Query().Get("access_token")
		}
		userID, err := issuer.ParseAccessToken(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired access token")
			return
		}
		rt.Serve(w, r, userID)
	}
}
