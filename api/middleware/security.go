package middleware

import (
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

func SecurityHeaders() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("X-XSS-Protection", "0")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if strings.EqualFold(c.Protocol(), "https") {
			c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		return c.Next()
	}
}

func AllowedCORSOrigins() map[string]struct{} {
	allowed := make(map[string]struct{})
	for _, raw := range strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",") {
		origin := strings.TrimSpace(raw)
		if origin == "" {
			continue
		}
		origin = strings.TrimRight(origin, "/")
		if parsed, err := url.Parse(origin); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			allowed[strings.ToLower(origin)] = struct{}{}
		}
	}
	return allowed
}

func IsOriginAllowed(origin string, allowed map[string]struct{}) bool {
	origin = strings.TrimRight(strings.ToLower(strings.TrimSpace(origin)), "/")
	if origin == "" {
		return true
	}
	_, ok := allowed[origin]
	return ok
}

func HTTPReadTimeout() time.Duration {
	return envDuration("HTTP_READ_TIMEOUT", 15*time.Second)
}

func HTTPWriteTimeout() time.Duration {
	return envDuration("HTTP_WRITE_TIMEOUT", 30*time.Second)
}

func HTTPIdleTimeout() time.Duration {
	return envDuration("HTTP_IDLE_TIMEOUT", 60*time.Second)
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
