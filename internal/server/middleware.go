package server

import (

	"net/http"

	"github.com/victornguyen247/LLM-GateWay/internal/ratelimit"
)

func RateLimitMiddleware(mgr *ratelimit.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
			limiter := mgr.Get(r.Header.Get("X-User-ID"))
			if !limiter.Allow() {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}