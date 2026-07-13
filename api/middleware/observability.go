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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	RequestIDHeader   = "X-Request-ID"
	TraceparentHeader = "traceparent"
	requestIDLocalKey = "gateway.request_id"
	traceIDLocalKey   = "gateway.trace_id"
	spanIDLocalKey    = "gateway.span_id"
)

type requestIDContextKey struct{}

func NewOperationalLogger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: operationalLogLevel()}
	if operationalLogFormat() == "text" {
		return slog.New(RedactingHandler(slog.NewTextHandler(os.Stdout, opts)))
	}
	return slog.New(RedactingHandler(slog.NewJSONHandler(os.Stdout, opts)))
}

func operationalLogFormat() string {
	format := strings.ToLower(strings.TrimSpace(os.Getenv("GATEWAY_LOG_FORMAT")))
	if format != "" {
		return format
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
		return "json"
	}
	return "text"
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

func TraceContext(serviceName string) fiber.Handler {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		serviceName = "gateway"
	}
	propagator := propagation.TraceContext{}
	tracer := otel.Tracer(serviceName + "/http")
	return func(c fiber.Ctx) error {
		extracted := propagator.Extract(c.Context(), fiberTraceCarrier{c: c})
		parentSpanContext := trace.SpanContextFromContext(extracted)
		spanName := c.Method() + " " + c.Path()
		ctx, span := tracer.Start(extracted, spanName, trace.WithSpanKind(trace.SpanKindServer))
		activeSpanContext := trace.SpanContextFromContext(ctx)
		if !activeSpanContext.IsValid() && parentSpanContext.IsValid() {
			activeSpanContext = parentSpanContext
			ctx = trace.ContextWithSpanContext(ctx, activeSpanContext)
		}
		if activeSpanContext.IsValid() {
			c.Locals(traceIDLocalKey, activeSpanContext.TraceID().String())
			c.Locals(spanIDLocalKey, activeSpanContext.SpanID().String())
		}
		c.SetContext(ctx)

		err := c.Next()
		status := responseStatus(c, err)
		route := c.FullPath()
		if route != "" {
			span.SetName(c.Method() + " " + route)
		}
		span.SetAttributes(
			attribute.String("http.request.method", c.Method()),
			attribute.String("url.path", c.Path()),
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", status),
			attribute.String("gateway.request_id", RequestIDFromCtx(c)),
		)
		if err != nil {
			span.RecordError(err)
		}
		if status >= fiber.StatusInternalServerError || err != nil {
			span.SetStatus(codes.Error, errorType(err))
		}
		propagator.Inject(trace.ContextWithSpanContext(ctx, activeSpanContext), fiberTraceCarrier{c: c})
		span.End()
		return err
	}
}

func TraceIDFromCtx(c fiber.Ctx) string {
	if c == nil {
		return ""
	}
	if value, ok := c.Locals(traceIDLocalKey).(string); ok {
		return value
	}
	return ""
}

func SpanIDFromCtx(c fiber.Ctx) string {
	if c == nil {
		return ""
	}
	if value, ok := c.Locals(spanIDLocalKey).(string); ok {
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
			slog.String("trace_id", TraceIDFromCtx(c)),
			slog.String("span_id", SpanIDFromCtx(c)),
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

type fiberTraceCarrier struct {
	c fiber.Ctx
}

func (fc fiberTraceCarrier) Get(key string) string {
	if fc.c == nil {
		return ""
	}
	return fc.c.Get(key)
}

func (fc fiberTraceCarrier) Set(key string, value string) {
	if fc.c == nil {
		return
	}
	fc.c.Set(key, value)
}

func (fc fiberTraceCarrier) Keys() []string {
	return []string{TraceparentHeader, "tracestate"}
}

func RedactingHandler(next slog.Handler) slog.Handler {
	if next == nil {
		return nil
	}
	return redactingHandler{next: next}
}

type redactingHandler struct {
	next slog.Handler
}

func (h redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clean.AddAttrs(redactLogAttr(attr))
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		clean = append(clean, redactLogAttr(attr))
	}
	return redactingHandler{next: h.next.WithAttrs(clean)}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{next: h.next.WithGroup(name)}
}

func redactLogAttr(attr slog.Attr) slog.Attr {
	if sensitiveLogKey(attr.Key) {
		return slog.String(attr.Key, "[redacted]")
	}
	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		clean := make([]slog.Attr, 0, len(group))
		for _, child := range group {
			clean = append(clean, redactLogAttr(child))
		}
		return slog.Group(attr.Key, attrsToAny(clean)...)
	}
	if attr.Value.Kind() == slog.KindString {
		value := attr.Value.String()
		if containsSensitiveLogValue(value) {
			return slog.String(attr.Key, "[redacted]")
		}
	}
	return attr
}

func attrsToAny(attrs []slog.Attr) []any {
	out := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, attr)
	}
	return out
}

func sensitiveLogKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"secret", "token", "password", "authorization", "signature", "mnemonic", "private_key", "raw_payload", "body"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func containsSensitiveLogValue(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"api_secret", "webhook_secret", "x-api-secret", "authorization", "raw_signature", "mnemonic", "private_key", "signed_tx", "raw_tx"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
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
