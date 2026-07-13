package middleware

import (
	"context"
	"core/helpers"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

type bucket struct {
	count   int
	resetAt time.Time
}

type APIKeyRateLimitStore interface {
	Allow(ctx context.Context, keyHash string, limit int, window time.Duration) (bool, error)
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	limit   int
	window  time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		buckets: make(map[string]*bucket),
		limit:   limit,
		window:  window,
	}
	helpers.GoSafely("rate-limiter.cleanup", rl.cleanupLoop)
	return rl
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok || now.After(b.resetAt) {
		rl.buckets[key] = &bucket{count: 1, resetAt: now.Add(rl.window)}
		return true
	}
	if b.count >= rl.limit {
		return false
	}
	b.count++
	return true
}

func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for k, b := range rl.buckets {
			if now.After(b.resetAt) {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

var (
	paymentCreateLimiter = newRateLimiter(20, time.Minute)
	checkoutLimiter      = newRateLimiter(60, time.Minute)
	apiKeyLimiter        = newRateLimiter(envInt("API_KEY_RATE_LIMIT_PER_MINUTE", 120), time.Minute)
	adminAuthLimiter     = newRateLimiter(envInt("ADMIN_AUTH_RATE_LIMIT_PER_MINUTE", 8), time.Minute)
)

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func rateKey(c fiber.Ctx) string {
	if key := c.Get("X-API-Key"); key != "" {
		return "api:" + hashRateLimitSecret(key)
	}
	if auth := c.Get("Authorization"); auth != "" {
		return "auth:" + hashRateLimitSecret(auth)
	}
	return "ip:" + c.IP()
}

func hashRateLimitSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func distributedRateLimitRequired() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_SCALE_MODE"))) {
	case "production", "prod", "distributed":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PRODUCTION_SCALE_MODE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func RateLimitPaymentCreate() fiber.Handler {
	return func(c fiber.Ctx) error {
		key := rateKey(c)
		if !paymentCreateLimiter.allow(key) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "rate limit exceeded",
			})
		}
		return c.Next()
	}
}

func RateLimitAPIKey() fiber.Handler {
	return RateLimitAPIKeyWithStore(nil)
}

func RateLimitAPIKeyWithStore(store APIKeyRateLimitStore) fiber.Handler {
	return func(c fiber.Ctx) error {
		key := rateKey(c)
		if store != nil {
			allowed, err := store.Allow(c.Context(), hashRateLimitSecret(key), apiKeyLimiter.limit, apiKeyLimiter.window)
			if err != nil {
				return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
					"success": false,
					"error":   "rate limit store unavailable",
				})
			}
			if !allowed {
				return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
					"success": false,
					"error":   "rate limit exceeded",
				})
			}
			return c.Next()
		}
		if distributedRateLimitRequired() {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"success": false,
				"error":   "distributed rate limit store required",
			})
		}
		if !apiKeyLimiter.allow(key) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"success": false,
				"error":   "rate limit exceeded",
			})
		}
		return c.Next()
	}
}

func RateLimitCheckout() fiber.Handler {
	return func(c fiber.Ctx) error {
		key := c.IP()
		if !checkoutLimiter.allow(key) {
			return c.Status(fiber.StatusTooManyRequests).SendString("Too many requests")
		}
		return c.Next()
	}
}

func RateLimitAdminAuth() fiber.Handler {
	return rateLimitAdminAuthWithLimiter(adminAuthLimiter)
}

func rateLimitAdminAuthWithLimiter(limiter *rateLimiter) fiber.Handler {
	return func(c fiber.Ctx) error {
		if limiter == nil {
			return c.Next()
		}
		if !limiter.allow(adminAuthRateKey(c)) {
			return c.Status(fiber.StatusTooManyRequests).SendString("Too many authentication attempts")
		}
		return c.Next()
	}
}

func adminAuthRateKey(c fiber.Ctx) string {
	email := strings.ToLower(strings.TrimSpace(c.FormValue("email")))
	tempSession := strings.TrimSpace(c.Cookies("admin_totp_pending"))
	if tempSession == "" {
		tempSession = strings.TrimSpace(c.Cookies("admin_totp_setup"))
	}
	return "admin-auth:" + c.IP() + ":" + hashRateLimitSecret(email+"|"+tempSession)
}
