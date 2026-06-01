package main
import (
	"log/slog"
	"os"
	// "github.com/joho/godotenv"
	"github.com/victornguyen247/LLM-GateWay/internal/server"
	"github.com/victornguyen247/LLM-GateWay/internal/proxy"
	"github.com/victornguyen247/LLM-GateWay/internal/ratelimit"
	"github.com/victornguyen247/LLM-GateWay/internal/cache"
	//"strconv"
	"time"
	"context"
	"syscall"
	"os/signal"
)

func main() {
	// create the logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// load the environment variables
	/*if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		logger.Error("Failed to load environment variables", "error", err)
		os.Exit(1)
	}*/
	rps := 2.0
	burst := 2
    //rps, err := strconv.ParseFloat(os.Getenv("RATE_LIMIT_RPS"), 64)
    //if err != nil && !os.IsNotExist(err){
    //    logger.Error("Failed to parse RATE_LIMIT_RPS", "error", err)
    //    os.Exit(1)
    //}
    //burst, err := strconv.ParseInt(os.Getenv("RATE_LIMIT_BURST"), 10, 32)
    //if err != nil && !os.IsNotExist(err){
    //    logger.Error("Failed to parse RATE_LIMIT_BURST", "error", err)
    //    os.Exit(1)
    //}

	// create the cache
	//size, err := strconv.Atoi(os.Getenv("CACHE_SIZE"))
	//if err != nil {
	//	logger.Error("Failed to parse CACHE_SIZE", "error", err)
	//	os.Exit(1)
	//}
	//ttl, err := time.ParseDuration(os.Getenv("CACHE_TTL"))
	//if err != nil {
	//	logger.Error("Failed to parse CACHE_TTL", "error", err)
	//	os.Exit(1)
	//}
	size := 1000
	ttl := 1 * time.Hour
	cache, err := cache.NewCache( size, ttl)
	if err != nil || cache == nil {
		logger.Error("Failed to create cache", "error", err)
		os.Exit(1)
	}

	// create the server
	gateway_listen := ":8080"
	openai_upstream_url := "https://api.openai.com"
	s := server.NewServer(
		gateway_listen, //os.Getenv("GATEWAY_LISTEN"), 
		logger, 
		proxy.NewOpenAIProxy(openai_upstream_url, os.Getenv("OPENAI_API_KEY"), logger), //os.Getenv("OPENAI_UPSTREAM_URL"), os.Getenv("OPENAI_API_KEY"), logger),
		ratelimit.NewManager(rps, int(burst)),
		cache)

	// create the context and stop function
    ctx , stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
    // start the server
	go func() {
		if err := s.Run(); err != nil{
			logger.Error("Failed to start server", "error", err)
			os.Exit(1)
		}
	}()

    // wait for the signal
	<-ctx.Done()
	s.Shutdown(ctx)
	logger.Info("gracefully shutting down server")
	os.Exit(0)
}