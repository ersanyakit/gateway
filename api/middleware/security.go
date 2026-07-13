package middleware

import (
	"net"
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

// RequireSameOrigin protects browser state-changing endpoints from cross-site
// form submissions. Origin is authoritative when present; Referer is accepted
// as a fallback for user agents that omit Origin. Requests that provide neither
// signal are rejected because checkout tokens can otherwise be abused as bearer
// URLs by another site.
func RequireSameOrigin() fiber.Handler {
	return func(c fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}

		source := strings.TrimSpace(c.Get("Origin"))
		if source == "" {
			source = strings.TrimSpace(c.Get("Referer"))
		}
		sourceOrigin, sourceOK := normalizedHTTPOrigin(source)
		requestOrigin, requestOK := checkoutRequestOrigin(c)
		if !sourceOK || !requestOK || sourceOrigin != requestOrigin {
			c.Set("Cache-Control", "no-store")
			return c.Status(fiber.StatusForbidden).SendString("same-origin request required")
		}
		return c.Next()
	}
}

func checkoutRequestOrigin(c fiber.Ctx) (string, bool) {
	scheme := strings.ToLower(strings.TrimSpace(c.Scheme()))
	if forwarded := firstForwardedHeaderValue(c.Get("X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	host := strings.TrimSpace(c.Host())
	if forwarded := firstForwardedHeaderValue(c.Get("X-Forwarded-Host")); forwarded != "" {
		host = forwarded
	}
	return normalizedHTTPOrigin(scheme + "://" + host)
}

func firstForwardedHeaderValue(value string) string {
	if idx := strings.IndexByte(value, ','); idx >= 0 {
		value = value[:idx]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizedHTTPOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.User != nil || parsed.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	hostname := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if hostname == "" || strings.ContainsAny(hostname, "\r\n\t /\\") {
		return "", false
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return scheme + "://" + host, true
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
