package middleware

import (
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

type bucket struct {
	count   int
	resetAt time.Time
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
	go rl.cleanupLoop()
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
		return "api:" + key
	}
	if auth := c.Get("Authorization"); auth != "" {
		return "auth:" + auth
	}
	return "ip:" + c.IP()
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
	return func(c fiber.Ctx) error {
		if !apiKeyLimiter.allow(rateKey(c)) {
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
