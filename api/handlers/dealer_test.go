package handlers

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"core/models"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func TestPaginationURLPreservesExistingQuery(t *testing.T) {
	got := paginationURL("/admin/deposits?from=0xabc&hash=0xdef", 2, 50)
	want := "/admin/deposits?from=0xabc&hash=0xdef&page=2&limit=50"
	if got != want {
		t.Fatalf("paginationURL() = %q, want %q", got, want)
	}
}

func TestAdminDashboardTemplateParses(t *testing.T) {
	if _, err := template.ParseFiles("../../views/dealer/admin_dashboard.html"); err != nil {
		t.Fatal(err)
	}
}

func TestPaginationURLAddsQuery(t *testing.T) {
	got := paginationURL("/admin/deposits", 3, 25)
	want := "/admin/deposits?page=3&limit=25"
	if got != want {
		t.Fatalf("paginationURL() = %q, want %q", got, want)
	}
}

func TestRequireAdminIgnoresSignedCookieWithoutGuard(t *testing.T) {
	app := fiber.New()
	app.Get("/admin", func(c fiber.Ctx) error {
		if _, ok := requireAdmin(c); ok {
			return c.Status(fiber.StatusOK).SendString("ok")
		}
		return c.Status(fiber.StatusUnauthorized).SendString("unauthorized")
	})

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{
		Name:  adminSessionCookie,
		Value: signedDealerSessionValue(adminSessionPayload("not-admin@example.com", time.Now().Add(time.Hour))),
	})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}
}

func TestRequireAdminUsesGuardLocal(t *testing.T) {
	app := fiber.New()
	app.Get("/admin", func(c fiber.Ctx) error {
		c.Locals(adminSessionEmailLocal, "Admin@Example.COM")
		email, ok := requireAdmin(c)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).SendString("unauthorized")
		}
		if email != "admin@example.com" {
			t.Fatalf("email = %q, want %q", email, "admin@example.com")
		}
		return c.Status(fiber.StatusOK).SendString("ok")
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/admin", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
}

func TestParseAdminSessionPayloadRejectsLegacyEmail(t *testing.T) {
	if _, err := parseAdminSessionPayload("admin@example.com", time.Now()); err == nil {
		t.Fatal("expected legacy admin session payload to be rejected")
	}
}

func TestAdminWebhookReplaySourceContractAuditsDenialsAndUsesReplayRepo(t *testing.T) {
	sourceBytes, err := os.ReadFile("dealer.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	start := strings.Index(source, "func HandleAdminWebhookReplay")
	if start < 0 {
		t.Fatal("HandleAdminWebhookReplay missing")
	}
	body := source[start:]
	if end := strings.Index(body, "\nfunc "); end >= 0 {
		body = body[:end]
	}

	for _, token := range []string{
		"EnqueueReplay",
		"WebhookReplayParams",
		`logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "failed"`,
		"Replay reddedildi veya delivery bulunamadı.",
		"Webhook delivery bulunamadı veya replay yetkin yok.",
		"Replay zaten aktif; duplicate istek no-op.",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("admin webhook replay source missing %q", token)
		}
	}
}

func TestDealerWebhookDeliveryViewsExposeDeadLetterReplayDiagnostics(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	originalID := uuid.New()
	row := models.WebhookDelivery{
		ID:                 uuid.New(),
		EventID:            "payment-id:payment_succeeded",
		EventType:          "payment_succeeded",
		TargetURL:          "https://merchant.example/webhook",
		Status:             models.WebhookDeliveryStatusDeadLetter,
		Attempts:           8,
		LastError:          "timeout",
		FailureCategory:    "timeout",
		OriginalDeliveryID: &originalID,
		ReplayCount:        2,
		ReplayRequestedBy:  "admin@example.com",
		ReplayRequestedAt:  &now,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	views := dealerWebhookDeliveryViews([]models.WebhookDelivery{row})
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	view := views[0]
	if view.FailureCategory != "timeout" || view.NextAction != "replay_or_investigate" || view.OriginalDeliveryID != originalID.String() {
		t.Fatalf("view diagnostics = %#v", view)
	}
	if view.ReplayCount != 2 || view.ReplayRequestedBy != "admin@example.com" || view.ReplayRequestedAt == "" {
		t.Fatalf("view replay metadata = %#v", view)
	}
}

func TestWebhookDeliveryNextActionFallbacks(t *testing.T) {
	tests := map[string]string{
		models.WebhookDeliveryStatusPending:    "delivery_pending",
		models.WebhookDeliveryStatusProcessing: "delivery_in_progress",
		models.WebhookDeliveryStatusFailed:     "waiting_retry",
		models.WebhookDeliveryStatusDeadLetter: "replay_or_investigate",
		models.WebhookDeliveryStatusSucceeded:  "",
	}
	for status, want := range tests {
		if got := webhookDeliveryNextAction(models.WebhookDelivery{Status: status}); got != want {
			t.Fatalf("status %s next action = %q, want %q", status, got, want)
		}
	}
	if got := webhookDeliveryNextAction(models.WebhookDelivery{Status: models.WebhookDeliveryStatusFailed, OperatorAction: "custom_action"}); got != "custom_action" {
		t.Fatalf("operator action override = %q, want custom_action", got)
	}
}
