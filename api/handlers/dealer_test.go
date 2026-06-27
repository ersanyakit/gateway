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

func TestBalanceAuthoritySourceContractsUseLedgerOnly(t *testing.T) {
	dealerSource := readHandlerSource(t, "dealer.go")
	for _, forbidden := range []string{
		"MerchantDepositSummary(c.Context()",
		"AllWalletDeposits(ctx)",
		"buildWalletBalanceMap(c.Context(), deps.TransactionRepo",
	} {
		if strings.Contains(dealerSource, forbidden) {
			t.Fatalf("dealer balance authority must not use transaction-derived source %q", forbidden)
		}
	}
	for _, required := range []string{
		"LedgerRepo.MerchantBalances",
		"WalletBalancesByWalletIDs",
		"buildWalletBalanceMap(c.Context(), deps.LedgerRepo",
	} {
		if !strings.Contains(dealerSource, required) {
			t.Fatalf("dealer balance authority missing ledger-derived source %q", required)
		}
	}

	v1Source := readHandlerSource(t, "v1api.go")
	commonBalance := extractHandlerFunctionBody(t, v1Source, "HandleV1CommonBalance")
	walletBalance := extractHandlerFunctionBody(t, v1Source, "v1WalletBalances")
	for name, body := range map[string]string{
		"HandleV1CommonBalance": commonBalance,
		"v1WalletBalances":      walletBalance,
	} {
		for _, forbidden := range []string{"TransactionRepo", "Blockchains", "BatchBalances", "Balance("} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s must not use non-ledger balance source %q", name, forbidden)
			}
		}
	}
	if !strings.Contains(commonBalance, "LedgerRepo.DomainBalances") {
		t.Fatal("common balance endpoint must read domain ledger balances")
	}
	if !strings.Contains(walletBalance, "LedgerRepo.WalletBalances") {
		t.Fatal("wallet balance endpoint must read wallet ledger balances")
	}
}

