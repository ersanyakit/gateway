package routes

import (
	"os"
	"strings"
	"testing"
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
		`r.fiber.Post("/admin/withdrawals/:id/approve", handlers.HandleAdminWithdrawalApprove(dealerDeps))`,
		`r.fiber.Post("/admin/refunds/:id/approve", handlers.HandleAdminRefundApprove(dealerDeps))`,
		`r.fiber.Post("/admin/recover", handlers.HandleAdminRecoverFunds(dealerDeps))`,
	} {
		idx := strings.Index(source, token)
		if idx < 0 {
			t.Fatalf("portal mutation route missing %q", token)
		}
		if idx < jwtIndex {
			t.Fatalf("portal mutation route %q is registered before PortalMutationJWT", token)
		}
	}
}
