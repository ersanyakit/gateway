package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestPortalMutationJWTIssuesTokenAndRequiresExplicitSubmitToken(t *testing.T) {
	t.Setenv("PORTAL_JWT_SECRET", "test-portal-jwt-secret")

	app := fiber.New()
	app.Use(PortalMutationJWT())
	app.Get("/admin/probe", func(c fiber.Ctx) error {
		return c.SendString("safe")
	})
	app.Post("/admin/probe", func(c fiber.Ctx) error {
		return c.SendString("mutated")
	})

	getReq := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	getReq.AddCookie(&http.Cookie{Name: "admin_session", Value: "session-a"})
	getResp, err := app.Test(getReq)
	if err != nil {
		t.Fatalf("GET app.Test: %v", err)
	}
	if getResp.StatusCode != fiber.StatusOK {
		t.Fatalf("GET status = %d, want %d", getResp.StatusCode, fiber.StatusOK)
	}
	cookies := responseCookies(getResp)
	token := cookies[portalJWTTokenCookie]
	if token == "" {
		t.Fatal("expected portal jwt cookie")
	}

	missingReq := httptest.NewRequest(http.MethodPost, "/admin/probe", nil)
	missingReq.AddCookie(&http.Cookie{Name: "admin_session", Value: "session-a"})
	missingReq.AddCookie(&http.Cookie{Name: portalJWTTokenCookie, Value: token})
	missingResp, err := app.Test(missingReq)
	if err != nil {
		t.Fatalf("POST missing app.Test: %v", err)
	}
	if missingResp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("POST without submitted token status = %d, want %d", missingResp.StatusCode, fiber.StatusForbidden)
	}

	form := url.Values{portalJWTTokenForm: {token}}
	validReq := httptest.NewRequest(http.MethodPost, "/admin/probe", strings.NewReader(form.Encode()))
	validReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	validReq.AddCookie(&http.Cookie{Name: "admin_session", Value: "session-a"})
	validResp, err := app.Test(validReq)
	if err != nil {
		t.Fatalf("POST valid app.Test: %v", err)
	}
	if validResp.StatusCode != fiber.StatusOK {
		t.Fatalf("POST valid status = %d, want %d", validResp.StatusCode, fiber.StatusOK)
	}
}

func TestPortalMutationJWTRejectsSessionBoundTokenMismatch(t *testing.T) {
	t.Setenv("PORTAL_JWT_SECRET", "test-portal-jwt-secret")

	app := fiber.New()
	app.Use(PortalMutationJWT())
	app.Get("/merchant/probe", func(c fiber.Ctx) error {
		return c.SendString("safe")
	})
	app.Post("/merchant/probe", func(c fiber.Ctx) error {
		return c.SendString("mutated")
	})

	getReq := httptest.NewRequest(http.MethodGet, "/merchant/probe", nil)
	getReq.AddCookie(&http.Cookie{Name: "dealer_session", Value: "dealer-session-a"})
	getResp, err := app.Test(getReq)
	if err != nil {
		t.Fatalf("GET app.Test: %v", err)
	}
	cookies := responseCookies(getResp)
	token := cookies[portalJWTTokenCookie]
	if token == "" {
		t.Fatal("expected portal jwt cookie")
	}

	mismatchReq := httptest.NewRequest(http.MethodPost, "/merchant/probe", nil)
	mismatchReq.Header.Set(portalJWTTokenHeader, token)
	mismatchReq.AddCookie(&http.Cookie{Name: "dealer_session", Value: "dealer-session-b"})
	mismatchResp, err := app.Test(mismatchReq)
	if err != nil {
		t.Fatalf("POST mismatch app.Test: %v", err)
	}
	if mismatchResp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("POST mismatch status = %d, want %d", mismatchResp.StatusCode, fiber.StatusForbidden)
	}
}

func responseCookies(resp *http.Response) map[string]string {
	cookies := make(map[string]string)
	for _, cookie := range resp.Cookies() {
		cookies[cookie.Name] = cookie.Value
	}
	return cookies
}
