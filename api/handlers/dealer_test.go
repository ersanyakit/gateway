package handlers

import (
	"context"
	"encoding/json"
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
	fiberhtml "github.com/gofiber/template/html/v3"
	"github.com/google/uuid"
)

func TestPaginationURLPreservesExistingQuery(t *testing.T) {
	got := paginationURL("/admin/deposits?from=0xabc&hash=0xdef", 2, 50)
	want := "/admin/deposits?from=0xabc&hash=0xdef&page=2&limit=50"
	if got != want {
		t.Fatalf("paginationURL() = %q, want %q", got, want)
	}
}

func TestCanonicalHiddenChainsNormalizesAliasesAndDeduplicates(t *testing.T) {
	got, err := canonicalHiddenChains("Ethereum, bsc, ETH, TRON Nile Testnet")
	if err != nil {
		t.Fatal(err)
	}
	if want := "ethereum,tron-testnet,bnbchain"; got != want {
		t.Fatalf("canonicalHiddenChains() = %q, want %q", got, want)
	}
	if _, err := canonicalHiddenChains("unknown-network"); err == nil {
		t.Fatal("canonicalHiddenChains accepted an unknown network")
	}
}

func TestDealerSettingsNetworkViewsSeparatePolicyAndExplicitHidden(t *testing.T) {
	visible, hidden := dealerSettingsNetworkViews(true, "ethereum")
	if len(visible)+len(hidden) != len(constants.AllChainIDs()) {
		t.Fatalf("settings networks = %d, want %d", len(visible)+len(hidden), len(constants.AllChainIDs()))
	}
	states := make(map[string]DealerSettingsNetworkView)
	for _, view := range append(append([]DealerSettingsNetworkView{}, visible...), hidden...) {
		states[view.Key] = view
	}
	if !states["ethereum"].ExplicitHidden || states["ethereum"].PolicyHidden {
		t.Fatalf("ethereum state = %+v, want explicit hidden only", states["ethereum"])
	}
	for _, key := range []string{"chiliz-spicy", "tron-testnet"} {
		if !states[key].Testnet || !states[key].PolicyHidden || states[key].ExplicitHidden {
			t.Fatalf("%s state = %+v, want policy-hidden testnet", key, states[key])
		}
	}
}