func TestDealerBalanceViewsUseLedgerOnly(t *testing.T) {
	sourceBytes, err := os.ReadFile("dealer.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)

	dashboard := extractHandlerFunctionBody(t, source, "HandleDealerDashboard")
	for _, forbidden := range []string{
		"MerchantDepositSummary",
		"DomainDepositSummary",
		"AllWalletDeposits",
	} {
		if strings.Contains(dashboard, forbidden) {
			t.Fatalf("dealer dashboard balance path must not use transaction-derived helper %q", forbidden)
		}
	}
	for _, required := range []string{
		"deps.LedgerRepo.MerchantBalances",
		"dealerLedgerBalanceViews",
	} {
		if !strings.Contains(dashboard, required) {
			t.Fatalf("dealer dashboard balance path missing ledger authority %q", required)
		}
	}

	if !strings.Contains(source, "func buildWalletBalanceMap(ctx context.Context, ledgerRepo *repositories.LedgerRepo") {
		t.Fatal("admin wallet balance map must accept LedgerRepo, not TransactionRepo")
	}
	walletMap := extractHandlerFunctionBody(t, source, "buildWalletBalanceMap")
	for _, forbidden := range []string{
		"TransactionRepo",
		"AllWalletDeposits",
		"MerchantDepositSummary",
	} {
		if strings.Contains(walletMap, forbidden) {
			t.Fatalf("admin wallet balance map must not use transaction-derived helper %q", forbidden)
		}
	}
	if !strings.Contains(walletMap, "WalletBalancesByWalletIDs") {
		t.Fatal("admin wallet balance map must batch-read ledger balances")
	}
}

func TestV1BalanceEndpointsUseLedgerOnly(t *testing.T) {
	sourceBytes, err := os.ReadFile("v1api.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	common := extractHandlerFunctionBody(t, source, "HandleV1CommonBalance")
	wallet := extractHandlerFunctionBody(t, source, "v1WalletBalances")

	for name, body := range map[string]string{
		"common balance": common,
		"wallet balance": wallet,
	} {
		for _, forbidden := range []string{
			"TransactionRepo",
			"MerchantDepositSummary",
			"DomainDepositSummary",
			"AllWalletDeposits",
			"Blockchains",
			"GetBalance",
			"BalanceOf",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s path must not use non-ledger balance authority %q", name, forbidden)
			}
		}
	}
	if !strings.Contains(common, "deps.LedgerRepo.DomainBalances") {
		t.Fatal("common balance endpoint must use LedgerRepo.DomainBalances")
	}
	if !strings.Contains(wallet, "deps.LedgerRepo.WalletBalances") {
		t.Fatal("wallet balance endpoint must use LedgerRepo.WalletBalances")
	}
}

func TestOutboundHandlersRequireLedgerReservationContracts(t *testing.T) {
	transferSource := readHandlerSource(t, "transfer.go")
	executeBody := extractHandlerFunctionBody(t, transferSource, "ExecuteWalletTransfer")
	if !strings.Contains(executeBody, "ErrLedgerReservationRequired") {
		t.Fatal("ExecuteWalletTransfer must reject direct sweep calls without a ledger reservation")
	}
	handleWithdrawBody := extractHandlerFunctionBody(t, transferSource, "HandleWithdraw")
	for _, forbidden := range []string{
		"ExecuteWalletTransfer(walletRepo, chains, params, false)",
		"ExecuteWalletTransfer(walletRepo, chains, params, true)",
	} {
		if strings.Contains(handleWithdrawBody, forbidden) {
			t.Fatalf("legacy HandleWithdraw must not direct-broadcast without reservation: %q", forbidden)
		}
	}
	handleSweepBody := extractHandlerFunctionBody(t, transferSource, "HandleSweep")
	for _, forbidden := range []string{
		"ExecuteWalletTransfer(walletRepo, chains, params, true)",
		"ExecuteWalletTransfer(walletRepo, chains, params, false)",
	} {
		if strings.Contains(handleSweepBody, forbidden) {
			t.Fatalf("legacy HandleSweep must not direct-broadcast without reservation: %q", forbidden)
		}
	}

	dealerSource := readHandlerSource(t, "dealer.go")
	adminSweepBody := extractHandlerFunctionBody(t, dealerSource, "HandleAdminSweep")
	for _, required := range []string{
		"CreateWithHold",
		"ApproveWithTransfer",
		"manual sweep requires an explicit amount",
	} {
		if !strings.Contains(adminSweepBody, required) {
			t.Fatalf("admin sweep reservation contract missing %q", required)
		}
	}
	if strings.Contains(adminSweepBody, "ExecuteWalletTransfer(deps.WalletRepo, deps.Blockchains, params, isSweep)") {
		t.Fatal("admin sweep must not direct-broadcast through ExecuteWalletTransfer without a hold")
	}
}

func TestV1PayoutCreateMapsInsufficientHoldToBadRequest(t *testing.T) {
	source := readHandlerSource(t, "v1api.go")
	body := extractHandlerFunctionBody(t, source, "HandleV1PayoutCreate")
	for _, token := range []string{
		"errors.Is(err, repositories.ErrInsufficientAvailableBalance)",
		"fiber.StatusBadRequest",
		"payout creation failed",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("V1 payout create insufficient-balance mapping missing %q", token)
		}
	}
}

func TestV1RefundCreateMapsInsufficientHoldToBadRequest(t *testing.T) {
	source := readHandlerSource(t, "v1api.go")
	body := extractHandlerFunctionBody(t, source, "HandleV1RefundCreate")
	for _, token := range []string{
		"errors.Is(err, repositories.ErrInsufficientAvailableBalance)",
		"fiber.StatusBadRequest",
		"refund creation failed",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("V1 refund create insufficient-balance mapping missing %q", token)
		}
	}
}

