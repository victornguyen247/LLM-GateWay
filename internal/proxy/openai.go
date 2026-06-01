package proxy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
func NewOpenAIProxy(upstreamURL, apiKey string, logger *slog.Logger) *OpenAIProxy {
	return &OpenAIProxy{
		upstreamURL: upstreamURL,
		apiKey:      apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

func (p *OpenAIProxy) upstreamRequestURL(r *http.Request) string {
	base := strings.TrimSuffix(p.upstreamURL, "/")
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	url := base + path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	return url
}

// function to handle the request
func (p *OpenAIProxy) Handle(w http.ResponseWriter, r *http.Request) {
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
    
	// Create the request (preserve path, e.g. /v1/chat/completions)
	req, err := http.NewRequestWithContext(ctx, r.Method, p.upstreamRequestURL(r), bytes.NewReader(body))
	if err != nil{
		logger.Error("failed to create request", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	// Forward the request
	resp, err := p.client.Do(req)
	if err != nil{
		logger.Error("failed to forward request", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Log the upstream response
	logger.Info("upstream response received", 
				slog.Int("upstream_status", resp.StatusCode), 
				slog.Int64("duration", time.Since(startTime).Milliseconds()), 
				slog.Int64("response_size", resp.ContentLength),
				)

	// Write the response
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	if err != nil{
		logger.Error("failed to copy response body", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}