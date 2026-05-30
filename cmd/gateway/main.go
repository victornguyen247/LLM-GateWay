package main
import (
	"log/slog"
	"os"
	"github.com/joho/godotenv"
	"github.com/victornguyen247/LLM-GateWay/internal/server"
	"github.com/victornguyen247/LLM-GateWay/internal/proxy"
)

func main() {
	// create the logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// load the environment variables
	if err := godotenv.Load(); err != nil {
		logger.Error("Failed to load environment variables", "error", err)
		os.Exit(1)
	}

	// create the server
	s := server.NewServer(
		os.Getenv("GATEWAY_LISTEN"), 
		logger, 
		proxy.NewOpenAIProxy(os.Getenv("OPENAI_UPSTREAM_URL"), os.Getenv("OPENAI_API_KEY"), logger))

	if err := s.Run(); err != nil {
		logger.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}