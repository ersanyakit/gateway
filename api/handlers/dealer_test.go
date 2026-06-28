package handlers

import (
	"context"
	"html/template"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/models"
	"core/repositories"

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

func TestActivityDashboardTabUsesPathAndQueryFallback(t *testing.T) {
	app := fiber.New()
	app.Get("/merchant/dashboard/activity", func(c fiber.Ctx) error {
		return c.SendString(activityDashboardTab(c))
	})
	app.Get("/merchant/dashboard/activity/:subsection", func(c fiber.Ctx) error {
		return c.SendString(activityDashboardTab(c))
	})

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/merchant/dashboard/activity", "audit"},
		{"/merchant/dashboard/activity/payments", "payments"},
		{"/merchant/dashboard/activity/deposits", "deposits"},
		{"/merchant/dashboard/activity?tab=payments", "payments"},
		{"/merchant/dashboard/activity?status=paid", "payments"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s request failed: %v", tc.path, err)
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("%s body read failed: %v", tc.path, err)
		}
		body := string(bodyBytes)
		if body != tc.want {
			t.Fatalf("%s tab = %q, want %q", tc.path, body, tc.want)
		}
	}
}

func TestAdminDashboardTemplateParses(t *testing.T) {
	if _, err := template.ParseFiles("../../views/dealer/admin_dashboard.html"); err != nil {
		t.Fatal(err)
	}
}

func TestMerchantDashboardTemplateParses(t *testing.T) {
	if _, err := template.ParseFiles("../../views/dealer/dashboard.html"); err != nil {
		t.Fatal(err)
	}
}

func TestMerchantUsersPanelHasSingleSearch(t *testing.T) {
	dashboard, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(dashboard)
	if strings.Count(html, `name="q" type="search"`) != 1 {
		t.Fatal("merchant users panel must keep one server-side user search")
	}
	if strings.Contains(html, `data-admin-table-search="merchant-wallet-table"`) {
		t.Fatal("merchant users panel must not render a second page-local wallet table search")
	}
}

func TestMerchantDashboardPanelsUseSharedHeaders(t *testing.T) {
	dashboard, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(dashboard)
	if strings.Contains(html, "merchant-products-toolbar") || strings.Contains(html, "merchant-products-actions") {
		t.Fatal("merchant dashboard panels must use the shared section header/action classes")
	}
	for _, title := range []string{
		"Biriken varlıklar",
		"Network kasaları",
		"Users &amp; Wallets",
		"Transaction yeniden işleme",
		"Activity",
		"Transactions",
		"Integrations",
		"Çekim talepleri",
		"Görünüm ayarları",
		"Desteklenen ağlar",
	} {
		if !strings.Contains(html, "<h2>"+title+"</h2>") {
			t.Fatalf("merchant dashboard missing panel title %q", title)
		}
	}
	if count := strings.Count(html, `class="merchant-section-header"`); count < 10 {
		t.Fatalf("merchant dashboard section header count = %d, want at least 10", count)
	}
}

func TestAdminLiveBalanceRawParsesTronHexNative(t *testing.T) {
	raw, err := adminLiveBalanceRaw("0xf4240", asset.NewTRX(constants.TRON))
	if err != nil {
		t.Fatal(err)
	}
	if raw != "1000000" {
		t.Fatalf("raw = %q, want 1000000", raw)
	}
}