func TestAdminWithdrawalOperatorActionsWriteAuditLogs(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	for _, tc := range []struct {
		function string
		tokens   []string
	}{
		{
			function: "HandleAdminWithdrawalApprove",
			tokens: []string{
				`logDealerActivity(c, deps.ActivityLogRepo, &request.MerchantID, "admin", adminEmail, "withdrawal.approve", "failed"`,
				`logDealerActivity(c, deps.ActivityLogRepo, &request.MerchantID, "admin", adminEmail, "withdrawal.approve", "success"`,
			},
		},
		{
			function: "HandleAdminWithdrawalReject",
			tokens: []string{
				`logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "withdrawal.reject", "failed"`,
				`logDealerActivity(c, deps.ActivityLogRepo, &request.MerchantID, "admin", adminEmail, "withdrawal.reject", "success"`,
			},
		},
		{
			function: "HandleAdminRefundApprove",
			tokens: []string{
				`logDealerActivity(c, deps.ActivityLogRepo, &refund.MerchantID, "admin", adminEmail, "refund.approve", "failed"`,
				`logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "refund.approve", "failed"`,
				`logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "refund.approve", "success"`,
			},
		},
		{
			function: "HandleAdminRefundReject",
			tokens: []string{
				`logDealerActivity(c, deps.ActivityLogRepo, merchantID, "admin", adminEmail, "refund.reject", "failed"`,
				`logDealerActivity(c, deps.ActivityLogRepo, merchantID, "admin", adminEmail, "refund.reject", "success"`,
			},
		},
	} {
		body := extractHandlerFunctionBody(t, source, tc.function)
		for _, token := range tc.tokens {
			if !strings.Contains(body, token) {
				t.Fatalf("%s audit contract missing %q", tc.function, token)
			}
		}
	}
}

func TestAdminTestDepositPaymentMatchUsesExplicitOutcomeBoundary(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	body := extractHandlerFunctionBody(t, source, "deliverAdminPaymentWebhookIfMatched")
	for _, token := range []string{
		"deps.PaymentRepo.MatchFinalizedTransaction",
		"matchResult.Session",
		"deps.WebhookDeliveryRepo.EnqueuePayment",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("admin test deposit payment match helper missing %q", token)
		}
	}
	if strings.Contains(body, "MarkPaidByTransaction") {
		t.Fatal("admin test deposit payment match helper must not use paid-only wrapper")
	}
}

func TestAddTokenAmountRawSumsSignedLedgerValues(t *testing.T) {
	tests := map[string]struct {
		current string
		next    string
		want    string
	}{
		"empty current":      {"", "13", "13"},
		"positive sum":       {"25", "13", "38"},
		"negative net":       {"25", "-30", "-5"},
		"invalid current":    {"not-raw", "7", "7"},
		"invalid next keeps": {"9", "bad", "9"},
	}
	for name, tc := range tests {
		if got := addTokenAmountRaw(tc.current, tc.next); got != tc.want {
			t.Fatalf("%s: addTokenAmountRaw(%q, %q) = %q, want %q", name, tc.current, tc.next, got, tc.want)
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
		LastError:          "webhook_secret=should-not-render",
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
	if view.LastError != "redacted sensitive delivery error" {
		t.Fatalf("last error = %q, want redacted sensitive delivery error", view.LastError)
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

func extractHandlerFunctionBody(t *testing.T, source, functionName string) string {
	t.Helper()
	start := strings.Index(source, "func "+functionName+"(")
	if start == -1 {
		t.Fatalf("function %s not found", functionName)
	}
	open := strings.Index(source[start:], "{")
	if open == -1 {
		t.Fatalf("function %s has no opening brace", functionName)
	}
	index := start + open
	depth := 0
	for i := index; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[index : i+1]
			}
		}
	}
	t.Fatalf("function %s has no closing brace", functionName)
	return ""
}

func readHandlerSource(t *testing.T, path string) string {
	t.Helper()
	sourceBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(sourceBytes)
}
