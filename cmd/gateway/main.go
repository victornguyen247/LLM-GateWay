package main
import (
	"log/slog"
	"os"
	// "github.com/joho/godotenv"
	"github.com/victornguyen247/LLM-GateWay/internal/server"
	"github.com/victornguyen247/LLM-GateWay/internal/proxy"
	"github.com/victornguyen247/LLM-GateWay/internal/ratelimit"
	"github.com/victornguyen247/LLM-GateWay/internal/cache"
	"github.com/redis/go-redis/v9"
	"strconv"
	"time"
	"context"
	"syscall"
	"os/signal"
)

// newLimiter builds the configured rate limiter backend. RATE_LIMIT_BACKEND
// selects "memory" (default) or "redis"; the redis backend connects to
// REDIS_URL.
func newLimiter(logger *slog.Logger) ratelimit.Limiter {
	backend := os.Getenv("RATE_LIMIT_BACKEND")
	if backend == "" {
		backend = "memory"
	}

	switch backend {
	case "redis":
		redisURL := os.Getenv("REDIS_URL")
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			logger.Error("Failed to parse REDIS_URL", "error", err)
			os.Exit(1)
		}
		client := redis.NewClient(opts)
		if err := client.Ping(context.Background()).Err(); err != nil {
			logger.Error("Failed to connect to Redis", "error", err)
			os.Exit(1)
		}

		window := time.Second
		if w := os.Getenv("RATE_LIMIT_WINDOW"); w != "" {
			parsed, err := time.ParseDuration(w)
			if err != nil {
				logger.Error("Failed to parse RATE_LIMIT_WINDOW", "error", err)
				os.Exit(1)
			}
			window = parsed
		}

		limit := 2
		if l := os.Getenv("RATE_LIMIT_LIMIT"); l != "" {
			parsed, err := strconv.Atoi(l)
			if err != nil {
				logger.Error("Failed to parse RATE_LIMIT_LIMIT", "error", err)
				os.Exit(1)
			}
			limit = parsed
		}

		logger.Info("rate limiter backend: redis", "redis_url", redisURL, "window", window, "limit", limit)
		return ratelimit.NewRedisLimiter(client, window, limit)

	case "memory":
		rps := 2.0
		if r := os.Getenv("RATE_LIMIT_RPS"); r != "" {
			parsed, err := strconv.ParseFloat(r, 64)
			if err != nil {
				logger.Error("Failed to parse RATE_LIMIT_RPS", "error", err)
				os.Exit(1)
			}
			rps = parsed
		}

		burst := 2
		if b := os.Getenv("RATE_LIMIT_BURST"); b != "" {
			parsed, err := strconv.Atoi(b)
			if err != nil {
				logger.Error("Failed to parse RATE_LIMIT_BURST", "error", err)
				os.Exit(1)
			}
			burst = parsed
		}

		logger.Info("rate limiter backend: memory", "rps", rps, "burst", burst)
		return ratelimit.NewManager(rps, burst)

	default:
		logger.Error("unknown RATE_LIMIT_BACKEND", "backend", backend)
		os.Exit(1)
		return nil
	}
}

func main() {
	// create the logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// load the environment variables
	/*if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		logger.Error("Failed to load environment variables", "error", err)
		os.Exit(1)
	}*/

	limiter := newLimiter(logger)

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
		limiter,
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
