package server

import (
	"context"
	"log/slog"
)

type ctxKey int
const loggerCtxKey ctxKey = 0

// WithLogger adds a logger to the context
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerCtxKey, logger)
}

// LoggerFromContext retrieves the logger from the context
func LoggerFromContext(ctx context.Context) *slog.Logger {
	logger, ok := ctx.Value(loggerCtxKey).(*slog.Logger)
	if !ok {
		return slog.Default()
	}
	return logger
}