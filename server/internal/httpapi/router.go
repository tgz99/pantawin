package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
	"github.com/tgz99/pantawin/server/internal/auth"
	"github.com/tgz99/pantawin/server/internal/monitor"
	"github.com/tgz99/pantawin/server/internal/ssrf"
)

type RouterDeps struct {
	AuthService *auth.Service
	Issuer      *auth.TokenIssuer
	MonitorRepo *monitor.Repository
	Guard       *ssrf.Guard
	Scheduler   SchedulerControl
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

	r.Route("/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			// Spec section 8: rate limiting on auth endpoints (brute-force guard).
			r.Use(rateLimit(deps.Redis, "auth", 10, time.Minute))
			r.Post("/register", authH.register)
			r.Post("/login", authH.login)
			r.Post("/refresh", authH.refresh)
		})

		r.Group(func(r chi.Router) {
			r.Use(requireAuth(deps.Issuer))

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
		})
	})

	return r
}
