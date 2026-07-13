package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestRequireSameOrigin(t *testing.T) {
	app := fiber.New()
	app.Post("/checkout/token/cancel", RequireSameOrigin(), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	tests := []struct {
		name      string
		origin    string
		referer   string
		forwarded string
		want      int
	}{
		{name: "same origin", origin: "http://checkout.example", want: fiber.StatusNoContent},
		{name: "same origin default port", origin: "http://checkout.example:80", want: fiber.StatusNoContent},
		{name: "same origin referer fallback", referer: "http://checkout.example/checkout/token/pay?lang=en", want: fiber.StatusNoContent},
		{name: "forwarded https origin", origin: "https://checkout.example", forwarded: "https", want: fiber.StatusNoContent},
		{name: "cross origin", origin: "https://attacker.example", want: fiber.StatusForbidden},
		{name: "origin wins over same origin referer", origin: "https://attacker.example", referer: "http://checkout.example/checkout/token/pay", want: fiber.StatusForbidden},
		{name: "cross origin referer", referer: "https://attacker.example/form", want: fiber.StatusForbidden},
		{name: "opaque origin", origin: "null", want: fiber.StatusForbidden},
		{name: "missing provenance", want: fiber.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://checkout.example/checkout/token/cancel", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.referer != "" {
				req.Header.Set("Referer", tt.referer)
			}
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forwarded)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}
