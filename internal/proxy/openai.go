package proxy

import (
	"context"
	"io"
	"net/http"
	"time"
	"bytes"
	"log/slog"
	"github.com/google/uuid"
)

// struct to hold the proxy configuration
type OpenAIProxy struct {
	// upstream URL to forward the request to
	upstreamURL string
	// api key to authenticate the request
	apiKey string
	// client to make the upstream request
	client *http.Client
	// logger to log the requests and responses
	logger *slog.Logger
}

// function to create a new proxy
func NewProxy(upstreamURL, apiKey string, logger *slog.Logger) *Proxy {
	return &Proxy{
		upstreamURL: upstreamURL,
		apiKey: apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// function to handle the request
func (p *Proxy) Handle(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	ctx := context.WithValue(r.Context(), "requestID", requestID)
	logger := p.logger.With("requestID", requestID)
	
	startTime := time.Now()
	
	// Read the request body
	body, err := io.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil{
		logger.Error("failed to read request body", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
    
	// Create the request
	req, err := http.NewRequestWithContext(r.Context(), "POST", p.upstreamURL, bytes.NewReader(body))
	if err != nil{
		p.logger.Error("failed to create request", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	// Forward the request
	resp, err := p.client.Do(req)
	if err != nil{
		p.logger.Error("failed to forward request", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Log the upstream response
	logger.Info("upstream response received", 
				slog.Int("upstream_status", resp.StatusCode), 
				slog.Duration("duration", time.Since(startTime)), 
				slog.Int64("response_size", resp.ContentLength),
				)

	// Write the response
	w.WriteHeader(resp.StatusCode)
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	_, err = io.Copy(w, resp.Body)
	if err != nil{
		p.logger.Error("failed to copy response body", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}