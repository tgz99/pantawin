package httpapi

import (
	"net/http"
	"strings"

	"github.com/tgz99/pantawin/server/internal/auth"
)

// requireAuth validates the Authorization: Bearer <access-token> header and
// injects the authenticated user id into the request context. Rejects with
// 401 on any missing/invalid/expired/wrong-type token.
func requireAuth(issuer *auth.TokenIssuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) {
				writeError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
				return
			}
			token := strings.TrimPrefix(header, prefix)

			userID, err := issuer.ParseAccessToken(token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid or expired access token")
				return
			}

			ctx := withUserID(r.Context(), userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
