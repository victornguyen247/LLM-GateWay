package server

import (
	"net/http"
	"encoding/json"
	"log/slog"
	"time"
	"github.com/victornguyen247/LLM-GateWay/internal/proxy"
)

type Server struct {
	// http server to listen for requests
	httpServer *http.Server
	// logger to log the requests and responses
	logger *slog.Logger
	// mux to route the requests
	mux *http.ServeMux
	// proxy to forward the requests to the upstream
	proxy proxy.Proxy
}

// Function to create a new server
func NewServer(addr string, logger *slog.Logger, proxy proxy.Proxy) *Server {
	mux := http.NewServeMux()

	s := &Server{
		httpServer: &http.Server{
			Addr: addr,
			Handler: mux,
			ReadTimeout: 5 * time.Second,
			WriteTimeout: 35 * time.Second,
			IdleTimeout: 60 * time.Second,
		},
		logger: logger,
		mux: mux,
		proxy: proxy,
	}
	return s
}

// Function to register routes
func (s *Server) registerRoutes(){
	// register the health check route
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request){
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// forward Gemini /v1beta/* endpoints (generateContent, etc.)
	s.mux.HandleFunc("/v1beta/", s.proxy.Handle)
}

// Function to start the server
func (s *Server) Run() error {
	s.registerRoutes()
	s.logger.Info("Starting server", "address", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil {
		return err
	}
	return nil
}