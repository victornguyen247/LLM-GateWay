package proxy

import (
	"net/http"
	"log/slog"
)

// interface to define the proxy methods
type Proxy interface {
	// function to create a new proxy
	NewProxy(upstreamURL, apiKey string, logger *slog.Logger) *Proxy
	// function to handle the request
	Handle(w http.ResponseWriter, r *http.Request)
}