package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.opentelemetry.io/otel/trace"
)

func TestRequestIDUsesSafeClientHeader(t *testing.T) {
	app := fiber.New()
	app.Use(RequestID())
	app.Get("/probe", func(c fiber.Ctx) error {
		if got := RequestIDFromCtx(c); got != "req-12345678" {
			t.Fatalf("RequestIDFromCtx = %q, want client id", got)
		}
		if got := RequestIDFromContext(c.Context()); got != "req-12345678" {
			t.Fatalf("RequestIDFromContext = %q, want client id", got)
		}
		return c.SendString("ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set(RequestIDHeader, "req-12345678")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get(RequestIDHeader); got != "req-12345678" {
		t.Fatalf("response request id = %q, want client id", got)
	}
}

func TestRequestIDGeneratesForUnsafeClientHeader(t *testing.T) {
	app := fiber.New()
	app.Use(RequestID())
	app.Get("/probe", func(c fiber.Ctx) error {
		return c.SendString(RequestIDFromCtx(c))
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set(RequestIDHeader, "short")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	got := strings.TrimSpace(string(body))
	if got == "" || got == "short" {
		t.Fatalf("generated request id = %q, want non-empty generated id", got)
	}
	if resp.Header.Get(RequestIDHeader) != got {
		t.Fatalf("response request id = %q, want %q", resp.Header.Get(RequestIDHeader), got)
	}
}

func TestRecoverPanicReturnsSanitizedResponseAndLog(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	app := fiber.New()
	app.Use(RequestID())
	app.Use(RequestLogger(logger))
	app.Use(RecoverPanic(logger))
	app.Get("/panic", func(c fiber.Ctx) error {
		panic("mnemonic secret should not leak")
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/panic", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bodyText := string(body)

	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if !strings.Contains(bodyText, "internal server error") || !strings.Contains(bodyText, "request_id") {
		t.Fatalf("body = %q, want sanitized error with request_id", bodyText)
	}
	if strings.Contains(bodyText, "mnemonic secret") {
		t.Fatalf("panic value leaked in response: %q", bodyText)
	}
	logText := logs.String()
	if !strings.Contains(logText, "http_panic") || !strings.Contains(logText, "http_request") {
		t.Fatalf("logs = %q, want panic and request entries", logText)
	}
	if strings.Contains(logText, "mnemonic secret") {
		t.Fatalf("panic value leaked in logs: %q", logText)
	}
}

func TestRequestLoggerDoesNotLogQueryOrHeaders(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	app := fiber.New()
	app.Use(RequestID())
	app.Use(RequestLogger(logger))
	app.Get("/probe", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/probe?api_secret=raw-secret", nil)
	req.Header.Set("Authorization", "Bearer raw-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	_ = resp.Body.Close()

	logText := logs.String()
	for _, forbidden := range []string{"raw-secret", "raw-token", "api_secret"} {
		if strings.Contains(logText, forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, logText)
		}
	}
	for _, expected := range []string{`"msg":"http_request"`, `"path":"/probe"`, `"status":204`} {
		if !strings.Contains(logText, expected) {
			t.Fatalf("log = %q, missing %s", logText, expected)
		}
	}
}

func TestTraceContextPropagatesTraceparentAndLogsTraceID(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	app := fiber.New()
	app.Use(RequestID())
	app.Use(TraceContext("gateway-test"))
	app.Use(RequestLogger(logger))
	app.Get("/probe", func(c fiber.Ctx) error {
		spanContext := trace.SpanContextFromContext(c.Context())
		if !spanContext.IsValid() {
			t.Fatal("span context should be valid from traceparent")
		}
		if got := TraceIDFromCtx(c); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("TraceIDFromCtx = %q, want upstream trace id", got)
		}
		return c.SendString(spanContext.TraceID().String())
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set(TraceparentHeader, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("body trace id = %q", body)
	}
	if got := resp.Header.Get(TraceparentHeader); !strings.Contains(got, "4bf92f3577b34da6a3ce929d0e0e4736") {
		t.Fatalf("response traceparent = %q, want propagated trace id", got)
	}
	if logText := logs.String(); !strings.Contains(logText, `"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`) {
		t.Fatalf("request log missing trace id: %s", logText)
	}
}

func TestRedactingHandlerScrubsSensitiveAttributes(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(RedactingHandler(slog.NewJSONHandler(&logs, nil)))

	logger.Info("security_event",
		"api_secret", "plain-secret",
		"authorization", "Bearer raw-token",
		"details", "webhook_secret=plain raw_signature=abc",
	)

	logText := logs.String()
	for _, forbidden := range []string{"plain-secret", "raw-token", "webhook_secret=plain", "raw_signature=abc"} {
		if strings.Contains(logText, forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, logText)
		}
	}
	if count := strings.Count(logText, "[redacted]"); count < 3 {
		t.Fatalf("log redaction count = %d, log=%s", count, logText)
	}
}

func TestOperationalLogFormatDefaultsToJSONInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("GATEWAY_LOG_FORMAT", "")
	if got := operationalLogFormat(); got != "json" {
		t.Fatalf("production log format = %q, want json", got)
	}

	t.Setenv("APP_ENV", "development")
	if got := operationalLogFormat(); got != "text" {
		t.Fatalf("development log format = %q, want text", got)
	}

	t.Setenv("GATEWAY_LOG_FORMAT", "json")
	if got := operationalLogFormat(); got != "json" {
		t.Fatalf("explicit log format = %q, want json", got)
	}
}