func TestAdminLiveBalanceRawParsesNativeComponentDecimal(t *testing.T) {
	raw, err := adminLiveBalanceRaw("ETH:1.500000000000000000 | WETH:0", asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	if err != nil {
		t.Fatal(err)
	}
	if raw != "1500000000000000000" {
		t.Fatalf("raw = %q, want 1500000000000000000", raw)
	}
}

func TestAdminLiveBalanceRawRejectsMissingSelectedToken(t *testing.T) {
	_, err := adminLiveBalanceRaw("TRX:0xf4240", asset.NewTRC20(constants.TRON, "TToken", "USDT", "Tether", 6))
	if err == nil {
		t.Fatal("expected missing token balance to fail")
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

func TestAdminRoleCanMutateHighRiskActions(t *testing.T) {
	for _, tc := range []struct {
		role string
		want bool
	}{
		{"", true},
		{"owner", true},
		{"admin", true},
		{"security", true},
		{"operator", false},
		{"viewer", false},
	} {
		if got := adminRoleCanMutateHighRisk(tc.role); got != tc.want {
			t.Fatalf("adminRoleCanMutateHighRisk(%q) = %v, want %v", tc.role, got, tc.want)
		}
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

func TestAdminVaultUsesLedgerPlatformBalances(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	dashboard := extractHandlerFunctionBody(t, source, "HandleAdminDashboard")
	for _, required := range []string{
		`case "vault":`,
		"deps.LedgerRepo.PlatformBalances",
		"dealerVaultBalanceViews",
	} {
		if !strings.Contains(dashboard, required) {
			t.Fatalf("admin vault path missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"TransactionRepo.AllWalletDeposits",
		"MerchantDepositSummary",
		"DomainDepositSummary",
		"BatchBalances",
		"GetBalance",
	} {
		if strings.Contains(dashboard, forbidden) {
			t.Fatalf("admin vault must not use non-ledger balance source %q", forbidden)
		}
	}
}

func TestAdminDashboardPaginatesStandaloneAdminLists(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	manageAdmins := extractHandlerFunctionBody(t, source, "HandleAdminManageAdmins")
	for _, required := range []string{
		"adminDashboardPageParams(c)",
		"adminHeaderStatsFor(c.Context(), deps).applyTo(&data)",
		"data.AdminMerchants = paginateViewSlice(adminViews, page, limit)",
		`dealerPaginationView(page, limit, int64(len(adminViews)), "/admin/admins")`,
	} {
		if !strings.Contains(manageAdmins, required) {
			t.Fatalf("admin accounts route missing pagination token %q", required)
		}
	}

	dashboard := extractHandlerFunctionBody(t, source, "HandleAdminDashboard")
	for _, required := range []string{
		"data.AdminVaults = paginateViewSlice(vaultViews, page, limit)",
		`dealerPaginationView(page, limit, int64(len(vaultViews)), "/admin/vault")`,
		"deps.OutboundPolicyRepo.ListWhitelistPage(c.Context(), page, limit)",
		`dealerPaginationView(page, limit, total, "/admin/security")`,
	} {
		if !strings.Contains(dashboard, required) {
			t.Fatalf("admin dashboard missing pagination token %q", required)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	template := string(templateBytes)
	for _, required := range []string{
		`{{.AdminPagination.Total}} asset`,
		`data-admin-table-count="admin-vault-table"`,
		`action="/admin/security/outbound-whitelist"`,
	} {
		if !strings.Contains(template, required) {
			t.Fatalf("admin template missing pagination token %q", required)
		}
	}
	if strings.Count(template, `{{template "pg" .AdminPagination}}`) < 13 {
		t.Fatal("admin template should render pagination on every list-heavy panel")
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
	adminSweepBody := extractHandlerFunctionBody(t, dealerSource, "HandleAdminRecoverFunds")
	for _, required := range []string{
		"CreateWithHold",
		"ApproveWithTransfer",
		"manual sweep requires an explicit amount",
	} {
		if !strings.Contains(adminSweepBody, required) {
			t.Fatalf("admin recover funds reservation contract missing %q", required)
		}
	}
	if strings.Contains(adminSweepBody, "ExecuteWalletTransfer(deps.WalletRepo, deps.Blockchains, params, isSweep)") {
		t.Fatal("admin recover funds must not direct-broadcast through ExecuteWalletTransfer without a hold")
	}
}

func TestAdminOutboundTransfersCarrySignerAuditContext(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	for name, body := range map[string]string{
		"HandleAdminRecoverFunds":      extractHandlerFunctionBody(t, source, "HandleAdminRecoverFunds"),
		"HandleAdminWithdrawalApprove": extractHandlerFunctionBody(t, source, "HandleAdminWithdrawalApprove"),
		"HandleAdminRefundApprove":     extractHandlerFunctionBody(t, source, "HandleAdminRefundApprove"),
	} {
		if !strings.Contains(body, "ActorID:       adminEmail") {
			t.Fatalf("%s must pass admin actor id into signer audit context", name)
		}
		if !strings.Contains(body, "CorrelationID: dealerSignerCorrelationID") {
			t.Fatalf("%s must pass request/resource correlation id into signer audit context", name)
		}
	}
}

func TestOutboundMoneyActionsEnforcePolicyBeforeMutation(t *testing.T) {
	dealerSource := readHandlerSource(t, "dealer.go")
	for _, tc := range []struct {
		function      string
		policyToken   string
		mutationToken string
	}{
		{"HandleDealerWithdrawalCreate", "enforceDealerOutboundPolicy", "deps.WithdrawalRepo.CreateWithHold"},
		{"HandleAdminRecoverFunds", "enforceDealerOutboundPolicy", "deps.WithdrawalRepo.CreateWithHold"},
		{"HandleAdminWithdrawalApprove", "enforceDealerOutboundPolicy", "deps.WithdrawalRepo.ApproveWithTransfer"},
		{"HandleAdminRefundApprove", "enforceDealerOutboundPolicy", "ensureDealerReserveWallet"},
	} {
		body := extractHandlerFunctionBody(t, dealerSource, tc.function)
		policyIndex := strings.Index(body, tc.policyToken)
		mutationIndex := strings.Index(body, tc.mutationToken)
		if policyIndex == -1 {
			t.Fatalf("%s missing outbound policy guard", tc.function)
		}
		if mutationIndex == -1 {
			t.Fatalf("%s missing mutation token %q", tc.function, tc.mutationToken)
		}
		if policyIndex > mutationIndex {
			t.Fatalf("%s must enforce outbound policy before %q", tc.function, tc.mutationToken)
		}
	}

	v1Source := readHandlerSource(t, "v1api.go")
	for _, tc := range []struct {
		function      string
		mutationToken string
	}{
		{"HandleV1PayoutCreate", "ensureV1ReserveWallet"},
		{"HandleV1RefundCreate", "ensureV1ReserveWallet"},
	} {
		body := extractHandlerFunctionBody(t, v1Source, tc.function)
		policyIndex := strings.Index(body, "enforceV1OutboundPolicy")
		mutationIndex := strings.Index(body, tc.mutationToken)
		if policyIndex == -1 {
			t.Fatalf("%s missing V1 outbound policy guard", tc.function)
		}
		if mutationIndex == -1 {
			t.Fatalf("%s missing mutation token %q", tc.function, tc.mutationToken)
		}
		if policyIndex > mutationIndex {
			t.Fatalf("%s must enforce outbound policy before %q", tc.function, tc.mutationToken)
		}
	}
}

func TestAdminHighRiskActionsRequirePrivilegedRoleBeforeLookup(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	for _, tc := range []struct {
		function    string
		lookupToken string
	}{
		{"HandleAdminRecoverFunds", "parseAdminAssetSelection"},
		{"HandleAdminWithdrawalApprove", "uuid.Parse"},
		{"HandleAdminWithdrawalReject", "uuid.Parse"},
		{"HandleAdminRefundApprove", "uuid.Parse"},
		{"HandleAdminRefundReject", "uuid.Parse"},
	} {
		body := extractHandlerFunctionBody(t, source, tc.function)
		guardIndex := strings.Index(body, "requirePrivilegedAdmin(c, deps.AdminRepo)")
		lookupIndex := strings.Index(body, tc.lookupToken)
		if guardIndex < 0 {
			t.Fatalf("%s missing privileged admin guard", tc.function)
		}
		if lookupIndex < 0 {
			t.Fatalf("%s missing lookup token %q", tc.function, tc.lookupToken)
		}
		if guardIndex > lookupIndex {
			t.Fatalf("%s must check privileged admin role before %q", tc.function, tc.lookupToken)
		}
	}
}

func TestAdminOutboundSecurityGuardsAndControlsSourceContract(t *testing.T) {
	routes := readHandlerSource(t, "../routes/routes.go")
	for _, token := range []string{
		"portalJWT := middleware.PortalMutationJWT()",
		`r.fiber.Use("/dealer", portalJWT)`,
		`r.fiber.Use("/merchant", portalJWT)`,
		`r.fiber.Use("/admin", portalJWT)`,
		`HandleAdminOutboundPolicyUpdate(dealerDeps)`,
		`HandleAdminOutboundWhitelistCreate(dealerDeps)`,
		`HandleAdminOutboundWhitelistToggle(dealerDeps)`,
	} {
		if !strings.Contains(routes, token) {
			t.Fatalf("routes missing security token %q", token)
		}
	}

	source := readHandlerSource(t, "dealer.go")
	for _, tc := range []struct {
		function      string
		resourceToken string
	}{
		{"HandleAdminRecoverFunds", `strings.TrimSpace(c.FormValue("wallet_id"))`},
		{"HandleAdminWithdrawalApprove", `uuid.Parse(c.Params("id"))`},
		{"HandleAdminWithdrawalReject", `uuid.Parse(c.Params("id"))`},
		{"HandleAdminRefundApprove", `uuid.Parse(c.Params("id"))`},
		{"HandleAdminRefundReject", `uuid.Parse(c.Params("id"))`},
	} {
		body := extractHandlerFunctionBody(t, source, tc.function)
		guardIndex := strings.Index(body, "requirePrivilegedAdmin(c, deps.AdminRepo)")
		resourceIndex := strings.Index(body, tc.resourceToken)
		if guardIndex == -1 {
			t.Fatalf("%s missing role privilege guard", tc.function)
		}
		if resourceIndex == -1 {
			t.Fatalf("%s missing resource token %q", tc.function, tc.resourceToken)
		}
		if guardIndex > resourceIndex {
			t.Fatalf("%s must check role before resource lookup", tc.function)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	template := string(templateBytes)
	for _, token := range []string{
		"{{.AdminRole}}",
		"{{.AdminOutboundPolicy.ConfigurationSummary}}",
		`/admin/security/outbound-policy`,
		`/admin/security/outbound-whitelist`,
		`name="role"`,
	} {
		if !strings.Contains(template, token) {
			t.Fatalf("admin security template missing %q", token)
		}
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

func TestV1OutboundCreateUsesIdempotencyAndCorrelationMetadata(t *testing.T) {
	source := readHandlerSource(t, "v1api.go")
	for _, tc := range []struct {
		name         string
		function     string
		resource     string
		enqueueToken string
		repairToken  string
	}{
		{name: "payout", function: "HandleV1PayoutCreate", resource: "payout", enqueueToken: "enqueueV1PayoutRequestedLifecycle", repairToken: "repairV1PayoutRequestedLifecycleOnReplay"},
		{name: "refund", function: "HandleV1RefundCreate", resource: "refund", enqueueToken: "enqueueV1RefundRequestedLifecycle", repairToken: "repairV1RefundRequestedLifecycleOnReplay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := extractHandlerFunctionBody(t, source, tc.function)
			for _, token := range []string{
				"beginV1CreateIdempotency",
				"validateV1CreateMetadata",
				"IdempotencyKey:",
				"CorrelationID:",
				"CompleteResource",
				tc.enqueueToken,
				tc.repairToken,
				`"` + tc.resource + `"`,
			} {
				if !strings.Contains(body, token) {
					t.Fatalf("%s missing idempotent create token %q", tc.function, token)
				}
			}
			completeIndex := strings.Index(body, "CompleteResource")
			enqueueIndex := strings.Index(body, tc.enqueueToken)
			if completeIndex == -1 || enqueueIndex == -1 || enqueueIndex < completeIndex {
				t.Fatalf("%s must enqueue requested lifecycle events only after create/idempotency completion", tc.function)
			}
		})
	}
}

func TestAdminOutboundApproveDoesNotEmitTerminalEventAtBroadcast(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	create := extractHandlerFunctionBody(t, source, "HandleDealerWithdrawalCreate")
	for _, token := range []string{
		"DomainID:",
		"CorrelationID:",
		"constants.WebhookEventPayoutRequestedV1",
		"outbound_requested_event_enqueue_failed",
	} {
		if !strings.Contains(create, token) {
			t.Fatalf("dealer withdrawal create must persist lifecycle metadata and enqueue requested event: %q", token)
		}
	}

	withdrawal := extractHandlerFunctionBody(t, source, "HandleAdminWithdrawalApprove")
	if !strings.Contains(withdrawal, "requireOutboundMakerChecker") {
		t.Fatal("withdrawal approve must enforce configured maker-checker guard")
	}
	if !strings.Contains(withdrawal, "constants.WebhookEventPayoutBroadcastV1") {
		t.Fatal("withdrawal approve must enqueue broadcast event")
	}
	if strings.Count(withdrawal, "constants.WebhookEventPayoutBroadcastV1") != 1 {
		t.Fatal("withdrawal approve must enqueue broadcast only on the successful persist path")
	}
	if !strings.Contains(withdrawal, "openDealerOutboundLifecycleReconciliation") {
		t.Fatal("withdrawal approve must open reconciliation for processing errors after possible broadcast")
	}
	if strings.Contains(withdrawal, "constants.WebhookEventPayoutFinalizedV1") {
		t.Fatal("withdrawal approve must not enqueue finalized event without finality evidence")
	}

	refund := extractHandlerFunctionBody(t, source, "HandleAdminRefundApprove")
	if !strings.Contains(refund, "requireOutboundMakerChecker") {
		t.Fatal("refund approve must enforce configured maker-checker guard")
	}
	if !strings.Contains(refund, "ClaimPendingWithHoldAndSource") || !strings.Contains(refund, "constants.WebhookEventRefundBroadcastV1") {
		t.Fatal("refund approve must persist source metadata and enqueue broadcast event")
	}
	for _, token := range []string{
		"repositories.OutboundTransferFailureBroadcastUncertain",
		"SetProcessingError",
		"openDealerOutboundLifecycleReconciliation",
		"deps.RefundRepo.Find",
	} {
		if !strings.Contains(refund, token) {
			t.Fatalf("refund approve must preserve processing holds on broadcast-uncertain errors: %q", token)
		}
	}
	for _, forbidden := range []string{
		"MarkSucceededWithLedger",
		"constants.WebhookEventRefundSucceededV1",
	} {
		if strings.Contains(refund, forbidden) {
			t.Fatalf("refund approve must not terminalize immediately after broadcast: %q", forbidden)
		}
	}
}

func TestAdminOutboundActionsAreIdempotentNoOpsOnRetries(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	for _, tc := range []struct {
		name        string
		function    string
		statusToken string
		message     string
		mutateToken string
	}{
		{
			name:        "withdrawal approve",
			function:    "HandleAdminWithdrawalApprove",
			statusToken: "case models.WithdrawalStatusProcessing, models.WithdrawalStatusFinalized, models.WithdrawalStatusApproved:",
			message:     "Çekim onayı zaten işlenmiş.",
			mutateToken: "ApproveWithTransfer",
		},
		{
			name:        "withdrawal reject",
			function:    "HandleAdminWithdrawalReject",
			statusToken: "case models.WithdrawalStatusRejected:",
			message:     "Çekim talebi zaten reddedilmiş.",
			mutateToken: "MarkRejected",
		},
		{
			name:        "refund approve",
			function:    "HandleAdminRefundApprove",
			statusToken: "case models.RefundStatusProcessing, models.RefundStatusSucceeded, models.RefundStatusApproved:",
			message:     "Refund onayı zaten işlenmiş.",
			mutateToken: "ClaimPendingWithHoldAndSource",
		},
		{
			name:        "refund reject",
			function:    "HandleAdminRefundReject",
			statusToken: "case models.RefundStatusRejected:",
			message:     "Refund talebi zaten reddedilmiş.",
			mutateToken: "MarkRejected",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := extractHandlerFunctionBody(t, source, tc.function)
			retryIndex := strings.Index(body, tc.message)
			mutateIndex := strings.Index(body, tc.mutateToken)
			if !strings.Contains(body, tc.statusToken) || retryIndex < 0 {
				t.Fatalf("%s missing idempotent retry no-op guard", tc.function)
			}
			if mutateIndex < 0 || retryIndex > mutateIndex {
				t.Fatalf("%s must return idempotent retry no-op before mutation", tc.function)
			}
		})
	}
}

func TestOutboundMakerCheckerGuardDefaultsOffAndRejectsSelfApprovalWhenEnabled(t *testing.T) {
	if err := requireOutboundMakerChecker("admin@example.com", "admin@example.com"); err != nil {
		t.Fatalf("default-off maker-checker guard rejected self approval: %v", err)
	}
	t.Setenv("OUTBOUND_MAKER_CHECKER_REQUIRED", "true")
	if err := requireOutboundMakerChecker("admin@example.com", "admin@example.com"); err == nil {
		t.Fatal("configured maker-checker guard should reject self approval")
	}
	if err := requireOutboundMakerChecker("", "admin@example.com"); err == nil {
		t.Fatal("configured maker-checker guard should reject blank requester identity")
	}
	if err := requireOutboundMakerChecker("requester@example.com", "admin@example.com"); err != nil {
		t.Fatalf("configured maker-checker guard rejected separate reviewer: %v", err)
	}
}

func TestDealerActivityLogCapturesCorrelationID(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	body := extractHandlerFunctionBody(t, source, "logDealerActivity")
	for _, token := range []string{
		"middleware.RequestIDFromCtx",
		"CorrelationID:",
		"dealerActorRole",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("logDealerActivity must capture audit correlation token %q", token)
		}
	}
}

func TestDealerActorRoleUsesAdminSessionRole(t *testing.T) {
	app := fiber.New()
	app.Get("/probe", func(c fiber.Ctx) error {
		c.Locals(adminSessionRoleLocal, models.AdminRoleSecurity)
		if got := dealerActorRole(c, "admin"); got != models.AdminRoleSecurity {
			t.Fatalf("dealerActorRole admin = %q, want %q", got, models.AdminRoleSecurity)
		}
		if got := dealerActorRole(c, "dealer"); got != "dealer" {
			t.Fatalf("dealerActorRole dealer = %q, want dealer", got)
		}
		return c.SendStatus(fiber.StatusOK)
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/probe", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
}

func TestDealerDonationPaymentLinkCreateAndUpdateContracts(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	for _, fn := range []string{"HandleDealerProductCreate", "HandleDealerProductUpdate"} {
		body := extractHandlerFunctionBody(t, source, fn)
		for _, token := range []string{
			"parseDealerProductForm",
			"form.linkType",
			"form.amount",
			"form.currency",
		} {
			if !strings.Contains(body, token) {
				t.Fatalf("%s missing payment link form token %q", fn, token)
			}
		}
	}

	form := extractHandlerFunctionBody(t, source, "parseDealerProductForm")
	for _, token := range []string{
		"models.IsDonationLinkType(form.linkType)",
		`form.amount = "0"`,
		`form.currency = ""`,
		"types.ValidatePositiveDecimal(form.amount)",
	} {
		if !strings.Contains(form, token) {
			t.Fatalf("parseDealerProductForm missing donation/fixed validation token %q", token)
		}
	}

	routes := readHandlerSource(t, "../routes/routes.go")
	if !strings.Contains(routes, `r.fiber.Post(prefix+"/products/:id/update", handlers.HandleDealerProductUpdate(dealerDeps))`) {
		t.Fatal("dealer product update route is not registered")
	}

	dashboard, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	dashboardHTML := string(dashboard)
	for _, token := range []string{
		`data-payment-link-type`,
		`data-payment-fixed-fields`,
		`data-required-when-fixed="true"`,
		`/merchant/products/{{.ID}}/update`,
		`Donation / serbest tutar`,
	} {
		if !strings.Contains(dashboardHTML, token) {
			t.Fatalf("merchant dashboard missing payment link UI token %q", token)
		}
	}

	dashboardJS, err := os.ReadFile("../../views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(dashboardJS)
	for _, token := range []string{
		"initPaymentLinkTypeToggle",
		"fixedFields.classList.toggle('hidden', donation)",
		"input.required = !donation",
		"input.disabled = donation",
		"currency.disabled = donation",
	} {
		if !strings.Contains(js, token) {
			t.Fatalf("dashboard.js missing payment link toggle token %q", token)
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
				`logDealerDecisionActivity(c, deps.ActivityLogRepo, &request.MerchantID, request.DomainID, "admin", adminEmail, "withdrawal.approve", "success"`,
			},
		},
		{
			function: "HandleAdminWithdrawalReject",
			tokens: []string{
				`logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "withdrawal.reject", "failed"`,
				`logDealerDecisionActivity(c, deps.ActivityLogRepo, &updated.MerchantID, updated.DomainID, "admin", adminEmail, "withdrawal.reject", "success"`,
			},
		},
		{
			function: "HandleAdminRefundApprove",
			tokens: []string{
				`logDealerActivity(c, deps.ActivityLogRepo, &refund.MerchantID, "admin", adminEmail, "refund.approve", "failed"`,
				`logDealerDecisionActivity(c, deps.ActivityLogRepo, &refund.MerchantID, &refundDomainID, "admin", adminEmail, "refund.approve", "success"`,
			},
		},
		{
			function: "HandleAdminRefundReject",
			tokens: []string{
				`logDealerActivity(c, deps.ActivityLogRepo, merchantID, "admin", adminEmail, "refund.reject", "failed"`,
				`logDealerDecisionActivity(c, deps.ActivityLogRepo, merchantID, domainID, "admin", adminEmail, "refund.reject", "success"`,
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

func TestAdminSweepLiveBalanceSourceContract(t *testing.T) {
	routes := readHandlerSource(t, "../routes/routes.go")
	if !strings.Contains(routes, `r.fiber.Get("/admin/sweep/live-balance", handlers.HandleAdminSweepLiveBalance(dealerDeps))`) {
		t.Fatal("admin sweep live balance route must be registered")
	}

	source := readHandlerSource(t, "dealer.go")
	body := extractHandlerFunctionBody(t, source, "HandleAdminSweepLiveBalance")
	for _, token := range []string{
		"requireAdmin(c)",
		`parseAdminAssetSelection(deps.AssetRegistry, c.Query("asset"))`,
		"repositories.WalletAddressForChainID(*wallet, chainID)",
		"deps.Blockchains.GetChainByID(chainID)",
		"adminLiveBalanceRawForAsset(ctx, chain, address, selectedAsset)",
		`"result":      "success"`,
		`"balance_raw": raw`,
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("live balance contract missing %q", token)
		}
	}

	helperBody := extractHandlerFunctionBody(t, source, "adminLiveBalanceRawForAsset")
	for _, token := range []string{
		"selected.GetChainType() == asset.ChainEVM",
		"adminLiveEVMTokenBalanceRaw(ctx, chain, address, selected)",
		"chain.BatchBalances(ctx, []string{address}, 1)",
		"adminLiveBalanceRaw(result.Balance, selected)",
	} {
		if !strings.Contains(helperBody, token) {
			t.Fatalf("live balance helper contract missing %q", token)
		}
	}

	evmBody := extractHandlerFunctionBody(t, source, "adminLiveEVMTokenBalanceRaw")
	for _, token := range []string{
		"asset.TokenAddress(selected)",
		"erc20.NewERC20Caller",
		"caller.BalanceOf",
	} {
		if !strings.Contains(evmBody, token) {
			t.Fatalf("EVM token live balance helper missing %q", token)
		}
	}
}

func TestAdminLiveBalanceRawSelectsNativeAndTokenComponents(t *testing.T) {
	nativeETH := asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18)
	if got, err := adminLiveBalanceRaw("ETH:1.25 | WETH:0.5", nativeETH); err != nil || got != "1250000000000000000" {
		t.Fatalf("native raw = %q err=%v, want 1250000000000000000", got, err)
	}

	weth := asset.NewERC20(constants.Ethereum, "0x0000000000000000000000000000000000000001", "WETH", "Wrapped Ether", 18)
	if got, err := adminLiveBalanceRaw("ETH:1.25 | WETH:0.5", weth); err != nil || got != "500000000000000000" {
		t.Fatalf("token raw = %q err=%v, want 500000000000000000", got, err)
	}

	usdc := asset.NewERC20(constants.Base, "0x0000000000000000000000000000000000000002", "USDC", "USD Coin", 6)
	if got, err := adminLiveBalanceRaw("ETH:0.1 | USDC:12.345678", usdc); err != nil || got != "12345678" {
		t.Fatalf("decimal token raw = %q err=%v, want 12345678", got, err)
	}
	if _, err := adminLiveBalanceRaw("USDC:0.0000001", usdc); err == nil {
		t.Fatal("expected over-precision token balance to be rejected")
	}
}

func TestAdminLiveBalanceRawForAssetUsesBatchBalancesForNative(t *testing.T) {
	chain := &adminLiveBalanceTestChain{
		id:      constants.Ethereum,
		name:    "ethereum",
		results: []models.BalanceResult{{Address: "0x1111111111111111111111111111111111111111", Balance: "ETH:1.25 | WETH:0"}},
	}
	raw, err := adminLiveBalanceRawForAsset(context.Background(), chain, "0x1111111111111111111111111111111111111111", asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	if err != nil {
		t.Fatal(err)
	}
	if raw != "1250000000000000000" {
		t.Fatalf("raw = %q, want 1250000000000000000", raw)
	}
	if chain.batchCalls != 1 {
		t.Fatalf("BatchBalances calls = %d, want 1", chain.batchCalls)
	}
}

func TestAdminLiveBalanceRawForAssetRejectsInvalidEVMTokenContractBeforeRPC(t *testing.T) {
	chain := &adminLiveBalanceTestChain{id: constants.Arbitrum, name: "arbitrum"}
	_, err := adminLiveBalanceRawForAsset(context.Background(), chain, "0x1111111111111111111111111111111111111111", asset.NewERC20(constants.Arbitrum, "not-a-contract", "USDC", "USD Coin", 6))
	if err == nil {
		t.Fatal("expected invalid token contract error")
	}
	if chain.batchCalls != 0 {
		t.Fatalf("BatchBalances calls = %d, want 0", chain.batchCalls)
	}
}

type adminLiveBalanceTestChain struct {
	id         constants.ChainID
	name       string
	results    []models.BalanceResult
	batchCalls int
}

func (c *adminLiveBalanceTestChain) ChainID() constants.ChainID { return c.id }
func (c *adminLiveBalanceTestChain) Name() string               { return c.name }
func (c *adminLiveBalanceTestChain) WSS() []string              { return nil }
func (c *adminLiveBalanceTestChain) RPCs() []string             { return nil }
func (c *adminLiveBalanceTestChain) Create(context.Context) (*blockchain.WalletDetails, error) {
	return nil, nil
}
func (c *adminLiveBalanceTestChain) CreateHDWallet(context.Context, int, int) (*blockchain.WalletDetails, error) {
	return nil, nil
}
func (c *adminLiveBalanceTestChain) Deposit(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (c *adminLiveBalanceTestChain) Withdraw(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (c *adminLiveBalanceTestChain) WithdrawToken(context.Context, blockchain.WalletDetails, string, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (c *adminLiveBalanceTestChain) Sweep(context.Context, blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (c *adminLiveBalanceTestChain) SweepTo(context.Context, blockchain.WalletDetails, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (c *adminLiveBalanceTestChain) SweepERC20To(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (c *adminLiveBalanceTestChain) PrefundGas(context.Context, blockchain.WalletDetails, string) (bool, error) {
	return false, nil
}
func (c *adminLiveBalanceTestChain) ValidateAddress(string) bool { return true }
func (c *adminLiveBalanceTestChain) AddWorker(blockchain.Worker) error {
	return nil
}
func (c *adminLiveBalanceTestChain) RemoveWorker(blockchain.Worker) error {
	return nil
}
func (c *adminLiveBalanceTestChain) WorkerCount() int { return 0 }
func (c *adminLiveBalanceTestChain) BatchBalances(context.Context, []string, int) []models.BalanceResult {
	c.batchCalls++
	return c.results
}
func (c *adminLiveBalanceTestChain) StartWorkers(context.Context) error { return nil }
func (c *adminLiveBalanceTestChain) StopWorkers() error                 { return nil }

func TestAdminRecoverNetAmountRawDeductsNativeFees(t *testing.T) {
	t.Setenv("TRON_NATIVE_SWEEP_FEE_SUN", "1100000")
	net, fee, err := adminRecoverNetAmountRaw(nil, nil, asset.NewTRX(constants.TRON), "18500000")
	if err != nil {
		t.Fatal(err)
	}
	if net != "17400000" || fee != "1100000" {
		t.Fatalf("TRX net=%q fee=%q, want net=17400000 fee=1100000", net, fee)
	}

	t.Setenv("SOLANA_TRANSFER_FEE_LAMPORTS", "5000")
	net, fee, err = adminRecoverNetAmountRaw(nil, nil, asset.NewSOL(constants.Solana), "1000000000")
	if err != nil {
		t.Fatal(err)
	}
	if net != "999995000" || fee != "5000" {
		t.Fatalf("SOL net=%q fee=%q, want net=999995000 fee=5000", net, fee)
	}

	t.Setenv("BITCOIN_FEE_RATE_SAT_PER_VBYTE", "12")
	net, fee, err = adminRecoverNetAmountRaw(nil, nil, asset.NewBTC(), "2000")
	if err != nil {
		t.Fatal(err)
	}
	if net != "320" || fee != "1680" {
		t.Fatalf("BTC net=%q fee=%q, want net=320 fee=1680", net, fee)
	}
}

func TestAdminRecoverNetAmountRawLeavesTokenAmountUntouched(t *testing.T) {
	token := asset.NewTRC20(constants.TRON, "TToken", "USDT", "Tether", 6)
	net, fee, err := adminRecoverNetAmountRaw(nil, nil, token, "18500000")
	if err != nil {
		t.Fatal(err)
	}
	if net != "18500000" || fee != "0" {
		t.Fatalf("token net=%q fee=%q, want unchanged amount and zero fee", net, fee)
	}
}

func TestAdminRecoverNetAmountRawRejectsFeeExhaustedAmount(t *testing.T) {
	t.Setenv("TRON_NATIVE_SWEEP_FEE_SUN", "1100000")
	_, _, err := adminRecoverNetAmountRaw(nil, nil, asset.NewTRX(constants.TRON), "1100000")
	if err == nil {
		t.Fatal("expected fee-exhausted native amount to be rejected")
	}
	if !strings.Contains(err.Error(), "network fee sonrası") {
		t.Fatalf("error = %q, want network fee message", err.Error())
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

func TestDealerVaultBalanceViewsSeparatesAvailableAndTransit(t *testing.T) {
	token := "TToken"
	rows := []repositories.LedgerBalanceRow{
		{ChainID: int64(constants.TRON), Token: &token, Symbol: "USDT", Decimals: 6, Account: models.LedgerAccountMerchantAvailable, BalanceRaw: "100000000"},
		{ChainID: int64(constants.TRON), Token: &token, Symbol: "USDT", Decimals: 6, Account: models.LedgerAccountMerchantPending, BalanceRaw: "2000000"},
		{ChainID: int64(constants.TRON), Token: &token, Symbol: "USDT", Decimals: 6, Account: models.LedgerAccountWithdrawalTransit, BalanceRaw: "3000000"},
		{ChainID: int64(constants.TRON), Token: &token, Symbol: "USDT", Decimals: 6, Account: models.LedgerAccountRefundTransit, BalanceRaw: "1000000"},
		{ChainID: int64(constants.TRON), Token: &token, Symbol: "USDT", Decimals: 6, Account: models.LedgerAccountSweepTransit, BalanceRaw: "500000"},
		{ChainID: int64(constants.TRON), Token: &token, Symbol: "USDT", Decimals: 6, Account: models.LedgerAccountPlatformClearing, BalanceRaw: "999999999"},
	}

	views := dealerVaultBalanceViews(rows, nil)
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	view := views[0]
	if view.Symbol != "USDT" || len(view.Details) != 1 {
		t.Fatalf("vault group = %#v, want one USDT detail", view)
	}
	if view.AvailableDisplay != "100" || view.PendingDisplay != "2" || view.WithdrawalDisplay != "3" || view.RefundDisplay != "1" || view.SweepDisplay != "0.5" {
		t.Fatalf("vault account displays = %#v", view)
	}
	if view.LockedDisplay != "4.5" || view.VaultDisplay != "106.5" {
		t.Fatalf("vault totals = locked %q vault %q, want 4.5 and 106.5", view.LockedDisplay, view.VaultDisplay)
	}
	detail := view.Details[0]
	if detail.AvailableDisplay != "100" || detail.VaultDisplay != "106.5" {
		t.Fatalf("vault detail = %#v, want same USDT totals", detail)
	}
}

func TestDealerVaultBalanceViewsIncludesRegistryAssetsWithoutLedgerRows(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewTRC20(constants.TRON, "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", "USDT", "Tether USD", 6))

	views := dealerVaultBalanceViews(nil, registry)
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	view := views[0]
	if view.Symbol != "USDT" {
		t.Fatalf("view asset = %#v, want USDT group", view)
	}
	if len(view.Details) != 1 || view.Details[0].Chain != "TRON" || view.Details[0].Symbol != "USDT" {
		t.Fatalf("view details = %#v, want TRON USDT", view.Details)
	}
	for name, got := range map[string]string{
		"vault":      view.VaultDisplay,
		"available":  view.AvailableDisplay,
		"pending":    view.PendingDisplay,
		"withdrawal": view.WithdrawalDisplay,
		"refund":     view.RefundDisplay,
		"sweep":      view.SweepDisplay,
	} {
		if got != "0" {
			t.Fatalf("%s display = %q, want 0", name, got)
		}
	}
}

func TestDealerVaultBalanceViewsGroupsWrappedAssetsByCanonicalSymbol(t *testing.T) {
	registry := asset.NewRegistry()
	registry.RegisterAlias("WETH", "ETH")
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	registry.Register(asset.NewERC20(constants.Base, "0x4200000000000000000000000000000000000006", "WETH", "Wrapped Ether", 18))
	wethToken := "0x4200000000000000000000000000000000000006"
	rows := []repositories.LedgerBalanceRow{
		{ChainID: int64(constants.Ethereum), Symbol: "ETH", Decimals: 18, Account: models.LedgerAccountMerchantAvailable, BalanceRaw: "1000000000000000000"},
		{ChainID: int64(constants.Base), Token: &wethToken, Symbol: "WETH", Decimals: 18, Account: models.LedgerAccountMerchantAvailable, BalanceRaw: "2500000000000000000"},
	}

	views := dealerVaultBalanceViews(rows, registry)
	if len(views) != 1 {
		t.Fatalf("views = %d, want one ETH group", len(views))
	}
	view := views[0]
	if view.Symbol != "ETH" {
		t.Fatalf("group symbol = %q, want ETH", view.Symbol)
	}
	if view.VaultDisplay != "3.5" || view.AvailableDisplay != "3.5" {
		t.Fatalf("group totals = vault %q available %q, want 3.5", view.VaultDisplay, view.AvailableDisplay)
	}
	if view.NetworkCount != 2 || view.VariantCount != 2 || len(view.Details) != 2 {
		t.Fatalf("group counts/details = networks %d variants %d details %#v", view.NetworkCount, view.VariantCount, view.Details)
	}
	if !strings.Contains(view.SearchText, "WETH") || !strings.Contains(view.SearchText, "Base") {
		t.Fatalf("search text = %q, want wrapped asset detail terms", view.SearchText)
	}
}

func TestDealerTreasuryBalanceGroupsAggregatesSameCoinAcrossNetworks(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewERC20(constants.Ethereum, "0x0000000000000000000000000000000000000001", "USDC", "USD Coin", 6))
	registry.Register(asset.NewERC20(constants.Base, "0x0000000000000000000000000000000000000002", "USDC", "USD Coin", 6))

	groups := dealerTreasuryBalanceGroups([]DealerBalanceView{
		{Chain: "Ethereum", Symbol: "USDC", Token: "0x0000000000000000000000000000000000000001", AmountRaw: "12250000", AmountDisplay: "12.25", Decimals: 6, DisplayToken: "0x0000000000000000000000000000000000000001"},
		{Chain: "Base", Symbol: "USDC", Token: "0x0000000000000000000000000000000000000002", AmountRaw: "7750000", AmountDisplay: "7.75", Decimals: 6, DisplayToken: "0x0000000000000000000000000000000000000002"},
	}, registry)

	if len(groups) != 1 {
		t.Fatalf("groups = %d, want one USDC group", len(groups))
	}
	group := groups[0]
	if group.Symbol != "USDC" || group.AvailableDisplay != "20" || group.VaultDisplay != "20" {
		t.Fatalf("group = %#v, want USDC total 20", group)
	}
	if group.NetworkCount != 2 || group.VariantCount != 1 || len(group.Details) != 2 {
		t.Fatalf("group counts/details = networks %d variants %d details %#v", group.NetworkCount, group.VariantCount, group.Details)
	}
	if !strings.Contains(group.SearchText, "Ethereum") || !strings.Contains(group.SearchText, "Base") {
		t.Fatalf("search text = %q, want network terms", group.SearchText)
	}
}

func TestTokenAmountSortValueNormalizesDecimals(t *testing.T) {
	usdt100 := tokenAmountSortValue("100000000", 6)
	eth1 := tokenAmountSortValue("1000000000000000000", 18)
	if compareBigIntStrings(t, usdt100, eth1) <= 0 {
		t.Fatalf("sort value for 100 USDT = %s, want greater than 1 ETH sort value %s", usdt100, eth1)
	}
	if got := tokenAmountSortValue("1", 18); got != "1000000000000000000" {
		t.Fatalf("sort value for 1 raw wei = %s, want 1000000000000000000", got)
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

func compareBigIntStrings(t *testing.T, left, right string) int {
	t.Helper()
	leftValue, ok := new(big.Int).SetString(left, 10)
	if !ok {
		t.Fatalf("invalid integer %q", left)
	}
	rightValue, ok := new(big.Int).SetString(right, 10)
	if !ok {
		t.Fatalf("invalid integer %q", right)
	}
	return leftValue.Cmp(rightValue)
}
