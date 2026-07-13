package middleware

import (
	"crypto/subtle"
	"os"
	"strings"

	"github.com/gofiber/fiber/v3"
)

const (
	legacyCommandTokenHeader        = "X-Internal-Command-Token"
	legacyCommandAltTokenHeader     = "X-Gateway-Internal-Token"
	legacyCommandDisabledStatusCode = fiber.StatusNotFound
)

func RequireLegacyCommandAccess() fiber.Handler {
	return func(c fiber.Ctx) error {
		expected := legacyCommandToken()
		if expected == "" {
			return c.SendStatus(legacyCommandDisabledStatusCode)
		}
		if !legacyCommandTokenMatches(c, expected) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false,
				"error":   "legacy command access denied",
			})
		}
		return c.Next()
	}
}

func legacyCommandToken() string {
	for _, key := range []string{"LEGACY_COMMAND_TOKEN", "INTERNAL_COMMAND_TOKEN", "GATEWAY_INTERNAL_COMMAND_TOKEN"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return ""
}

func legacyCommandTokenMatches(c fiber.Ctx, expected string) bool {
	actual := strings.TrimSpace(c.Get(legacyCommandTokenHeader))
	if actual == "" {
		actual = strings.TrimSpace(c.Get(legacyCommandAltTokenHeader))
	}
	if actual == "" {
		auth := strings.TrimSpace(c.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			actual = strings.TrimSpace(auth[7:])
		}
	}
	if actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
