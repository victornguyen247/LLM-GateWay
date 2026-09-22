package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"

	"github.com/victornguyen247/LLM-GateWay/internal/cache"
	"github.com/victornguyen247/LLM-GateWay/internal/ratelimit"
)

type bufWriter struct {
	http.ResponseWriter
	buf    bytes.Buffer
	status int
}

func (w *bufWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *bufWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}

func RateLimitMiddleware(mgr ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Authorization")
			if key == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			allowed, err := mgr.Allow(r.Context(), key)
			if err != nil {
				slog.Default().Warn("rate limiter backend error; failing open",
					slog.Any("error", err),
				)
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func CacheMiddleware(c cache.Cache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusInternalServerError)
				return
			}
			defer r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(body))

			// Streaming responses aren't cacheable.
			if bytes.Contains(body, []byte(`"stream":true`)) ||
				bytes.Contains(body, []byte(`"stream": true`)) {
				next.ServeHTTP(w, r)
				return
			}

			keyBytes := []byte(r.Method + ":" + r.URL.Path + ":" + string(body))
			key := cache.HashRequest(keyBytes)

			entry, found, err := c.Get(r.Context(), key)
			if err != nil {
				// Fail-open: treat as miss, but log so Redis outages don't hide.
				slog.Default().Warn("cache get failed, treating as miss", "error", err)
			}
			if found {
				// Set ALL headers first — WriteHeader freezes them.
				for k, values := range entry.Headers {
					for _, v := range values {
						w.Header().Add(k, v)
					}
				}
				if entry.ContentType != "" {
					w.Header().Set("Content-Type", entry.ContentType)
				}
				w.Header().Set("X-Cache", "HIT")
				w.WriteHeader(entry.Status)
				_, _ = w.Write(entry.Body)
				return
			}

			w.Header().Set("X-Cache", "MISS")
			bufw := &bufWriter{ResponseWriter: w}
			next.ServeHTTP(bufw, r)

			if bufw.status >= 200 && bufw.status < 300 {
				if setErr := c.Set(r.Context(), key, cache.Entry{
					Body:        bufw.buf.Bytes(),
					ContentType: bufw.Header().Get("Content-Type"),
					Status:      bufw.status,
					Headers:     bufw.Header().Clone(),
				}); setErr != nil {
					slog.Default().Warn("cache set failed", "error", setErr)
				}
			}
		})
	}
}
