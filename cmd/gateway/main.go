package main
import (
	"log/slog"
	"os"
	"github.com/victornguyen247/LLM-GateWay/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	s := server.NewServer("localhost:8080", logger)

	if err := s.Run(); err != nil {
		logger.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}