func TestBaseURLUsesConfiguredPublicURLAndSanitizesRequestHeaders(t *testing.T) {
	for _, key := range []string{"PUBLIC_BASE_URL", "GATEWAY_BASE_URL", "APP_BASE_URL"} {
		t.Setenv(key, "")
	}

	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		return c.SendString(baseURL(c))
	})

	t.Setenv("PUBLIC_BASE_URL", "https://pay.example.com/base/path")
	req := httptest.NewRequest(http.MethodGet, "http://evil.test/", nil)
	req.Host = "evil.test"
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "https://pay.example.com" {
		t.Fatalf("configured baseURL = %q, want https://pay.example.com", got)
	}

	t.Setenv("PUBLIC_BASE_URL", "")
	req = httptest.NewRequest(http.MethodGet, "http://safe.example/", nil)
	req.Header.Set("X-Forwarded-Proto", "javascript")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "http://safe.example" {
		t.Fatalf("invalid proto baseURL = %q, want http://safe.example", got)
	}

	if got := sanitizedRequestHost("evil.example/path"); got != "localhost" {
		t.Fatalf("poisoned host sanitized to %q, want localhost", got)
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
		{"/merchant/dashboard/activity", "deposits"},
		{"/merchant/dashboard/activity/payments", "payments"},
		{"/merchant/dashboard/activity/deposits", "deposits"},
		{"/merchant/dashboard/activity/audit", "audit"},
		{"/merchant/dashboard/activity?tab=payments", "payments"},
		{"/merchant/dashboard/activity?tab=audit", "audit"},
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

func TestMerchantDashboardDefaultPanelIsOverview(t *testing.T) {
	app := fiber.New()
	app.Get("/merchant/dashboard", func(c fiber.Ctx) error {
		return c.SendString(currentDashboardPanel(c))
	})
	app.Get("/merchant/dashboard/:section", func(c fiber.Ctx) error {
		return c.SendString(currentDashboardPanel(c))
	})

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/merchant/dashboard", "overview"},
		{"/merchant/dashboard/overview", "overview"},
		{"/merchant/dashboard/treasury", "treasury"},
	} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
		if err != nil {
			t.Fatalf("%s request failed: %v", tc.path, err)
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("%s body read failed: %v", tc.path, err)
		}
		if got := string(bodyBytes); got != tc.want {
			t.Fatalf("%s panel = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestMerchantRefundsRouteRedirectIsExplicit(t *testing.T) {
	app := fiber.New()
	app.Get("/merchant/dashboard/:section", func(c fiber.Ctx) error {
		if !dealerRefundsRouteRequested(c) {
			return c.Status(fiber.StatusInternalServerError).SendString("refund route not detected")
		}
		return redirectDealerRefundsRoute(c)
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/merchant/dashboard/refunds", nil))
	if err != nil {
		t.Fatalf("refund route request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusSeeOther {
		t.Fatalf("refund route status = %d, want %d", resp.StatusCode, fiber.StatusSeeOther)
	}
	if got := resp.Header.Get("Location"); got != merchantDashboardRefundsRedirectURL {
		t.Fatalf("refund route location = %q, want %q", got, merchantDashboardRefundsRedirectURL)
	}
	if !strings.Contains(resp.Header.Get("Set-Cookie"), flashErrorCookie) {
		t.Fatal("refund route redirect must set explicit flash error context")
	}
}

func TestMerchantRefundsRouteDecisionSourceContract(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	body := extractHandlerFunctionBody(t, source, "HandleDealerDashboard")
	redirectIndex := strings.Index(body, "dealerRefundsRouteRequested(c)")
	panelIndex := strings.Index(body, "activePanel := currentDashboardPanel(c)")
	if redirectIndex < 0 {
		t.Fatal("dealer dashboard must detect /merchant/dashboard/refunds explicitly")
	}
	if panelIndex < 0 {
		t.Fatal("dealer dashboard active panel assignment missing")
	}
	if redirectIndex > panelIndex {
		t.Fatal("refund route decision must happen before dashboard panel fallback")
	}
	for _, token := range []string{
		"merchantDashboardRefundsRedirectURL",
		"Merchant refund paneli henüz ayrı bir yüzey değil",
		"merchantDashboardActivityPaymentsURL + \"?status=paid\"",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("refund route decision missing token %q", token)
		}
	}
}

func TestDealerPublicAuthRoutesRenderLaunchPath(t *testing.T) {
	app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
	app.Get("/", HandleDealerHome())
	app.Get("/merchant/login", HandleDealerLogin())
	app.Get("/merchant/register", HandleDealerRegister())
	app.Get("/merchant/onboarding", HandleDealerOnboarding())

	for _, tc := range []struct {
		name  string
		path  string
		wants []string
	}{
		{
			name: "public home",
			path: "/",
			wants: []string{
				"Gateway Checkout",
				"Create payment link",
				`href="/merchant/register"`,
				`href="/merchant/login"`,
				`href="/swagger/index.html"`,
			},
		},
		{
			name: "merchant login",
			path: "/merchant/login",
			wants: []string{
				"Üye işyeri girişi",
				`href="/auth/oidc/login"`,
				`href="/merchant/register"`,
			},
		},
		{
			name: "merchant register",
			path: "/merchant/register",
			wants: []string{
				"Üye işyeri kaydı",
				`action="/merchant/register"`,
				`href="/merchant/login"`,
			},
		},
		{
			name: "merchant onboarding",
			path: "/merchant/onboarding?merchant_id=mid-1&name=Acme&email=dev@example.com",
			wants: []string{
				"Kayıt tamamlandı",
				"mid-1",
				"Acme",
				"dev@example.com",
				`href="/merchant/dashboard"`,
			},
		},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s request failed: %v", tc.name, err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("%s status = %d, want %d", tc.name, resp.StatusCode, fiber.StatusOK)
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("%s body read failed: %v", tc.name, err)
		}
		body := string(bodyBytes)
		for _, want := range tc.wants {
			if !strings.Contains(body, want) {
				t.Fatalf("%s body missing %q", tc.name, want)
			}
		}
		assertNoDealerPublicAuthSecretLeak(t, tc.name, body)
	}
}

func TestDealerOIDCMissingConfigRendersRecoveryWithoutSecrets(t *testing.T) {
	t.Setenv("OIDC_AUTHORITY", "")
	t.Setenv("OIDC_AUTH_URL", "")
	t.Setenv("OIDC_ISSUER_URL", "")
	t.Setenv("OIDC_CLIENT_ID", "")
	t.Setenv("OIDC_REDIRECT_URI", "")
	t.Setenv("OIDC_CLIENT_SECRET", "super-secret-client-value")

	app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
	app.Get("/auth/oidc/login", HandleOIDCLogin())

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/auth/oidc/login", nil))
	if err != nil {
		t.Fatalf("oidc missing request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusNotImplemented {
		t.Fatalf("oidc missing status = %d, want %d", resp.StatusCode, fiber.StatusNotImplemented)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	for _, want := range []string{
		"OIDC ayarı eksik",
		"OIDC_AUTHORITY/OIDC_AUTH_URL",
		"OIDC_CLIENT_ID",
		"OIDC_REDIRECT_URI",
		`href="/merchant/login"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("oidc missing body missing %q", want)
		}
	}
	assertNoDealerPublicAuthSecretLeak(t, "oidc missing", body)
	if strings.Contains(body, "super-secret-client-value") {
		t.Fatal("oidc missing page leaked OIDC client secret value")
	}
}

func TestOIDCEmailVerifiedClaimIsRequired(t *testing.T) {
	tests := []struct {
		name                string
		payload             string
		wantDecodeError     bool
		wantValidationError string
	}{
		{
			name:                "verified false",
			payload:             `{"email":"dealer@example.com","email_verified":false}`,
			wantValidationError: "OIDC email adresi doğrulanmamış",
		},
		{
			name:                "verified missing",
			payload:             `{"email":"dealer@example.com"}`,
			wantValidationError: "OIDC email doğrulama bilgisi dönmedi",
		},
		{
			name:            "verified malformed",
			payload:         `{"email":"dealer@example.com","email_verified":"not-a-boolean-secret-marker"}`,
			wantDecodeError: true,
		},
		{
			name:    "verified true",
			payload: `{"email":"dealer@example.com","email_verified":true}`,
		},
		{
			name:    "verified true string",
			payload: `{"email":"dealer@example.com","email_verified":"true"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var claims oidcUserInfo
			decodeErr := json.Unmarshal([]byte(tc.payload), &claims)
			if tc.wantDecodeError {
				if decodeErr == nil {
					t.Fatal("malformed email_verified claim was accepted")
				}
				if strings.Contains(decodeErr.Error(), "not-a-boolean-secret-marker") {
					t.Fatal("claim decoder error leaked the raw claim value")
				}
				return
			}
			if decodeErr != nil {
				t.Fatalf("valid OIDC claims failed to decode: %v", decodeErr)
			}

			validationErr := requireOIDCVerifiedEmail(&claims)
			if tc.wantValidationError == "" {
				if validationErr != nil {
					t.Fatalf("verified email was rejected: %v", validationErr)
				}
				return
			}
			if validationErr == nil || validationErr.Error() != tc.wantValidationError {
				t.Fatalf("validation error = %v, want %q", validationErr, tc.wantValidationError)
			}
		})
	}
}

func TestDealerDomainSetupCredentialEvidenceContract(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	for _, token := range []string{
		"helpers.ValidateDomainHost(domainURL)",
		"dealerRotateAPISecretConfirmed(c, dealerRotateAPISecretConfirmation(domainIDStr))",
		`logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.rotate_api_secret", "failed"`,
		"dealerSigningExample(domain)",
		"IdempotencyExample:",
		"Secret active; raw value is not displayed after creation.",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("dealer domain credential source missing %q", token)
		}
	}

	serviceBytes, err := os.ReadFile("../../services/system/domain.go")
	if err != nil {
		t.Fatal(err)
	}
	service := string(serviceBytes)
	for _, token := range []string{
		"helpers.ValidateDomainHost(*params.DomainURL)",
		"helpers.ValidateDomainHost(domainURL)",
	} {
		if !strings.Contains(service, token) {
			t.Fatalf("domain service missing host validation token %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	template := string(templateBytes)
	for _, token := range []string{
		`class="merchant-section-header merchant-domains-toolbar"`,
		`class="merchant-products-tabs merchant-domains-tabs"`,
		`<details class="merchant-domain-disclosure">`,
		"Kimlik bilgileri ve teknik kanıtlar",
		"Credential state",
		"Signing evidence",
		`message = METHOD + "\n" + path_and_query + "\n" + timestamp + "\n" + raw_body`,
		"Idempotency evidence",
		`data-rotate-api-secret="{{.ID}}"`,
		`data-rotate-confirm="{{.RotateConfirm}}"`,
		`pattern="[A-Za-z0-9]`,
		"API secret raw değeri panelde saklanmaz",
	} {
		if !strings.Contains(template, token) {
			t.Fatalf("domain credential template missing %q", token)
		}
	}
	if strings.Contains(template, "{{.APISecret}}") || strings.Contains(template, "{{.WebhookSecret}}") {
		t.Fatal("domain dashboard template must not render raw API/webhook secret fields from the model")
	}

	cssBytes, err := os.ReadFile("../../views/assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)
	for _, token := range []string{
		"Compact merchant domain operations",
		".merchant-domain-summary",
		".merchant-domain-disclosure-body",
		".merchant-domains-tabs .merchant-products-tab[aria-selected=\"true\"]",
	} {
		if !strings.Contains(css, token) {
			t.Fatalf("compact merchant domain CSS missing %q", token)
		}
	}

	jsBytes, err := os.ReadFile("../../views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBytes)
	for _, token := range []string{
		"handleRotateAPISecretClick",
		"/rotate-api-secret",
		"confirm_rotate",
		"X-Gateway-Rotate-Confirm",
		"Yeni API secret bir kez gösteriliyor",
	} {
		if !strings.Contains(js, token) {
			t.Fatalf("dashboard JS missing rotate token %q", token)
		}
	}
}

func TestMerchantDashboardFastNavigationContract(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	handler := extractHandlerFunctionBody(t, source, "HandleDealerDashboard")
	for _, token := range []string{
		`needsLedger := activePanel == "treasury" || activePanel == "withdrawals"`,
		`if activePanel == "domains" {`,
		`c.Get("X-Merchant-Navigation")`,
		`return c.Render("dealer/dashboard", data)`,
	} {
		if !strings.Contains(handler, token) {
			t.Fatalf("merchant dashboard selective render missing %q", token)
		}
	}
	if strings.Contains(handler, "enrichBalancesWithUSD") {
		t.Fatal("merchant dashboard request path must not block on external USD pricing")
	}

	jsBytes, err := os.ReadFile("../../views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBytes)
	for _, token := range []string{
		"initMerchantFastNavigation",
		"X-Merchant-Navigation",
		"current.replaceWith(incoming)",
		"window.history.pushState",
		"cacheTTL = 10000",
	} {
		if !strings.Contains(js, token) {
			t.Fatalf("merchant fast navigation JS missing %q", token)
		}
	}
}

func TestMerchantSettingsUsesAccessibleDragDropNetworkManager(t *testing.T) {
	dashboardBytes, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(dashboardBytes)
	for _, token := range []string{
		`data-chain-visibility-form`,
		`data-chain-zone="visible"`,
		`data-chain-zone="hidden"`,
		`data-chain-key="{{.Key}}"`,
		`data-chain-live role="status" aria-live="polite"`,
		`name="hidden_chains" type="hidden"`,
		`data-chain-toggle`,
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("merchant settings drag/drop template missing %q", token)
		}
	}
	if strings.Count(html, `name="hidden_chains"`) != 1 {
		t.Fatal("merchant settings must submit exactly one canonical hidden_chains field")
	}

	jsBytes, err := os.ReadFile("../../views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBytes)
	for _, token := range []string{
		"initMerchantChainVisibility",
		"data-chain-explicit-hidden",
		"event.dataTransfer.effectAllowed = 'move'",
		"Testnet politikasıyla kilitli ağ taşınamaz.",
	} {
		if !strings.Contains(js, token) {
			t.Fatalf("merchant settings drag/drop JS missing %q", token)
		}
	}

	cssBytes, err := os.ReadFile("../../views/assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)
	for _, token := range []string{"Merchant network visibility board", ".merchant-chain-board", ".merchant-chain-card", ".merchant-switch"} {
		if !strings.Contains(css, token) {
			t.Fatalf("merchant settings drag/drop CSS missing %q", token)
		}
	}
}

func TestDealerWebhookSettingsEvidencePackContract(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	for _, token := range []string{
		"dealerLatestWebhookDeliveries(c.Context(), deps, merchant.ID, domains)",
		"LatestByMerchantDomains(ctx, merchantID, domainIDs)",
		`WebhookSigningMode:  "HMAC-SHA256 over timestamp + raw_body"`,
		`WebhookCatalogURL:   "/docs/money-event-catalog.md"`,
		`WebhookDocsURL:      "/docs/integration-guide.md#webhooks"`,
		"WebhookLastStatus:",
		"WebhookLastAttempts:",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("dealer webhook evidence source missing %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	template := string(templateBytes)
	for _, token := range []string{
		"Webhook evidence",
		"{{.WebhookSigningMode}}",
		"Last delivery: {{.WebhookLastStatus}}",
		`href="{{.WebhookCatalogURL}}"`,
		"Event catalog",
		`href="{{.WebhookDocsURL}}"`,
		"Webhook docs",
		`data-test-webhook="{{.ID}}"`,
	} {
		if !strings.Contains(template, token) {
			t.Fatalf("dealer webhook evidence template missing %q", token)
		}
	}

	routes := readHandlerSource(t, "../routes/routes.go")
	for _, token := range []string{
		`r.fiber.Get("/docs/integration-guide.md", handlers.HandleIntegrationGuide())`,
		`r.fiber.Get("/docs/money-event-catalog.md", handlers.HandleMoneyEventCatalog())`,
	} {
		if !strings.Contains(routes, token) {
			t.Fatalf("developer docs route missing %q", token)
		}
	}
}

func assertNoDealerPublicAuthSecretLeak(t *testing.T, name string, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, forbidden := range []string{
		"client_secret",
		"api_secret",
		"webhook_secret",
		"private_key",
		"mnemonic",
		"raw_signature",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("%s body leaked forbidden token %q", name, forbidden)
		}
	}
}

func TestMerchantActivityDefaultsToBlockchainDepositsFirst(t *testing.T) {
	dashboard, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(dashboard)
	depositTab := strings.Index(html, `data-products-tab="deposits">Blockchain deposits`)
	paymentTab := strings.Index(html, `data-products-tab="payments">Ödeme aktiviteleri`)
	auditTab := strings.Index(html, `data-products-tab="audit">Audit logger`)
	if depositTab < 0 || paymentTab < 0 || auditTab < 0 {
		t.Fatal("activity tabs must include deposits, payments and audit")
	}
	if !(depositTab < paymentTab && paymentTab < auditTab) {
		t.Fatal("activity tabs must order Blockchain deposits before payments and audit")
	}
	if strings.Contains(html, `aria-selected="{{if or (eq .ActivityTab "") (eq .ActivityTab "audit")}}true`) {
		t.Fatal("audit tab must not be selected for empty activity tab")
	}
}

func TestAdminDashboardTemplateParses(t *testing.T) {
	if _, err := template.ParseFiles("../../views/dealer/admin_dashboard.html"); err != nil {
		t.Fatal(err)
	}
}

func TestAdminDepositsAndWithdrawalsUsePaginatedProTables(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	adminDashboardBody := extractHandlerFunctionBody(t, source, "HandleAdminDashboard")
	for _, token := range []string{
		`page, limit := adminDashboardPageParams(c)`,
		`deps.TransactionRepo.ListPageFiltered(c.Context(), page, limit, fromFilter, toFilter, hashFilter)`,
		`data.AdminPagination = dealerPaginationView(page, limit, total, depositBase)`,
		`statusFilter := normalizeAdminWithdrawalStatusFilter(c.Query("status"))`,
		`data.AdminWithdrawalStatusFilter = statusFilter`,
		`deps.WithdrawalRepo.ListPage(c.Context(), page, limit, statusFilter)`,
		`buildAdminWithdrawalPaginationBase(statusFilter)`,
		`recentRows, total, err := deps.TransactionRepo.ListPage(c.Context(), page, limit)`,
		`data.AdminPagination = dealerPaginationView(page, limit, total, "/admin")`,
	} {
		if !strings.Contains(adminDashboardBody, token) {
			t.Fatalf("admin dashboard missing paginated table token %q", token)
		}
	}

	repoBytes, err := os.ReadFile("../../repositories/withdrawal_request_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	repo := string(repoBytes)
	for _, token := range []string{
		`func (r *WithdrawalRequestRepo) ListPage(ctx context.Context, page, limit int, status string)`,
		`status = strings.TrimSpace(status)`,
		`countQuery = countQuery.Where("status = ?", status)`,
		`query = query.Where("status = ?", status)`,
	} {
		if !strings.Contains(repo, token) {
			t.Fatalf("withdrawal repo missing status pagination token %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(templateBytes)
	for _, token := range []string{
		`id="admin-deposits-table" data-admin-table`,
		`data-admin-table-search="admin-deposits-table"`,
		`data-admin-table-count="admin-deposits-table" data-total="{{.AdminPagination.Total}}"`,
		`name="limit"`,
		`data-admin-table-row data-admin-table-key="{{.ID}}" data-search="{{.SearchText}}"`,
		`id="deposit-{{.ID}}-details" data-admin-table-detail-for="{{.ID}}"`,
		`id="admin-overview-deposits-table" data-admin-table`,
		`data-admin-table-search="admin-overview-deposits-table"`,
		`data-admin-table-count="admin-overview-deposits-table" data-total="{{.AdminPagination.Total}}"`,
		`id="overview-deposit-{{.ID}}-details" data-admin-table-detail-for="{{.ID}}"`,
		`{{if .ExplorerURL}}<a href="{{.ExplorerURL}}" target="_blank" rel="noopener" title="{{.Hash}}">{{.HashShort}}</a>`,
		`id="admin-withdrawals-table" data-admin-table`,
		`data-admin-table-search="admin-withdrawals-table"`,
		`data-admin-table-count="admin-withdrawals-table" data-total="{{.AdminPagination.Total}}"`,
		`name="status"`,
		`id="withdrawal-{{.ID}}-details" data-admin-table-detail-for="{{.ID}}"`,
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("admin dashboard template missing pro table token %q", token)
		}
	}

	dashboardJS, err := os.ReadFile("../../views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(dashboardJS)
	for _, token := range []string{
		`emptyRow.hidden = visibleCount > 0;`,
		`var serverTotal = parseInt(countEl.getAttribute('data-total') || '', 10);`,
		`label = visibleCount + ' / ' + serverTotal + ' ' + unit;`,
	} {
		if !strings.Contains(js, token) {
			t.Fatalf("dashboard.js missing pro table token %q", token)
		}
	}

	adminCSS, err := os.ReadFile("../../views/assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(adminCSS)
	for _, token := range []string{
		`.admin-deposits-table`,
		`.admin-deposit-detail`,
		`.admin-withdrawals-table`,
		`.admin-withdrawal-detail`,
	} {
		if !strings.Contains(css, token) {
			t.Fatalf("admin css missing pro table token %q", token)
		}
	}
}

func TestAdminMerchantDetailProvidesDomainsWalletsPayments(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	body := extractHandlerFunctionBody(t, source, "HandleAdminMerchantDetail")
	for _, token := range []string{
		`deps.MerchantService.Repo().FindAnyByID(c.Context(), merchantID)`,
		`data.AdminMerchantDetailTab = adminMerchantDetailTab(c)`,
		`deps.DomainService.ListByMerchant(c.Context(), merchantID)`,
		`deps.WalletRepo.ListByMerchantPage(c.Context(), merchantID, limit, (page-1)*limit)`,
		`buildWalletBalanceMap(c.Context(), deps.LedgerRepo, rows, deps.AssetRegistry)`,
		`normalizeAdminPaymentStatusFilter(c.Query("status"))`,
		`deps.PaymentRepo.ListByMerchantPage(c.Context(), merchantID, statusFilter, page, limit)`,
		`dealerPaymentViews(c, rows)`,
		`adminMerchantDetailPaginationBase(merchantID, "payments", statusFilter)`,
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("admin merchant detail handler missing token %q", token)
		}
	}

	routes := readHandlerSource(t, "../routes/routes.go")
	detailRoute := `r.fiber.Get("/admin/merchants/:id", handlers.HandleAdminMerchantDetail(dealerDeps))`
	catchAllRoute := `r.fiber.Get("/admin/:section", handlers.HandleAdminDashboard(dealerDeps))`
	if !strings.Contains(routes, detailRoute) {
		t.Fatalf("routes missing merchant detail route %q", detailRoute)
	}
	if strings.Index(routes, detailRoute) > strings.Index(routes, catchAllRoute) {
		t.Fatal("merchant detail route must be registered before admin catch-all")
	}

	merchantRepo, err := os.ReadFile("../../repositories/merchant_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(merchantRepo), `func (r *MerchantRepo) FindAnyByID(ctx context.Context, id uuid.UUID)`) {
		t.Fatal("merchant repo missing admin detail lookup")
	}

	paymentRepo, err := os.ReadFile("../../repositories/payment_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	paymentSource := string(paymentRepo)
	for _, token := range []string{
		`func (r *PaymentRepo) ListByMerchantPage(ctx context.Context, merchantID uuid.UUID, status string, page, limit int)`,
		`Preload("Merchant")`,
		`Preload("Domain")`,
	} {
		if !strings.Contains(paymentSource, token) {
			t.Fatalf("payment merchant page query missing %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(templateBytes)
	for _, token := range []string{
		`{{if eq .AdminPanel "merchant-detail"}}`,
		`href="/admin/merchants/{{.ID}}"`,
		`id="admin-merchants-table" data-admin-table`,
		`data-admin-table-search="admin-merchant-domains-table"`,
		`id="admin-merchant-domains-table" data-admin-table`,
		`data-admin-table-search="admin-merchant-wallets-table"`,
		`id="admin-merchant-wallets-table" data-admin-table`,
		`data-admin-table-search="admin-merchant-payments-table"`,
		`id="admin-merchant-payments-table" data-admin-table`,
		`{{template "pg" .AdminPagination}}`,
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("admin merchant detail template missing %q", token)
		}
	}
}

func TestAdminReadinessRouteSurfaceUsesV1ReadinessSemantics(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	dashboard := extractHandlerFunctionBody(t, source, "HandleAdminDashboard")
	for _, token := range []string{
		`case "readiness":`,
		"dealerAdminReadinessView(c.Context(), deps)",
		"data.AdminReadinessReady",
		"data.AdminReadinessRaw",
	} {
		if !strings.Contains(dashboard, token) {
			t.Fatalf("admin dashboard readiness branch missing %q", token)
		}
	}

	readiness := extractHandlerFunctionBody(t, source, "dealerAdminReadinessView")
	for _, token := range []string{
		"v1RunReadinessChecks",
		"migration.strategy",
		"signer.production",
		"metrics.access",
		"portal.jwt_secret",
		"webhook.delivery_backlog",
		"sweep.job_backlog",
		"reconciliation.drift",
		"provider.health.aggregate",
		"Controlled beta",
		"Real-funds production",
		"Wallet-provider custody",
		"Exchange-grade tracking",
	} {
		if !strings.Contains(readiness, token) {
			t.Fatalf("admin readiness view missing %q", token)
		}
	}

	routes := readHandlerSource(t, "../routes/routes.go")
	if !strings.Contains(routes, `r.fiber.Get("/admin/:section", handlers.HandleAdminDashboard(dealerDeps))`) {
		t.Fatal("admin readiness must remain reachable through admin section route")
	}
	for _, token := range []string{
		`DomainRepo:              r.DomainRepo`,
		`ProviderHealthRepo:      r.ProviderHealthRepo`,
	} {
		if !strings.Contains(routes, token) {
			t.Fatalf("admin readiness deps wiring missing %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	template := string(templateBytes)
	for _, token := range []string{
		`href="{{.AdminReadinessURL}}"`,
		`{{if eq .AdminPanel "readiness"}}`,
		"Readiness gate checklist",
		"/api/v1/common/readiness",
		"{{.BlockingLabel}}",
		"{{if .Blocking}}",
		"Last checked",
	} {
		if !strings.Contains(template, token) {
			t.Fatalf("admin readiness template missing %q", token)
		}
	}
}

func TestAdminMetricsRouteSurfaceUsesOperationalMetrics(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	dashboard := extractHandlerFunctionBody(t, source, "HandleAdminDashboard")
	for _, token := range []string{
		`case "metrics":`,
		`dealerAdminMetricsView(c.Context(), deps, strings.TrimSpace(c.Query("tab")))`,
		"data.AdminMetricsSummary",
		"data.AdminMetricsGroups",
		"data.AdminMetricAlerts",
		"data.AdminMetricTabs",
		"data.AdminMetricsActiveTab",
		"data.AdminMetricsRaw",
	} {
		if !strings.Contains(dashboard, token) {
			t.Fatalf("admin dashboard metrics branch missing %q", token)
		}
	}

	metricsView := extractHandlerFunctionBody(t, source, "dealerAdminMetricsView")
	for _, token := range []string{
		"buildOperationalMetrics",
		"OperationalMetricsDeps",
		"MoneyEventOutboxRepo:    deps.MoneyEventOutboxRepo",
		"MoneyEventInboxRepo:     deps.MoneyEventInboxRepo",
		"WorkerLeaseRepo:         deps.WorkerLeaseRepo",
		"ChainStateRepo:          deps.ChainStateRepo",
		"WalletAddressLookupRepo: deps.WalletAddressLookupRepo",
		"dealerParsePrometheusMetrics",
	} {
		if !strings.Contains(metricsView, token) {
			t.Fatalf("admin metrics view missing %q", token)
		}
	}

	routes := readHandlerSource(t, "../routes/routes.go")
	for _, token := range []string{
		`r.fiber.Get("/admin/:section", handlers.HandleAdminDashboard(dealerDeps))`,
		`MoneyEventOutboxRepo:    r.MoneyEventOutboxRepo`,
		`MoneyEventInboxRepo:     r.MoneyEventInboxRepo`,
		`WorkerLeaseRepo:         r.WorkerLeaseRepo`,
		`ChainStateRepo:          r.ChainStateRepo`,
		`WalletAddressLookupRepo: r.WalletAddressLookupRepo`,
	} {
		if !strings.Contains(routes, token) {
			t.Fatalf("admin metrics deps wiring missing %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	template := string(templateBytes)
	for _, token := range []string{
		`href="{{.AdminMetricsURL}}"`,
		`{{if eq .AdminPanel "metrics"}}`,
		"Operational metrics",
		`{{range .AdminMetricTabs}}`,
		"admin-metric-tabs",
		"Aksiyon",
		"Önce bunlara bak",
		"{{.AdminMetricsSummary.CollectionErrors}}",
		"{{range .AdminMetricAlerts}}",
		"{{range .AdminMetricsGroups}}",
		`{{if eq .AdminMetricsActiveTab "issues"}}`,
		`{{else if eq .AdminMetricsActiveTab "raw"}}`,
		"Prometheus exposition",
	} {
		if !strings.Contains(template, token) {
			t.Fatalf("admin metrics template missing %q", token)
		}
	}
}

func TestAdminProviderHealthSurfaceUsesSnapshotSourceOfTruth(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	dashboard := extractHandlerFunctionBody(t, source, "HandleAdminDashboard")
	for _, token := range []string{
		`case "provider-health":`,
		"deps.ProviderHealthRepo.ListLatest(c.Context())",
		"dealerProviderHealthViews(rows)",
		`dealerPaginationView(1, limit, int64(len(data.AdminProviderHealth)), "/admin/provider-health")`,
	} {
		if !strings.Contains(dashboard, token) {
			t.Fatalf("admin provider health dashboard missing %q", token)
		}
	}
	view := extractHandlerFunctionBody(t, source, "dealerProviderHealthViews")
	for _, token := range []string{
		"ProviderHash:",
		"StatusClass:",
		"StaleIndicator:",
		"FallbackPolicy:",
		"ReadinessEvidence:",
		"v1RedactReadinessText(row.ErrorDetail)",
	} {
		if !strings.Contains(view, token) {
			t.Fatalf("admin provider health view missing %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	template := string(templateBytes)
	for _, token := range []string{
		`href="{{.AdminProviderHealthURL}}"`,
		`{{if eq .AdminPanel "provider-health"}}`,
		"Provider health",
		"provider_hash {{.ProviderHash}}",
		"{{.StaleIndicator}}",
		"{{.FallbackPolicy}}",
		"readiness evidence",
		"Provider health snapshot yok.",
	} {
		if !strings.Contains(template, token) {
			t.Fatalf("admin provider health template missing %q", token)
		}
	}
}

func TestAdminNetworkOperationalStateSurfaceUsesDatabaseSourceOfTruth(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	dashboard := extractHandlerFunctionBody(t, source, "HandleAdminDashboard")
	for _, token := range []string{
		`case "networks":`,
		`deps.NetworkOperationalStateRepo.ListAll(c.Context())`,
		`data.AdminNetworkStates = dealerNetworkOperationalStateViews(rows)`,
	} {
		if !strings.Contains(dashboard, token) {
			t.Fatalf("admin network dashboard missing %q", token)
		}
	}

	update := extractHandlerFunctionBody(t, source, "HandleAdminNetworkOperationalStateUpdate")
	for _, token := range []string{
		`requirePrivilegedAdmin(c, deps.AdminRepo)`,
		`strings.TrimSpace(c.FormValue("chain_id"))`,
		`constants.IsSupportedChainID(chainID)`,
		`models.NormalizeNetworkOperationalMode`,
		`models.IsValidNetworkOperationalMode(mode)`,
		`len([]rune(reason)) > 500`,
		`mode != models.NetworkOperationalModeActive && reason == ""`,
		`candidate.Validate()`,
		`deps.NetworkOperationalStateRepo.GetByChain(c.Context(), chainID)`,
		`deps.NetworkOperationalStateRepo.Upsert`,
		`repositories.NetworkOperationalStateUpdate{`,
		`logDealerDecisionActivity`,
		`"network_operational_state.update"`,
	} {
		if !strings.Contains(update, token) {
			t.Fatalf("admin network update handler missing %q", token)
		}
	}
	guardIndex := strings.Index(update, `requirePrivilegedAdmin(c, deps.AdminRepo)`)
	parseIndex := strings.Index(update, `strconv.ParseInt(chainIDRaw, 10, 64)`)
	if guardIndex < 0 || parseIndex < 0 || guardIndex > parseIndex {
		t.Fatal("admin network update must check privileged role before parsing the target network")
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(templateBytes)
	for _, token := range []string{
		`href="{{.AdminNetworksURL}}"`,
		`{{if eq .AdminPanel "networks"}}`,
		`{{range .AdminNetworkStates}}`,
		`action="/admin/networks/state"`,
		`name="chain_id"`,
		`name="mode"`,
		`value="deposits_off"`,
		`value="withdrawals_off"`,
		`value="maintenance"`,
		`name="reason" maxlength="500"`,
		`{{.UpdatedBy}}`,
		`{{.UpdatedAt}}`,
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("admin network template missing %q", token)
		}
	}
}

func TestDealerNetworkOperationalStateViewsExposeFlowSemantics(t *testing.T) {
	updatedAt := time.Date(2026, time.July, 13, 9, 30, 0, 0, time.UTC)
	views := dealerNetworkOperationalStateViews([]models.NetworkOperationalState{
		{
			ChainID:   constants.Ethereum,
			Mode:      models.NetworkOperationalModeMaintenance,
			Reason:    "  RPC bakımı  ",
			UpdatedBy: "security@example.com",
			UpdatedAt: updatedAt,
		},
		{
			ChainID: constants.TRONTestnet,
			Mode:    models.NetworkOperationalModeDepositsOff,
		},
	})
	if len(views) != 2 {
		t.Fatalf("network state views = %d, want 2", len(views))
	}
	maintenance := views[0]
	if maintenance.Chain != "Ethereum" || maintenance.ChainID != "1" || maintenance.ChainSlug != "ethereum" {
		t.Fatalf("maintenance chain identity = %+v", maintenance)
	}
	if maintenance.Mode != "maintenance" || maintenance.ModeLabel != "Bakım" || maintenance.ModeClass != "badge-red" {
		t.Fatalf("maintenance presentation = %+v", maintenance)
	}
	if !maintenance.BlocksDeposits || !maintenance.BlocksWithdrawals {
		t.Fatalf("maintenance flow flags = %+v", maintenance)
	}
	if maintenance.Reason != "RPC bakımı" || maintenance.UpdatedBy != "security@example.com" || maintenance.UpdatedAt == "-" {
		t.Fatalf("maintenance metadata = %+v", maintenance)
	}

	depositsOff := views[1]
	if !depositsOff.BlocksDeposits || depositsOff.BlocksWithdrawals {
		t.Fatalf("deposits_off flow flags = %+v", depositsOff)
	}
	if !depositsOff.Testnet || depositsOff.UpdatedBy != "-" || depositsOff.UpdatedAt != "-" {
		t.Fatalf("testnet/default metadata = %+v", depositsOff)
	}
}

func TestDealerPreselectedDepositFlowsCheckNetworkModeBeforeMutation(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	tests := []struct {
		function string
		mutation string
	}{
		{function: "parseDealerProductForm", mutation: "form.defaultChainID = &chainID"},
		{function: "HandleDealerInvoiceCreate", mutation: "deps.WalletRepo.Create"},
		{function: "HandlePaymentLink", mutation: "deps.WalletRepo.Create"},
	}
	for _, tc := range tests {
		body := extractHandlerFunctionBody(t, source, tc.function)
		guard := strings.Index(body, "networkops.RequireDeposits")
		mutation := strings.Index(body, tc.mutation)
		if guard < 0 {
			t.Fatalf("%s missing persisted network deposit guard", tc.function)
		}
		if mutation < 0 {
			t.Fatalf("%s missing mutation token %q", tc.function, tc.mutation)
		}
		if guard > mutation {
			t.Fatalf("%s must check network mode before %q", tc.function, tc.mutation)
		}
	}
}

func TestDealerMetricsParserClassifiesOperationalRisk(t *testing.T) {
	raw := strings.Join([]string{
		"# HELP gateway_metrics_collection_error Metrics collection errors by collector.",
		"# TYPE gateway_metrics_collection_error gauge",
		`gateway_metrics_collection_error{collector="gateway_chain_state",reason="query_failed"} 1`,
		"# HELP gateway_provider_health Provider health status gauge.",
		"# TYPE gateway_provider_health gauge",
		`gateway_provider_health{chain="ethereum",reason="primary selected",status="degraded"} 0.5`,
		"# HELP gateway_webhook_delivery_backlog Webhook delivery rows by retry-relevant status.",
		"# TYPE gateway_webhook_delivery_backlog gauge",
		`gateway_webhook_delivery_backlog{status="dead_letter"} 2`,
		"# HELP gateway_build_info Gateway process info.",
		"# TYPE gateway_build_info gauge",
		`gateway_build_info{version="test"} 1`,
	}, "\n")

	series := dealerParsePrometheusMetrics(raw)
	if len(series) != 4 {
		t.Fatalf("series count = %d, want 4", len(series))
	}
	statusByName := map[string]string{}
	for _, item := range series {
		statusByName[item.name] = item.status
	}
	if statusByName["gateway_metrics_collection_error"] != "critical" {
		t.Fatalf("collection error status = %q", statusByName["gateway_metrics_collection_error"])
	}
	if statusByName["gateway_provider_health"] != "degraded" {
		t.Fatalf("provider health status = %q", statusByName["gateway_provider_health"])
	}
	if statusByName["gateway_webhook_delivery_backlog"] != "dead_letter" {
		t.Fatalf("dead letter backlog status = %q", statusByName["gateway_webhook_delivery_backlog"])
	}
	if statusByName["gateway_build_info"] != "ok" {
		t.Fatalf("build info status = %q", statusByName["gateway_build_info"])
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
	adminCSSBytes, err := os.ReadFile("../../views/assets/admin.css")
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
	adminCSS := string(adminCSSBytes)
	for _, required := range []string{
		".merchant-wallet-wrap {\n  overflow-x: hidden;",
		".merchant-users-table {\n  width: 100%;\n  min-width: 0;\n  table-layout: fixed;",
		".merchant-wallet-detail .admin-vault-detail-table {\n  width: 100%;\n  min-width: 0;\n  table-layout: fixed;",
	} {
		if !strings.Contains(adminCSS, required) {
			t.Fatalf("merchant users CSS must prevent horizontal table drift; missing %q", required)
		}
	}
}

func TestMerchantTransactionsPanelUsesServerPaginationAndCompactColumns(t *testing.T) {
	sourceBytes, err := os.ReadFile("dealer.go")
	if err != nil {
		t.Fatal(err)
	}
	dashboard, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	adminCSS, err := os.ReadFile("../../views/assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}

	handler := extractHandlerFunctionBody(t, string(sourceBytes), "HandleDealerDashboard")
	for _, token := range []string{
		`loadsTransactionRows := activePanel == "transactions" || (activePanel == "activity" && activityTab == "deposits")`,
		`transactionPaginationBase = merchantDashboardTransactionsURL`,
		`deps.TransactionRepo.ListByMerchantPage(c.Context(), merchant.ID, depositPage, depositLimit)`,
		`data.TransactionPage = dealerPaginationView(depositPage, depositLimit, depositTotal, transactionPaginationBase)`,
	} {
		if !strings.Contains(handler, token) {
			t.Fatalf("merchant transactions handler missing pagination token %q", token)
		}
	}

	html := string(dashboard)
	for _, token := range []string{
		"Blockchain",
		"Asset",
		"Amount",
		"Customer",
		"Webhook",
		"Status",
		"Action",
		`{{.TransactionPage.From}}–{{.TransactionPage.To}} / {{.TransactionPage.Total}} tx`,
		`{{range .TransactionPage.PageURLs}}`,
		`colspan="7"`,
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("merchant transactions template missing compact table token %q", token)
		}
	}
	if strings.Contains(html, `data-admin-table-count="merchant-transactions-table"`) {
		t.Fatal("merchant transactions total must come from server pagination, not page-local table count")
	}

	css := string(adminCSS)
	for _, token := range []string{
		".merchant-transactions-table {\n  min-width: 920px;",
		".merchant-transaction-asset-compact",
		".merchant-transaction-chain-inline-logo",
		".merchant-transactions-table .merchant-row-button",
	} {
		if !strings.Contains(css, token) {
			t.Fatalf("merchant transactions CSS missing compact token %q", token)
		}
	}
}

func TestMerchantActivityPanelUsesServerPaginationInsteadOfClientDatatables(t *testing.T) {
	dashboard, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	adminCSS, err := os.ReadFile("../../views/assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}

	html := string(dashboard)
	for _, token := range []string{
		`{{.AuditPage.From}}–{{.AuditPage.To}} / {{.AuditPage.Total}} audit`,
		`{{.ActivityPaymentPage.From}}–{{.ActivityPaymentPage.To}} / {{.ActivityPaymentPage.Total}} session`,
		`{{.ActivityDepositPage.From}}–{{.ActivityDepositPage.To}} / {{.ActivityDepositPage.Total}} deposit`,
		`{{range .AuditPage.PageURLs}}`,
		`{{range .ActivityPaymentPage.PageURLs}}`,
		`{{range .ActivityDepositPage.PageURLs}}`,
		"Yeni kayıtlar önce",
		"Yeni depositler önce",
		"<th>Blockchain</th>",
		`colspan="7"`,
		`?status=underpaid`,
		`?status=overpaid`,
		`?status=partial_paid`,
		"Status copy: awaiting_payment",
		`<span class="merchant-status-pill {{.StatusClass}}" title="{{.Status}}">{{.StatusLabel}}</span>`,
		"<th>Webhook</th>",
		"<th>Status</th>",
		"<th>Request</th>",
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("merchant activity template missing server pagination token %q", token)
		}
	}
	for _, forbidden := range []string{
		`data-admin-table-search="merchant-audit-table"`,
		`data-admin-table-search="merchant-activity-payments-table"`,
		`data-admin-table-search="merchant-activity-deposits-table"`,
		`id="merchant-audit-table" data-admin-table`,
		`id="merchant-activity-payments-table" data-admin-table`,
		`id="merchant-activity-deposits-table" data-admin-table`,
		`data-admin-table-count="merchant-audit-table"`,
		`data-admin-table-count="merchant-activity-payments-table"`,
		`data-admin-table-count="merchant-activity-deposits-table"`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("merchant activity must not use page-local datatable token %q", forbidden)
		}
	}

	css := string(adminCSS)
	for _, token := range []string{
		".merchant-activity-server-toolbar",
		".merchant-activity-order-note",
		".merchant-activity-deposits-table th:nth-child(7)",
	} {
		if !strings.Contains(css, token) {
			t.Fatalf("merchant activity CSS missing compact token %q", token)
		}
	}
}

func TestPaymentStatusPresentationCoversMerchantEvidenceStates(t *testing.T) {
	for _, tc := range []struct {
		status string
		label  string
		class  string
	}{
		{models.PaymentStatusAwaitingPayment, "Ödeme bekliyor", "is-info"},
		{models.PaymentStatusPending, "Asset bekliyor", "is-warning"},
		{models.PaymentStatusPaid, "Ödendi", "is-success"},
		{models.PaymentStatusExpired, "Süre doldu", "is-danger"},
		{models.PaymentStatusFailed, "Hatalı", "is-danger"},
		{models.PaymentStatusUnderpaid, "Eksik ödeme", "is-warning"},
		{models.PaymentStatusOverpaid, "Fazla ödeme", "is-warning"},
		{models.PaymentStatusPartialPaid, "Kısmi ödeme", "is-warning"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			label, class := paymentStatusPresentation(tc.status)
			if label != tc.label || class != tc.class {
				t.Fatalf("paymentStatusPresentation(%q) = (%q, %q), want (%q, %q)", tc.status, label, class, tc.label, tc.class)
			}
		})
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
		"Ağ görünürlüğü",
	} {
		if !strings.Contains(html, "<h2>"+title+"</h2>") {
			t.Fatalf("merchant dashboard missing panel title %q", title)
		}
	}
	if count := strings.Count(html, "merchant-section-header"); count < 10 {
		t.Fatalf("merchant dashboard section header count = %d, want at least 10", count)
	}
}

func TestMerchantDashboardOverviewMoneyStateEvidenceContract(t *testing.T) {
	dashboard, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	adminCSS, err := os.ReadFile("../../views/assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	html := string(dashboard)
	for _, token := range []string{
		`{{if or (eq .ActivePanel "") (eq .ActivePanel "overview")}}`,
		`class="merchant-dashboard-overview grid"`,
		`href="{{.DashboardURL}}"{{if or (eq .ActivePanel "") (eq .ActivePanel "overview")}} aria-current="page"{{end}}`,
		`class="merchant-sidebar-intro"`,
		`class="merchant-nav-sections"`,
		`class="merchant-nav-group merchant-nav-group-utility"`,
		`class="merchant-dashboard-header"`,
		`class="merchant-overview-links"`,
		`href="{{.ActivityDepositsURL}}"`,
		`href="{{.ActivityPaymentsURL}}"`,
		`href="{{.UsersURL}}"`,
		`href="{{.DomainsPanelURL}}"`,
		`{{.DepositCount}}`,
		`{{.PaymentCount}}`,
		`{{.WalletCount}}`,
		`{{.DomainCount}}`,
		"Ledger bakiyesi yok. Domain kurup test checkout veya static deposit oluşturduğunda ledger evidence burada görünür.",
		`merchant-activity-mono merchant-transaction-hash-link`,
		`class="merchant-wallet-address"`,
		`data-copy-value="{{.Hash}}"`,
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("merchant dashboard overview contract missing %q", token)
		}
	}
	css := string(adminCSS)
	for _, token := range []string{
		".merchant-dashboard-overview",
		".merchant-dashboard-tabs",
		"Merchant navigation rail and data-grid refinement",
		"grid-template-columns: 228px minmax(0, 1fr)",
		".adm-wrap.kewl-grid > table.kewl-grid-table > thead > tr > th",
		"grid-template-rows: auto minmax(0, 1fr)",
		"height: 100%",
		".merchant-overview-links",
		".merchant-overview-links a",
		".merchant-overview-links strong",
		".merchant-activity-mono",
		".merchant-wallet-address",
	} {
		if !strings.Contains(css, token) {
			t.Fatalf("merchant dashboard overview CSS missing %q", token)
		}
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
		`confirmReplay != "replay:"+id.String()`,
		"Webhook replay için confirmation gerekli.",
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

func TestAdminWebhookDiagnosticsSurfaceContract(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	view := extractHandlerFunctionBody(t, source, "dealerWebhookDeliveryViews")
	for _, token := range []string{
		"EventVersion:",
		"MerchantID:",
		"DomainID:",
		"ResourceType:",
		"ResourceID:",
		"IdempotencyKey:",
		"PayloadPreview:",
		"LatencyEvidence:",
		"webhooksvc.SanitizeDeliveryText(row.LastError)",
		`dealerPreviewText(row.PayloadJSON, "Payload preview unavailable")`,
	} {
		if !strings.Contains(view, token) {
			t.Fatalf("admin webhook diagnostics view missing %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	template := string(templateBytes)
	for _, token := range []string{
		`{{.EventType}} / {{.EventVersion}}`,
		`merchant {{.MerchantID}}`,
		`domain {{.DomainID}}`,
		`idempotency {{.IdempotencyKey}}`,
		`Payload: {{.PayloadPreview}}`,
		`{{.LatencyEvidence}}`,
		`name="confirm_replay" value="replay:{{.ID}}"`,
		"Bu filtre için webhook delivery yok.",
	} {
		if !strings.Contains(template, token) {
			t.Fatalf("admin webhook diagnostics template missing %q", token)
		}
	}
}

func TestAdminReconciliationSurfaceContract(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	dashboard := extractHandlerFunctionBody(t, source, "HandleAdminDashboard")
	for _, token := range []string{
		`case "reconciliation":`,
		"data.AdminReconciliationStatusFilter = statusFilter",
		"deps.ReconciliationRepo.ListPage(c.Context(), page, limit, statusFilter)",
		"dealerReconciliationJobViews(rows)",
		`reconciliationBase := "/admin/reconciliation"`,
	} {
		if !strings.Contains(dashboard, token) {
			t.Fatalf("admin reconciliation dashboard missing %q", token)
		}
	}
	view := extractHandlerFunctionBody(t, source, "dealerReconciliationJobViews")
	for _, token := range []string{
		"Reason:",
		"StatusClass:",
		"Severity:",
		"Owner:",
		"ChainEvidence:",
		"LedgerEvidence:",
		"LifecycleEvidence:",
		"WebhookEvidence:",
		"BroadcastEvidence:",
		"AuditTimeline:",
	} {
		if !strings.Contains(view, token) {
			t.Fatalf("admin reconciliation view missing %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	template := string(templateBytes)
	for _, token := range []string{
		`href="{{.AdminReconciliationURL}}"`,
		`{{if eq .AdminPanel "reconciliation"}}`,
		"Reconciliation jobs",
		"Chain: {{.ChainEvidence}}",
		"Ledger: {{.LedgerEvidence}}",
		"Lifecycle: {{.LifecycleEvidence}}",
		"Webhook: {{.WebhookEvidence}}",
		"Broadcast: {{.BroadcastEvidence}}",
		"Bu filtre için reconciliation job yok.",
	} {
		if !strings.Contains(template, token) {
			t.Fatalf("admin reconciliation template missing %q", token)
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

func TestAdminWalletsPanelUsesDataTable(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	for _, token := range []string{
		"AddressPreview string",
		"BalancePreview string",
		"dealerAddressPreview(addresses)",
		"dealerBalancePreview(bals)",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("admin wallet view model missing %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(templateBytes)
	for _, token := range []string{
		`data-admin-table-search="admin-wallets-table"`,
		`data-admin-table-count="admin-wallets-table" data-total="{{.AdminPagination.Total}}"`,
		`id="admin-wallets-table" data-admin-table`,
		`data-admin-table-row data-admin-table-key="wallet-{{.ID}}"`,
		`id="wallet-{{.ID}}-details" data-admin-table-detail-for="wallet-{{.ID}}"`,
		`{{template "pg" .AdminPagination}}`,
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("admin wallets template missing data table token %q", token)
		}
	}
	if strings.Contains(html, `class="admin-wallet-list"`) {
		t.Fatal("admin wallets panel must use a data table, not the card/list layout")
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
	pageParams := extractHandlerFunctionBody(t, source, "adminDashboardPageParams")
	if !strings.Contains(pageParams, "paginate.FromContext(c.Context())") {
		t.Fatal("admin dashboard pagination must read Fiber paginate PageInfo")
	}
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
		"deps.PaymentRepo.ListPage(c.Context(), page, limit)",
		`dealerPaginationView(page, limit, total, "/admin/payments")`,
		"data.AdminVaults = paginateViewSlice(vaultViews, page, limit)",
		`dealerPaginationView(page, limit, int64(len(vaultViews)), "/admin/vault")`,
		"deps.OutboundPolicyRepo.ListWhitelistPage(c.Context(), page, limit)",
		`dealerPaginationView(page, limit, total, "/admin/security")`,
		"deps.ActivityLogRepo.ListPage(c.Context(), page, limit, mID)",
		"buildAdminActivityPaginationBase(merchantFilter)",
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
		`{{.AdminPagination.Total}} session`,
		`data-admin-table-search="admin-payments-table"`,
		`data-admin-table-count="admin-payments-table"`,
		`id="admin-payments-table" data-admin-table`,
		`admin-payment-detail-row`,
		`data-sort-value="{{.AmountSort}}"`,
		`{{.AdminPagination.Total}} asset`,
		`data-admin-table-count="admin-vault-table"`,
		`action="/admin/security/outbound-whitelist"`,
		`data-admin-table-search="admin-activity-table"`,
		`data-admin-table-count="admin-activity-table" data-total="{{.AdminPagination.Total}}"`,
		`id="admin-activity-table" data-admin-table`,
		`data-admin-table-row data-admin-table-key="activity-{{.ID}}"`,
		`name="limit"`,
		`data-sort-value="{{.CreatedSort}}"`,
		`class="admin-activity-pager"`,
		`{{template "pg-always" .AdminPagination}}`,
		`{{define "pg-always"}}`,
	} {
		if !strings.Contains(template, required) {
			t.Fatalf("admin template missing pagination token %q", required)
		}
	}
	if strings.Count(template, `{{template "pg" .AdminPagination}}`) < 13 {
		t.Fatal("admin template should render pagination on every list-heavy panel")
	}

	cssBytes, err := os.ReadFile("../../views/assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)
	for _, required := range []string{
		".admin-payments-table",
		".admin-payment-actions",
		".admin-payment-detail",
		".admin-activity-table",
		".admin-activity-filter-form",
		".admin-activity-pager",
		".admin-table-toolbar",
	} {
		if !strings.Contains(css, required) {
			t.Fatalf("admin CSS missing payments datatable token %q", required)
		}
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
		"CreateRecoverWithHold",
		"ApproveForOutbound",
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

func TestAdminRecoverPanelUsesChainFirstPositiveBalancePagination(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	adminDashboardBody := extractHandlerFunctionBody(t, source, "HandleAdminDashboard")
	recoverSubmitBody := extractHandlerFunctionBody(t, source, "HandleAdminRecoverFunds")
	for _, token := range []string{
		`data.AdminRecoverChains = dealerRecoverChainOptions(deps.AssetRegistry)`,
		`recoverChainValue := dealerRecoverChainFilter(deps.AssetRegistry, firstNonEmpty(c.Params("chain_id"), c.Query("chain")))`,
		`data.AdminRecoverChainFilter = recoverChainValue`,
		`recoverAssetValue := adminRecoverAssetValueFromRequest(deps.AssetRegistry, c)`,
		`data.AdminRecoverAssetFilter = recoverAssetValue`,
		`data.AdminRecoverChainFilter = fmt.Sprintf("%d", selectedAsset.GetChainID())`,
		`WalletIDsWithPositiveAvailableBalance`,
		`deps.WalletRepo.ListByIDs`,
		`filterDealerWalletViewsToAsset`,
		`adminRecoverPaginationBase(recoverAssetValue)`,
		`data.AdminPagination = dealerPaginationView(1, limit, 0, "/admin/recover")`,
	} {
		if !strings.Contains(adminDashboardBody, token) {
			t.Fatalf("admin recover dashboard missing chain-first pagination token %q", token)
		}
	}
	for _, token := range []string{
		`recoverURL := adminRecoverPaginationBase(recoverAssetValue)`,
		`recoverWalletHasRecoverableAssetBalance`,
		`0'dan büyük kullanılabilir veya sweep-locked bakiyeye sahip değil`,
	} {
		if !strings.Contains(recoverSubmitBody, token) {
			t.Fatalf("admin recover submit missing positive-balance guard token %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(templateBytes)
	start := strings.Index(html, `{{/* ══════ RECOVER FUNDS ══════ */}}`)
	end := strings.Index(html, `{{/* ══════ ADMINLER ══════ */}}`)
	if start < 0 || end < 0 || end <= start {
		t.Fatal("recover panel markers not found")
	}
	recoverPanel := html[start:end]
	chainIndex := strings.Index(recoverPanel, `id="recover-chain-select"`)
	assetIndex := strings.Index(recoverPanel, `id="recover-asset-select"`)
	sourceIndex := strings.Index(recoverPanel, `id="recover-source-wallet"`)
	if chainIndex < 0 || assetIndex < 0 || sourceIndex < 0 || chainIndex > assetIndex || assetIndex > sourceIndex {
		t.Fatal("admin recover form must render chain select before asset select before source wallet select")
	}
	for _, token := range []string{
		`{{range .AdminRecoverChains}}`,
		`data-recover-chain-url="/admin/recover"`,
		`{{if eq $.AdminRecoverChainFilter .ChainID}}selected{{end}}`,
		`data-recover-asset-url="/admin/recover"`,
		`data-recover-chain-select="recover-chain-select"`,
		`data-chain-id="{{.ChainID}}"`,
		`{{if not .AdminRecoverChainFilter}}disabled{{end}}`,
		`{{if and $.AdminRecoverChainFilter (ne $.AdminRecoverChainFilter .ChainID)}}disabled hidden{{end}}`,
		`{{if eq $.AdminRecoverAssetFilter .Value}}selected{{end}}`,
		`{{if not .AdminRecoverAssetFilter}}disabled{{end}}`,
		`{{range .WithdrawalWallets}}`,
		`{{if .AdminRecoverAssetFilter}}`,
		`{{template "pg" .AdminPagination}}`,
		`id="recover-live-explorer-link"`,
		`data-locked-raw="{{.LockedRaw}}"`,
		`{{if .ExplorerURL}}<a href="{{.ExplorerURL}}"`,
	} {
		if !strings.Contains(recoverPanel, token) {
			t.Fatalf("admin recover template missing chain-first token %q", token)
		}
	}
	sourceSelect := recoverPanel[sourceIndex:]
	if closeIndex := strings.Index(sourceSelect, `</select>`); closeIndex >= 0 {
		sourceSelect = sourceSelect[:closeIndex]
	}
	if strings.Contains(sourceSelect, `{{range .AdminWallets}}`) {
		t.Fatal("admin recover source select must use filtered WithdrawalWallets, not all AdminWallets")
	}

	dashboardJS, err := os.ReadFile("../../views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(dashboardJS)
	for _, token := range []string{
		`var chainSelect = document.getElementById('recover-chain-select');`,
		`function selectedRecoverChainValue()`,
		`function syncRecoverAssetOptions()`,
		`data-chain-id`,
		`suppressRecoverAssetNavigate`,
		`function navigateToRecoverChain()`,
		`function recoverPathSelection()`,
		`function recoverAssetPathValue(assetValue)`,
		`chainSelect.addEventListener('change'`,
		`function syncRecoverSourceWalletOptions()`,
		`function navigateToRecoverAsset()`,
		`data-recover-chain-url`,
		`data-recover-asset-url`,
		`URLSearchParams(window.location.search`,
		`option.disabled || option.hidden`,
		`root.setAttribute('data-disabled'`,
		`selectedAssetIsNative`,
		`function recoverableRawForBalance(balance)`,
		`function smallerPositiveIntegerString(left, right)`,
		`maxButton.setAttribute('data-max-raw', balance.lockedRaw)`,
		`Locked bakiye transfer edilebilir`,
		`subtractIntegerStrings(grossMaxRaw, feeRaw)`,
		`payload.explorer_url`,
	} {
		if !strings.Contains(js, token) {
			t.Fatalf("dashboard.js missing recover chain-first token %q", token)
		}
	}
	adminCSS, err := os.ReadFile("../../views/assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(adminCSS), `.admin-rich-select[data-disabled="true"] .admin-rich-trigger`) {
		t.Fatal("admin css missing disabled rich select state")
	}
}

func TestAdminOutboundTransfersCarrySignerAuditContext(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	mainSource, err := os.ReadFile("../../main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	workerBody := extractHandlerFunctionBody(t, string(mainSource), "executeOutboundTransaction")
	for name, body := range map[string]string{
		"HandleAdminRecoverFunds":      extractHandlerFunctionBody(t, source, "HandleAdminRecoverFunds"),
		"HandleAdminWithdrawalApprove": extractHandlerFunctionBody(t, source, "HandleAdminWithdrawalApprove"),
		"HandleAdminRefundApprove":     extractHandlerFunctionBody(t, source, "HandleAdminRefundApprove"),
	} {
		if !strings.Contains(body, "adminEmail") {
			t.Fatalf("%s must persist admin actor identity for outbound worker", name)
		}
	}
	for _, token := range []string{"ActorID:       outbound.ActorID", "CorrelationID: outbound.CorrelationID", "JobID:         outbound.ID.String()"} {
		if !strings.Contains(workerBody, token) {
			t.Fatalf("executeOutboundTransaction must pass signer audit context token %q", token)
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
		{"HandleAdminRecoverFunds", "enforceDealerOutboundPolicy", "deps.WithdrawalRepo.CreateRecoverWithHold"},
		{"HandleAdminWithdrawalApprove", "enforceDealerOutboundPolicy", "deps.WithdrawalRepo.ApproveForOutbound"},
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
		`HandleAdminUpdateAdminRole(dealerDeps)`,
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
		`/admin/admins/{{.ID}}/role`,
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
	if !strings.Contains(withdrawal, "ApproveForOutbound") {
		t.Fatal("withdrawal approve must enqueue durable outbound transaction")
	}
	if strings.Contains(withdrawal, "constants.WebhookEventPayoutBroadcastV1") {
		t.Fatal("withdrawal approve must not enqueue broadcast event before worker persists tx hash")
	}
	if strings.Contains(withdrawal, "constants.WebhookEventPayoutFinalizedV1") {
		t.Fatal("withdrawal approve must not enqueue finalized event without finality evidence")
	}

	refund := extractHandlerFunctionBody(t, source, "HandleAdminRefundApprove")
	if !strings.Contains(refund, "requireOutboundMakerChecker") {
		t.Fatal("refund approve must enforce configured maker-checker guard")
	}
	if !strings.Contains(refund, "ClaimPendingWithHoldAndSourceForOutbound") {
		t.Fatal("refund approve must persist source metadata and enqueue durable outbound transaction")
	}
	if strings.Contains(refund, "constants.WebhookEventRefundBroadcastV1") {
		t.Fatal("refund approve must not enqueue broadcast event before worker persists tx hash")
	}
	for _, forbidden := range []string{
		"ExecuteReservedWalletTransfer",
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
			mutateToken: "ApproveForOutbound",
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
			mutateToken: "ClaimPendingWithHoldAndSourceForOutbound",
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
		`data-payment-link-wizard`,
		`data-payment-wizard-step="0"`,
		`data-payment-wizard-panel="2"`,
		`data-payment-wizard-next`,
		`data-payment-wizard-submit`,
		`data-payment-fixed-fields`,
		`data-required-when-fixed="true"`,
		`data-product-edit-form`,
		`data-edit-product="{{.ID}}"`,
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
		"initPaymentLinkWizard",
		"data-payment-wizard-ready",
		"firstInvalidPanel",
		"dashboard:modal-open",
		"field.classList.toggle('hidden', donation)",
		"input.required = !donation",
		"input.disabled = donation",
		"currency.disabled = donation",
	} {
		if !strings.Contains(js, token) {
			t.Fatalf("dashboard.js missing payment link toggle token %q", token)
		}
	}
}

func TestDashboardRichSelectMenuUsesViewportPlacement(t *testing.T) {
	dashboardJS, err := os.ReadFile("../../views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(dashboardJS)
	for _, token := range []string{
		"positionMenu()",
		"window.addEventListener('scroll', positionMenu, true)",
		"menu.setAttribute('data-floating', 'true')",
		"document.body.appendChild(menu)",
		"root.appendChild(menu)",
		"control.menu.contains(event.target)",
		"trigger.getBoundingClientRect()",
		"menu.style.position = 'fixed'",
		"optionsEl.style.maxHeight",
	} {
		if !strings.Contains(js, token) {
			t.Fatalf("dashboard rich select viewport placement missing %q", token)
		}
	}

	adminCSS, err := os.ReadFile("../../views/assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(adminCSS), `.admin-rich-menu[data-floating="true"]`) {
		t.Fatal("admin.css must define floating rich select menu override")
	}
}

func TestMerchantX402ToggleIsSelectableForFixedLinksAndExplainsDonationState(t *testing.T) {
	dashboard, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(dashboard)
	for _, token := range []string{
		`class="merchant-x402-toggle" data-x402-toggle`,
		`id="product_x402_enabled" name="x402_enabled" value="true" type="checkbox" aria-describedby="product_x402_help"`,
		`id="product_edit_x402_enabled" name="x402_enabled" value="true" type="checkbox" aria-describedby="product_edit_x402_help"`,
		`data-x402-help`,
		`data-fixed-copy="Network ve asset checkout'ta seçilir; alıcı adresi ve tutar session'dan otomatik alınır."`,
		`data-donation-copy="x402 yalnızca sabit tutarlı payment link'lerde kullanılabilir."`,
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("merchant x402 toggle missing accessibility token %q", token)
		}
	}
	if strings.Count(html, `class="merchant-x402-toggle" data-x402-toggle`) != 2 {
		t.Fatal("create and edit forms must each render one visible x402 toggle")
	}
	if strings.Contains(html, `class="merchant-x402-toggle" data-payment-fixed-fields`) ||
		strings.Contains(html, `data-payment-fixed-fields data-x402-toggle`) {
		t.Fatal("donation mode must keep the disabled x402 toggle visible so its explanation remains readable")
	}
	if strings.Contains(html, `id="product_x402_enabled" name="x402_enabled" value="true" type="checkbox" disabled`) ||
		strings.Contains(html, `id="product_edit_x402_enabled" name="x402_enabled" value="true" type="checkbox" disabled`) {
		t.Fatal("fixed-link x402 checkbox must be enabled in the rendered form")
	}

	dashboardJS, err := os.ReadFile("../../views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(dashboardJS)
	for _, token := range []string{
		`var x402Toggle = form.querySelector('[data-x402-toggle]')`,
		`var x402Help = form.querySelector('[data-x402-help]')`,
		`x402Input.disabled = donation`,
		`x402Input.setAttribute('aria-disabled', donation ? 'true' : 'false')`,
		`x402Input.checked = false`,
		`x402Toggle.setAttribute('data-disabled', donation ? 'true' : 'false')`,
		`var copyAttribute = donation ? 'data-donation-copy' : 'data-fixed-copy'`,
	} {
		if !strings.Contains(js, token) {
			t.Fatalf("dashboard x402 toggle behavior missing %q", token)
		}
	}
}

func TestMerchantRescanFormShowsSubmitFeedback(t *testing.T) {
	dashboard, err := os.ReadFile("../../views/dealer/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(dashboard)
	for _, token := range []string{
		`data-rescan-form`,
		`data-rescan-submit`,
		`data-rescan-feedback`,
		`data-rescan-elapsed`,
		`data-rich-select="chain"`,
		`{{range .RescanChains}}`,
		`data-logo-url="{{.LogoURL}}"`,
		`İstek gönderildi; blockchain RPC yanıtı bekleniyor.`,
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("merchant rescan form missing feedback token %q", token)
		}
	}

	dashboardJS, err := os.ReadFile("../../views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(dashboardJS)
	for _, token := range []string{
		"initMerchantRescanFeedback",
		"renderRichAvatar",
		"data-logo-url",
		"form.setAttribute('aria-busy', 'true')",
		"submit.textContent = 'İşleniyor...'",
		"RPC yanıtı bekleniyor; işlem devam ediyor.",
	} {
		if !strings.Contains(js, token) {
			t.Fatalf("dashboard.js missing rescan feedback token %q", token)
		}
	}

	adminCSS, err := os.ReadFile("../../views/assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(adminCSS), ".merchant-rescan-feedback") {
		t.Fatal("admin.css must define merchant rescan feedback styles")
	}
	if !strings.Contains(string(adminCSS), `.admin-rich-select[data-kind="chain"] .admin-rich-avatar`) {
		t.Fatal("admin.css must define rich chain select avatar styles")
	}
}

func TestDealerRescanChainOptionsIncludeLogos(t *testing.T) {
	options := dealerRescanChainOptions()
	if len(options) == 0 {
		t.Fatal("dealerRescanChainOptions returned no chains")
	}
	for _, option := range options {
		if option.Name == "" || option.Label == "" || option.ChainID == "" {
			t.Fatalf("rescan chain option missing identity fields: %+v", option)
		}
		if option.LogoURL == "" {
			t.Fatalf("rescan chain option %s missing logo URL", option.Name)
		}
		if !strings.HasPrefix(option.LogoURL, "/static/chains/") || !strings.HasSuffix(option.LogoURL, ".svg") {
			t.Fatalf("rescan chain option %s logo URL = %q, want static SVG", option.Name, option.LogoURL)
		}
	}
}

func TestPaymentLinkSessionExpiresAtSkipsDonation(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	if got := paymentLinkSessionExpiresAt(models.PaymentLinkTypeDonation, now); got != nil {
		t.Fatalf("donation expires_at = %v, want nil", got)
	}
	got := paymentLinkSessionExpiresAt(models.PaymentLinkTypeFixed, now)
	if got == nil {
		t.Fatal("fixed payment link expires_at = nil, want ttl")
	}
	if want := now.Add(paymentSessionTTL()); !got.Equal(want) {
		t.Fatalf("fixed expires_at = %s, want %s", got, want)
	}
}

func TestDealerAssetSelectionPreflightRunsBeforePersistence(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	for _, functionName := range []string{"HandleDealerInvoiceCreate", "HandlePaymentLink"} {
		body := extractHandlerFunctionBody(t, source, functionName)
		prepareToken := "preparePaymentCreateAssetSelection("
		if count := strings.Count(body, prepareToken); count != 1 {
			t.Fatalf("%s has %d asset-selection preflight calls, want 1", functionName, count)
		}
		prepareIndex := strings.Index(body, prepareToken)
		walletCreateIndex := strings.Index(body, "deps.WalletRepo.Create(")
		paymentCreateIndex := strings.Index(body, "deps.PaymentRepo.Create(")
		applyIndex := strings.Index(body, "applyPaymentCreateAssetSelection(")
		if walletCreateIndex < 0 || paymentCreateIndex < 0 || applyIndex < 0 {
			t.Fatalf("%s is missing the expected wallet/session persistence flow", functionName)
		}
		if prepareIndex > walletCreateIndex || prepareIndex > paymentCreateIndex {
			t.Fatalf("%s must validate and quote the asset before wallet/session persistence", functionName)
		}
		if applyIndex < paymentCreateIndex {
			t.Fatalf("%s must apply the prepared selection after session creation", functionName)
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

func TestAdminTestsCreatePaymentDonationLinksAndSessionDeposits(t *testing.T) {
	routes := readHandlerSource(t, "../routes/routes.go")
	if !strings.Contains(routes, `r.fiber.Post("/admin/test-links", handlers.HandleAdminTestPaymentLinkCreate(dealerDeps))`) {
		t.Fatal("admin test payment link route must be registered")
	}

	source := readHandlerSource(t, "dealer.go")
	dashboard := extractHandlerFunctionBody(t, source, "HandleAdminDashboard")
	for _, token := range []string{
		"data.AdminTestDomains = dealerAdminTestDomainOptions",
		"deps.PaymentRepo.ListTestableCheckoutSessions",
		"data.AdminTestPayments = dealerTestPaymentViews",
	} {
		if !strings.Contains(dashboard, token) {
			t.Fatalf("admin dashboard test panel missing token %q", token)
		}
	}
	createLink := extractHandlerFunctionBody(t, source, "HandleAdminTestPaymentLinkCreate")
	for _, token := range []string{
		"models.NormalizePaymentLinkType(c.FormValue(\"link_type\"))",
		"models.IsDonationLinkType(linkType)",
		"types.ValidatePositiveDecimal(amount)",
		"deps.ProductRepo.Create(c.Context(), product)",
		`baseURL(c) + "/payment-links/" + product.LinkToken`,
	} {
		if !strings.Contains(createLink, token) {
			t.Fatalf("admin test link create handler missing token %q", token)
		}
	}
	deposit := extractHandlerFunctionBody(t, source, "HandleAdminTestDeposit")
	for _, token := range []string{
		`adminTestPaymentOutcome(c) == "fail"`,
		`c.FormValue("payment_session_id")`,
		"adminPaymentSessionAsset(deps.AssetRegistry, *matchedSession)",
		"matchedSession.ExpectedAmountRaw",
		"positiveTokenAmountRaw(amountRaw)",
		"adminTestReturnToCheckout(c)",
		"session başarıya taşındı",
	} {
		if !strings.Contains(deposit, token) {
			t.Fatalf("admin test deposit handler missing session token %q", token)
		}
	}
	failHandler := extractHandlerFunctionBody(t, source, "handleAdminTestPaymentFailure")
	for _, token := range []string{
		"deps.PaymentRepo.MarkFailedForTest",
		"deps.WebhookDeliveryRepo.EnqueuePayment",
		"checkoutLocalizedURL(session.SessionToken, \"/pay\", adminTestCheckoutLang(c), \"\")",
	} {
		if !strings.Contains(failHandler, token) {
			t.Fatalf("admin test fail handler missing token %q", token)
		}
	}

	repoBytes, err := os.ReadFile("../../repositories/payment_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	repo := string(repoBytes)
	for _, token := range []string{
		"func (r *PaymentRepo) ListTestableCheckoutSessions(ctx context.Context, limit int)",
		"selected_chain_id IS NOT NULL",
		"TRIM(COALESCE(deposit_address, '')) <> ''",
		"models.PaymentLinkTypeDonation",
	} {
		if !strings.Contains(repo, token) {
			t.Fatalf("payment repo missing testable checkout token %q", token)
		}
	}

	templateBytes, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	template := string(templateBytes)
	for _, token := range []string{
		`action="/admin/test-links"`,
		`name="link_type" value="fixed"`,
		`name="link_type" value="donation"`,
		`{{range .AdminTestPayments}}`,
		`name="payment_session_id" value="{{.ID}}"`,
		`name="return_to" value="checkout"`,
		`name="test_outcome" value="success"`,
		`name="test_outcome" value="fail"`,
		`Sanal bakiye + success`,
		`Fail göster`,
	} {
		if !strings.Contains(template, token) {
			t.Fatalf("admin test template missing token %q", token)
		}
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
		"addressExplorerURL(deps.Blockchains, chainID, address)",
		`"network_fee_raw":`,
		`"network_fee_error":`,
		`"transferable_raw":`,
		`"result":`,
		`"success"`,
		`"balance_raw":`,
		`raw`,
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("live balance contract missing %q", token)
		}
	}

	helperBody := extractHandlerFunctionBody(t, source, "adminLiveBalanceRawForAsset")
	for _, token := range []string{
		"selected.GetChainType() == asset.ChainEVM",
		"adminLiveEVMNativeBalanceRaw(ctx, chain, address)",
		"adminLiveEVMTokenBalanceRaw(ctx, chain, address, selected)",
		"chain.BatchBalances(ctx, []string{address}, 1)",
		"adminLiveBalanceRaw(result.Balance, selected)",
	} {
		if !strings.Contains(helperBody, token) {
			t.Fatalf("live balance helper contract missing %q", token)
		}
	}

	nativeEVMBody := extractHandlerFunctionBody(t, source, "adminLiveEVMNativeBalanceRaw")
	for _, token := range []string{
		"ethclient.DialContext",
		"client.BalanceAt",
		"common.HexToAddress(address)",
	} {
		if !strings.Contains(nativeEVMBody, token) {
			t.Fatalf("EVM native live balance helper missing %q", token)
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

func TestAdminSweepUsesJobQueueInsteadOfManualRecoverTransfer(t *testing.T) {
	routes := readHandlerSource(t, "../routes/routes.go")
	if !strings.Contains(routes, `r.fiber.Post("/admin/sweep", handlers.HandleAdminSweepEnqueue(dealerDeps))`) {
		t.Fatal("admin sweep post route must enqueue sweep jobs")
	}
	if strings.Contains(routes, `r.fiber.Post("/admin/sweep", handlers.HandleAdminRecoverFunds(dealerDeps))`) {
		t.Fatal("admin sweep post route must not use manual recover funds handler")
	}

	source := readHandlerSource(t, "dealer.go")
	handler := extractHandlerFunctionBody(t, source, "HandleAdminSweepEnqueue")
	for _, token := range []string{
		"requirePrivilegedAdmin(c, deps.AdminRepo)",
		"deps.SweepJobRepo.EnqueueMissingFinalizedTransactions(c.Context(), limit)",
		`logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.sweep_enqueue"`,
		`redirectWithSuccess(c, "/admin/sweep", message)`,
	} {
		if !strings.Contains(handler, token) {
			t.Fatalf("admin sweep enqueue contract missing %q", token)
		}
	}

	dashboard, err := os.ReadFile("../../views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(dashboard)
	for _, token := range []string{
		`<form method="post" action="{{.AdminSweepURL}}"`,
		"Finalized depositleri wallet bazlı task kuyruğuna",
		"Sweep task queue",
		"{{.AdminSweepEligibleCount}}",
		"{{range .AdminSweepJobs}}",
		"admin-sweep-jobs-table",
		"<th>Status</th>",
		"<th>Job / TX</th>",
		"<th>Retry / zaman</th>",
		`<tr><td colspan="5"`,
	} {
		if !strings.Contains(html, token) {
			t.Fatalf("admin sweep template missing queue token %q", token)
		}
	}
	start := strings.Index(html, `{{if eq .AdminPanel "sweep"}}`)
	end := strings.Index(html, `{{/* ══════ RECOVER FUNDS ══════ */}}`)
	if start < 0 || end < 0 || end <= start {
		t.Fatal("admin sweep panel markers not found")
	}
	sweepPanel := html[start:end]
	tableStart := strings.Index(sweepPanel, `admin-sweep-jobs-table`)
	tableEnd := -1
	if tableStart >= 0 {
		tableEnd = tableStart + strings.Index(sweepPanel[tableStart:], `</table>`)
	}
	if tableStart < 0 || tableEnd < 0 || tableEnd <= tableStart {
		t.Fatal("admin sweep jobs table not found")
	}
	sweepJobsTable := sweepPanel[tableStart:tableEnd]
	for _, forbidden := range []string{
		`id="recover-form"`,
		`name="wallet_id" id="recover-source-wallet"`,
		`name="amount_raw"`,
		`Recover transfer gönder`,
	} {
		if strings.Contains(sweepPanel, forbidden) {
			t.Fatalf("admin sweep panel must not render manual recover control %q", forbidden)
		}
	}
	for _, forbidden := range []string{
		"<th>Result</th>",
		"<th>Timing</th>",
		`colspan="7"`,
	} {
		if strings.Contains(sweepJobsTable, forbidden) {
			t.Fatalf("admin sweep table must stay compact and not render %q", forbidden)
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
		id:      constants.TRON,
		name:    "tron",
		results: []models.BalanceResult{{Address: "T1111111111111111111111111111111111", Balance: "TRX:1.25"}},
	}
	raw, err := adminLiveBalanceRawForAsset(context.Background(), chain, "T1111111111111111111111111111111111", asset.NewTRX(constants.TRON))
	if err != nil {
		t.Fatal(err)
	}
	if raw != "1250000" {
		t.Fatalf("raw = %q, want 1250000", raw)
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

func TestAddressExplorerURLBuildsChilizAddressLink(t *testing.T) {
	got := addressExplorerURL(nil, constants.Chiliz, "0x1111111111111111111111111111111111111111")
	want := "https://scan.chiliz.com/address/0x1111111111111111111111111111111111111111"
	if got != want {
		t.Fatalf("address explorer url = %q, want %q", got, want)
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

func TestAdminAndAuditMutationsDoNotIgnorePersistenceErrors(t *testing.T) {
	source := readHandlerSource(t, "dealer.go")
	for _, check := range []struct {
		function string
		want     string
		forbid   string
	}{
		{"HandleAdminToggleAdmin", "if err := deps.AdminRepo.SetActive", "_ = deps.AdminRepo.SetActive"},
		{"HandleAdminResetTOTP", "if err := deps.AdminRepo.DisableTOTP", "_ = deps.AdminRepo.DisableTOTP"},
		{"logDealerActivity", "if err := repo.Create", "_ = repo.Create"},
		{"logDealerDecisionActivity", "if err := repo.Create", "_ = repo.Create"},
		{"markAdminWebhookDeliveryAttempt", "if err := deps.WebhookDeliveryRepo.MarkAttempt", "_ = deps.WebhookDeliveryRepo.MarkAttempt"},
	} {
		body := extractHandlerFunctionBody(t, source, check.function)
		if !strings.Contains(body, check.want) {
			t.Fatalf("%s missing persistence error handling %q", check.function, check.want)
		}
		if strings.Contains(body, check.forbid) {
			t.Fatalf("%s still ignores persistence error with %q", check.function, check.forbid)
		}
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
