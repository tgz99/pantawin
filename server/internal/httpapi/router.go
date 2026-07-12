package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/tgz99/pantawin/server/internal/auth"
	"github.com/tgz99/pantawin/server/internal/monitor"
)

func NewRouter(authService *auth.Service, issuer *auth.TokenIssuer, monitorRepo *monitor.Repository) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	authH := &authHandlers{service: authService}
	monitorH := &monitorHandlers{repo: monitorRepo}

	r.Route("/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", authH.register)
			r.Post("/login", authH.login)
			r.Post("/refresh", authH.refresh)
		})

		r.Group(func(r chi.Router) {
			r.Use(requireAuth(issuer))
			r.Get("/monitors", monitorH.listMonitors)
		})
	})

	return r
}
