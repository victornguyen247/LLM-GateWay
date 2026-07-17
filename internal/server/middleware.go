package server

import (
	"bytes"
	"net/http"

	"github.com/victornguyen247/LLM-GateWay/internal/ratelimit"
	"github.com/victornguyen247/LLM-GateWay/internal/cache"
	"io"
)

type bufWriter struct {
	http.ResponseWriter
	buf bytes.Buffer
	status int
}

func (w *bufWriter) WriteHeader(code int) {
	w.status =  code
	w.ResponseWriter.WriteHeader(code)
}

func (w *bufWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}

func RateLimitMiddleware(mgr Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
			key := r.Header.Get("Authorization")
			if key == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			allowed,err := mgr.Allow(r.Context(), key)
			if err != nil {
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

func CacheMiddleware(c *cache.Cache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusInternalServerError)
				return
			}	
			defer r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(body))

			// ignore streaming responses because they are not cacheable
			if bytes.Contains(body, []byte("stream")) {
				next.ServeHTTP(w, r)
				return
			}

			key_bytes := []byte(r.Method + ":" + r.URL.Path + ":" + string(body))
			key := cache.HashRequest(key_bytes)
			// check if the entry is in the cache
			entry, found := c.Get(key)
			if found { // cache hit
				w.WriteHeader(entry.Status)	// write the status code to the client
				w.Header().Set("Content-Type", entry.ContentType)
				w.Header().Set("X-Cache", "HIT") // set the cache header to hit
				for key, values := range entry.Headers {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.Write(entry.Body)
				return
			}

			// cache miss
			w.Header().Set("X-Cache", "MISS") // set the cache header to miss
			bufw := &bufWriter{ResponseWriter: w}
			next.ServeHTTP(bufw, r)
			if bufw.status >= 200 && bufw.status < 300 {
				c.Set(key, cache.Entry{
					Body: bufw.buf.Bytes(),
					ContentType: r.Header.Get("Content-Type"),
					Status: bufw.status,
					Headers: bufw.Header().Clone(),
				})
			}
		})
	}
}