package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const (
	RequestIDHeader   = "X-Request-ID"
	requestIDLocalKey = "gateway.request_id"
)

type requestIDContextKey struct{}

func NewOperationalLogger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: operationalLogLevel()}
	format := strings.ToLower(strings.TrimSpace(os.Getenv("GATEWAY_LOG_FORMAT")))
	if format == "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
			format = "json"
		} else {
			format = "text"
		}
	}
	if format == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func operationalLogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GATEWAY_LOG_LEVEL"))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func RequestID() fiber.Handler {
	return func(c fiber.Ctx) error {
		requestID := sanitizeRequestID(c.Get(RequestIDHeader))
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Locals(requestIDLocalKey, requestID)
		c.Set(RequestIDHeader, requestID)
		c.SetContext(context.WithValue(c.Context(), requestIDContextKey{}, requestID))
		return c.Next()
	}
}

func RequestIDFromCtx(c fiber.Ctx) string {
	if c == nil {
		return ""
	}
	if value, ok := c.Locals(requestIDLocalKey).(string); ok {
		return value
	}
	if requestID := sanitizeRequestID(c.GetRespHeader(RequestIDHeader)); requestID != "" {
		return requestID
	}
	return sanitizeRequestID(c.Get(RequestIDHeader))
}

func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if value, ok := ctx.Value(requestIDContextKey{}).(string); ok {
		return value
	}
	return ""
}

func RecoverPanic(logger *slog.Logger) fiber.Handler {
	logger = loggerOrDefault(logger)
	return func(c fiber.Ctx) (err error) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			requestID := RequestIDFromCtx(c)
			if requestID == "" {
				requestID = uuid.NewString()
				c.Locals(requestIDLocalKey, requestID)
				c.Set(RequestIDHeader, requestID)
			}

			logger.ErrorContext(c.Context(), "http_panic",
				"request_id", requestID,
				"method", c.Method(),
				"path", c.Path(),
				"panic_type", fmt.Sprintf("%T", recovered),
			)
			err = c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"result":     "error",
				"message":    "internal server error",
				"request_id": requestID,
			})
		}()
		return c.Next()
	}
}

func RequestLogger(logger *slog.Logger) fiber.Handler {
	logger = loggerOrDefault(logger)
	return func(c fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		status := responseStatus(c, err)
		level := requestLogLevel(status, err)

		logger.LogAttrs(c.Context(), level, "http_request",
			slog.String("request_id", RequestIDFromCtx(c)),
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.String("route", c.FullPath()),
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("error_type", errorType(err)),
		)
		return err
	}
}

func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}

func responseStatus(c fiber.Ctx, err error) int {
	status := c.Response().StatusCode()
	if status == 0 {
		status = fiber.StatusOK
	}
	if err == nil {
		return status
	}

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) && fiberErr.Code > 0 {
		return fiberErr.Code
	}
	if status < fiber.StatusBadRequest {
		return fiber.StatusInternalServerError
	}
	return status
}

func requestLogLevel(status int, err error) slog.Level {
	switch {
	case err != nil || status >= fiber.StatusInternalServerError:
		return slog.LevelError
	case status >= fiber.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func errorType(err error) string {
	if err == nil {
		return ""
	}
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return "fiber_error"
	}
	return fmt.Sprintf("%T", err)
}

func sanitizeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == ':':
		default:
			return ""
		}
	}
	return value
}
