package server

import (
	"net/http"
	"encoding/json"
	"log/slog"
	"time"
)

type Server struct {
	httpServer *http.Server
	logger *slog.Logger
	mux *http.ServeMux
}

// Function to create a new server
func NewServer(addr string, logger *slog.Logger) *Server {
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
	}
	return s
}

// Function to register routes
func (s *Server) registerRoutes(){
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request){
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}

func (s *Server) Run() error {
	s.registerRoutes()
	s.logger.Info("Starting server", "address", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil {
		return err
	}
	return nil
}