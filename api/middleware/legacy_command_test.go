package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestRequireLegacyCommandAccessDisabledWhenNoTokenConfigured(t *testing.T) {
	t.Setenv("LEGACY_COMMAND_TOKEN", "")
	t.Setenv("INTERNAL_COMMAND_TOKEN", "")
	t.Setenv("GATEWAY_INTERNAL_COMMAND_TOKEN", "")

	called := false
	app := fiber.New()
	app.Post("/merchant.fetch", RequireLegacyCommandAccess(), func(c fiber.Ctx) error {
		called = true
		return c.SendStatus(fiber.StatusNoContent)
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/merchant.fetch", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != legacyCommandDisabledStatusCode {
		t.Fatalf("status = %d, want %d", resp.StatusCode, legacyCommandDisabledStatusCode)
	}
	if called {
		t.Fatal("legacy command handler ran while access was disabled")
	}
}

func TestRequireLegacyCommandAccessRequiresConfiguredToken(t *testing.T) {
	t.Setenv("LEGACY_COMMAND_TOKEN", "legacy-token")
	t.Setenv("INTERNAL_COMMAND_TOKEN", "")
	t.Setenv("GATEWAY_INTERNAL_COMMAND_TOKEN", "")

	for _, tc := range []struct {
		name       string
		configure  func(*http.Request)
		wantStatus int
		wantCalled bool
	}{
		{
			name: "missing",
			configure: func(req *http.Request) {
			},
			wantStatus: fiber.StatusUnauthorized,
		},
		{
			name: "wrong",
			configure: func(req *http.Request) {
				req.Header.Set(legacyCommandTokenHeader, "wrong-token")
			},
			wantStatus: fiber.StatusUnauthorized,
		},
		{
			name: "primary header",
			configure: func(req *http.Request) {
				req.Header.Set(legacyCommandTokenHeader, "legacy-token")
			},
			wantStatus: fiber.StatusNoContent,
			wantCalled: true,
		},
		{
			name: "alternate header",
			configure: func(req *http.Request) {
				req.Header.Set(legacyCommandAltTokenHeader, "legacy-token")
			},
			wantStatus: fiber.StatusNoContent,
			wantCalled: true,
		},
		{
			name: "bearer token",
			configure: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer legacy-token")
			},
			wantStatus: fiber.StatusNoContent,
			wantCalled: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			app := fiber.New()
			app.Post("/merchant.fetch", RequireLegacyCommandAccess(), func(c fiber.Ctx) error {
				called = true
				return c.SendStatus(fiber.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodPost, "/merchant.fetch", nil)
			tc.configure(req)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if called != tc.wantCalled {
				t.Fatalf("called = %v, want %v", called, tc.wantCalled)
			}
		})
	}
}
