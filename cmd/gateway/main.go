package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/victornguyen247/LLM-GateWay/internal/cache"
	"github.com/victornguyen247/LLM-GateWay/internal/proxy"
	"github.com/victornguyen247/LLM-GateWay/internal/ratelimit"
	"github.com/victornguyen247/LLM-GateWay/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	rps := envFloat("RATE_LIMIT_RPS", 2.0)
	burst := envInt("RATE_LIMIT_BURST", 2)
	rateLimitBackend := envString("RATE_LIMIT_BACKEND", "memory")

	cacheBackend := envString("CACHE_BACKEND", "memory")
	cacheSize := envInt("CACHE_SIZE", 1000)
	cacheTTL := envDuration("CACHE_TTL", 1*time.Hour)

	// One Redis client, shared by any backend that wants it. Fail loudly at boot
	// so misconfiguration doesn't surface on the first request.
	var redisClient *redis.Client
	if rateLimitBackend == "redis" || cacheBackend == "redis" {
		redisURL := envString("REDIS_URL", "redis://localhost:6379")
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			logger.Error("invalid REDIS_URL", "error", err)
			os.Exit(1)
		}
		redisClient = redis.NewClient(opts)
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			logger.Error("redis ping failed", "error", err)
			os.Exit(1)
		}
	}

	var c cache.Cache
	switch cacheBackend {
	case "memory", "":
		mc, err := cache.NewMemoryCache(cacheSize, cacheTTL)
		if err != nil {
			logger.Error("failed to create memory cache", "error", err)
			os.Exit(1)
		}
		c = mc
		logger.Info("cache backend: memory", "size", cacheSize, "ttl", cacheTTL)
	case "redis":
		c = cache.NewRedisCache(redisClient, cacheTTL)
		logger.Info("cache backend: redis", "ttl", cacheTTL)
	default:
		logger.Error("unknown CACHE_BACKEND", "value", cacheBackend)
		os.Exit(1)
	}

	var lim ratelimit.Limiter
	switch rateLimitBackend {
	case "memory", "":
		lim = ratelimit.NewManager(rps, burst)
		logger.Info("rate limiter backend: memory", "rps", rps, "burst", burst)
	case "redis":
		window := envDuration("RATE_LIMIT_WINDOW", time.Second)
		limit := envInt("RATE_LIMIT_LIMIT", int(rps))
		lim = ratelimit.NewRedisLimiter(redisClient, window, limit)
		logger.Info("rate limiter backend: redis", "window", window, "limit", limit)
	default:
		logger.Error("unknown RATE_LIMIT_BACKEND", "value", rateLimitBackend)
		os.Exit(1)
	}

	upstreamURL := envString("OPENAI_UPSTREAM_URL", "https://api.openai.com")
	listen := envString("GATEWAY_LISTEN", ":8080")

	s := server.NewServer(
		listen,
		logger,
		proxy.NewOpenAIProxy(upstreamURL, os.Getenv("OPENAI_API_KEY"), logger),
		lim,
		c,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := s.Run(); err != nil {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}
	logger.Info("gracefully shut down")
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
