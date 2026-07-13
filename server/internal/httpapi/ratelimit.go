package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

// rateLimit is a fixed-window per-IP limiter backed by Redis (spec section
// 8: rate limiting on auth and monitor-creation endpoints). Fixed window is
// deliberately simple — at M1 traffic this guards against brute force and
// runaway clients, not sophisticated abuse.
func rateLimit(redisClient *redis.Client, name string, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// chi's middleware.RealIP has already rewritten RemoteAddr from
			// X-Forwarded-For (nginx sets it).
			key := fmt.Sprintf("pantawin:ratelimit:%s:%s", name, r.RemoteAddr)

			count, err := redisClient.Incr(r.Context(), key).Result()
			if err != nil {
				// Redis down shouldn't take auth down with it — fail open.
				next.ServeHTTP(w, r)
				return
			}
			if count == 1 {
				redisClient.Expire(r.Context(), key, window)
			}
			if count > int64(limit) {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(window.Seconds())))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
