package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

type fakeAPIKeyRateLimitStore struct {
	allowed bool
	called  bool
	keyHash string
}

func (f *fakeAPIKeyRateLimitStore) Allow(_ context.Context, keyHash string, _ int, _ time.Duration) (bool, error) {
	f.called = true
	f.keyHash = keyHash
	return f.allowed, nil
}

func TestHashRateLimitSecretDoesNotExposeRawSecret(t *testing.T) {
	raw := "Bearer gw_secret_test"
	hashed := hashRateLimitSecret(raw)

	if hashed == raw {
		t.Fatal("rate-limit key should not contain the raw secret")
	}
	if len(hashed) != 64 {
		t.Fatalf("hash length = %d, want 64", len(hashed))
	}
	if hashed != hashRateLimitSecret(raw) {
		t.Fatal("hash should be deterministic")
	}
}

func TestRateLimitAPIKeyUsesSharedStoreWhenProvided(t *testing.T) {
	store := &fakeAPIKeyRateLimitStore{allowed: true}
	app := fiber.New()
	app.Use(RateLimitAPIKeyWithStore(store))
	app.Get("/probe", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-API-Key", "raw-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}
	if !store.called {
		t.Fatal("shared store was not called")
	}
	if store.keyHash == "api:"+hashRateLimitSecret("raw-key") || store.keyHash == "raw-key" {
		t.Fatalf("store key hash leaked raw or once-hashed key: %q", store.keyHash)
	}
}

func TestRateLimitAPIKeyRequiresStoreInDistributedMode(t *testing.T) {
	t.Setenv("APP_SCALE_MODE", "distributed")
	app := fiber.New()
	app.Use(RateLimitAPIKeyWithStore(nil))
	app.Get("/probe", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-API-Key", "raw-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusServiceUnavailable)
	}
}

func TestRateLimitAdminAuthThrottlesRepeatedAttempts(t *testing.T) {
	app := fiber.New()
	app.Use(rateLimitAdminAuthWithLimiter(newRateLimiter(2, time.Minute)))
	app.Post("/admin/login", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader("email=admin%40example.com"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test attempt %d: %v", i+1, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("attempt %d status = %d, want %d", i+1, resp.StatusCode, fiber.StatusNoContent)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader("email=admin%40example.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("throttled status = %d, want %d", resp.StatusCode, fiber.StatusTooManyRequests)
	}
}
