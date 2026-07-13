package routes

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestPortalMutationJWTMountedBeforePortalPostRoutes(t *testing.T) {
	raw, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	jwtIndex := strings.Index(source, "portalJWT := middleware.PortalMutationJWT()")
	if jwtIndex < 0 {
		t.Fatal("PortalMutationJWT construction missing")
	}
	for _, token := range []string{
		`r.fiber.Use("/dealer", portalJWT)`,
		`r.fiber.Use("/merchant", portalJWT)`,
		`r.fiber.Use("/admin", portalJWT)`,
	} {
		idx := strings.Index(source, token)
		if idx < 0 {
			t.Fatalf("portal JWT mount missing %q", token)
		}
		if idx < jwtIndex {
			t.Fatalf("portal JWT mount %q must follow PortalMutationJWT construction", token)
		}
	}
	for _, token := range []string{
		`r.fiber.Post(prefix+"/withdrawals", handlers.HandleDealerWithdrawalCreate(dealerDeps))`,
		`r.fiber.Post(prefix+"/domains/:id/rotate-api-secret", handlers.HandleDealerDomainRotateAPISecret(dealerDeps))`,
		`r.fiber.Post("/admin/admins", handlers.HandleAdminCreateAdmin(dealerDeps))`,
		`r.fiber.Post("/admin/admins/:id/role", handlers.HandleAdminUpdateAdminRole(dealerDeps))`,
		`r.fiber.Post("/admin/withdrawals/:id/approve", handlers.HandleAdminWithdrawalApprove(dealerDeps))`,
		`r.fiber.Post("/admin/refunds/:id/approve", handlers.HandleAdminRefundApprove(dealerDeps))`,
		`r.fiber.Post("/admin/recover", handlers.HandleAdminRecoverFunds(dealerDeps))`,
		`r.fiber.Post("/admin/sweep", handlers.HandleAdminSweepEnqueue(dealerDeps))`,
	} {
		idx := strings.Index(source, token)
		if idx < 0 {
			t.Fatalf("portal mutation route missing %q", token)
		}
		if idx < jwtIndex {
			t.Fatalf("portal mutation route %q is registered before PortalMutationJWT", token)
		}
	}
	if strings.Contains(source, `r.fiber.Post("/admin/sweep", handlers.HandleAdminRecoverFunds(dealerDeps))`) {
		t.Fatal("admin sweep route must enqueue sweep jobs, not run manual recover funds")
	}
	for _, token := range []string{
		`r.fiber.Post("/admin/login", middleware.RateLimitAdminAuth(), handlers.HandleAdminLoginSubmit(r.AdminRepo))`,
		`r.fiber.Post("/admin/2fa/setup", middleware.RateLimitAdminAuth(), handlers.HandleAdminTOTPSetupSubmit(r.AdminRepo))`,
		`r.fiber.Post("/admin/2fa/verify", middleware.RateLimitAdminAuth(), handlers.HandleAdminTOTPVerifySubmit(r.AdminRepo))`,
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("admin auth route missing rate limit token %q", token)
		}
	}
	for _, token := range []string{
		`r.fiber.Get("/admin/recover/live-balance", handlers.HandleAdminSweepLiveBalance(dealerDeps))`,
		`r.fiber.Get("/admin/recover/:chain_id", handlers.HandleAdminDashboard(dealerDeps))`,
		`r.fiber.Get("/admin/recover/:chain_id/:asset", handlers.HandleAdminDashboard(dealerDeps))`,
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("admin recover route missing %q", token)
		}
	}
	liveIndex := strings.Index(source, `r.fiber.Get("/admin/recover/live-balance", handlers.HandleAdminSweepLiveBalance(dealerDeps))`)
	pathIndex := strings.Index(source, `r.fiber.Get("/admin/recover/:chain_id", handlers.HandleAdminDashboard(dealerDeps))`)
	genericIndex := strings.Index(source, `r.fiber.Get("/admin/:section", handlers.HandleAdminDashboard(dealerDeps))`)
	if !(liveIndex >= 0 && pathIndex > liveIndex && genericIndex > pathIndex) {
		t.Fatal("admin recover path routes must be registered after live-balance and before generic admin section route")
	}
}

func TestLegacyCommandRoutesRequireInternalAccessMiddleware(t *testing.T) {
	raw, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	if !strings.Contains(source, "legacyCommandAccess := middleware.RequireLegacyCommandAccess()") {
		t.Fatal("legacy command access middleware construction missing")
	}
	for _, route := range []string{
		"CMD_MERCHANT_CREATE",
		"CMD_MERCHANT_FETCH",
		"CMD_MERCHANT_DOMAIN_CREATE",
		"CMD_DOMAIN_DEPOSIT_SUMMARY",
		"CMD_MERCHANT_FETCH_BY_ID",
		"CMD_MERCHANT_FETCH_BY_EMAIL",
		"CMD_MERCHANT_DELETE_BY_ID",
		"CMD_MERCHANT_DELETE_BY_EMAIL",
		"CMD_MERCHANT_WALLET_CREATE",
		"CMD_WITHDRAW",
		"CMD_SWEEP",
	} {
		guarded := `r.fiber.Post(constants.` + route + `.String(), legacyCommandAccess,`
		if !strings.Contains(source, guarded) {
			t.Fatalf("legacy command route %s is not registered behind legacyCommandAccess", route)
		}
		unguarded := `r.fiber.Post(constants.` + route + `.String(), handlers.`
		if strings.Contains(source, unguarded) {
			t.Fatalf("legacy command route %s still has an unguarded registration", route)
		}
	}
}

func TestCheckoutMutationRoutesArePostOnlyAndSameOrigin(t *testing.T) {
	raw, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, want := range []string{
		`checkoutSameOrigin := middleware.RequireSameOrigin()`,
		`r.fiber.Post("/checkout/:token/select", checkoutSameOrigin, middleware.RateLimitCheckout(), handlers.HandleCheckoutSelectAsset(paymentDeps))`,
		`r.fiber.Get("/checkout/:token/change", checkoutPostOnly)`,
		`r.fiber.Post("/checkout/:token/change", checkoutSameOrigin, middleware.RateLimitCheckout(), handlers.HandleCheckoutChangeAsset(paymentDeps))`,
		`r.fiber.Get("/checkout/:token/cancel", checkoutPostOnly)`,
		`r.fiber.Post("/checkout/:token/cancel", checkoutSameOrigin, middleware.RateLimitCheckout(), handlers.HandleCheckoutCancel(paymentDeps))`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("checkout mutation route missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`r.fiber.Get("/checkout/:token/change", middleware.RateLimitCheckout(), handlers.HandleCheckoutChangeAsset(paymentDeps))`,
		`r.fiber.Get("/checkout/:token/cancel", handlers.HandleCheckoutCancel(paymentDeps))`,
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("state-changing checkout GET route remains registered through %q", forbidden)
		}
	}
}

func TestCheckoutPostOnlyReturnsMethodNotAllowed(t *testing.T) {
	app := fiber.New()
	app.Get("/checkout/:token/change", checkoutPostOnly)
	app.Get("/checkout/:token/cancel", checkoutPostOnly)
	for _, path := range []string{"/checkout/token/change", "/checkout/token/cancel"} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != fiber.StatusMethodNotAllowed {
			t.Fatalf("GET %s status = %d, want 405", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Allow"); got != fiber.MethodPost {
			t.Fatalf("GET %s Allow = %q, want POST", path, got)
		}
	}
}
