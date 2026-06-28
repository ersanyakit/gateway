package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"core/api/handlers"
	"core/models"

	"github.com/gofiber/template/html/v3"
)

func TestDealerViewsRender(t *testing.T) {
	engine := html.New("./views", ".html")

	data := handlers.DealerPageData{
		Title:               "Üye işyeri girişi",
		Active:              "login",
		OIDCLoginURL:        "/auth/oidc/login",
		OIDCProvider:        "Kurumsal hesap",
		RegisterURL:         "/merchant/register",
		LoginURL:            "/merchant/login",
		OnboardingURL:       "/merchant/onboarding",
		ActivityURL:         "/merchant/dashboard/activity",
		ActivityAuditURL:    "/merchant/dashboard/activity/audit",
		ActivityPaymentsURL: "/merchant/dashboard/activity/payments",
		ActivityDepositsURL: "/merchant/dashboard/activity/deposits",
		IntegrationsURL:     "/merchant/dashboard/domains",
		DomainsPanelURL:     "/merchant/dashboard/domains",
		ProductsPanelURL:    "/merchant/dashboard/products/index",
		ProductsLinksURL:    "/merchant/dashboard/products/links",
		RescanURL:           "/merchant/dashboard/rescan",
		MerchantID:          "merchant-id",
		MerchantName:        "Demo Merchant",
		MerchantEmail:       "demo@example.com",
		AdminPanel:          "rescan",
		AdminRescanURL:      "/admin/rescan",
		AdminRefundsURL:     "/admin/refunds",
		AssetCount:          1,
		NetworkCount:        3,
		Balances: []handlers.DealerBalanceView{
			{
				Chain:         "Solana",
				Symbol:        "SOL",
				AmountDisplay: "50",
				Deposits:      2,
				Users:         1,
			},
		},
		TreasuryGroups: []handlers.DealerVaultAssetView{
			{
				ID:               "treasury-sol",
				Symbol:           "SOL",
				SearchText:       "SOL Solana",
				NetworkCount:     1,
				VariantCount:     1,
				AvailableDisplay: "50",
				AvailableSort:    "50",
				Details: []handlers.DealerVaultBalanceView{
					{
						Chain:            "Solana",
						Symbol:           "SOL",
						DisplayToken:     "-",
						AvailableDisplay: "50",
					},
				},
			},
		},
		ChainVaults: []handlers.DealerChainVaultView{
			{Chain: "Bitcoin", Empty: true},
			{Chain: "TRON", Empty: true},
			{Chain: "Solana", Deposits: 2},
		},
		AuditLogs: []handlers.DealerAuditLogView{
			{
				Event:       "dealer.login",
				Status:      "success",
				Actor:       "demo@example.com",
				Subject:     "merchant · merchant-id",
				Description: "Üye işyeri e-posta ile giriş yaptı.",
				IPAddress:   "127.0.0.1",
				UserAgent:   "Go test browser",
				Method:      "POST",
				Path:        "/merchant/login",
				CreatedAt:   "2026-06-02 12:00:00.000 UTC",
				CreatedISO:  "2026-06-02T12:00:00Z",
			},
		},
	}

	for _, view := range []string{
		"dealer/home",
		"dealer/login",
		"dealer/register",
		"dealer/dashboard",
		"dealer/admin_login",
		"dealer/admin_dashboard",
		"dealer/onboarding",
		"dealer/oidc_missing",
	} {
		var buf bytes.Buffer
		if err := engine.Render(&buf, view, data, "dealer/layout"); err != nil {
			t.Fatalf("%s render failed: %v", view, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("%s rendered empty output", view)
		}
		if view == "dealer/dashboard" {
			output := buf.String()
			for _, expected := range []string{"Bitcoin", "TRON", "Coin bazında konsolide treasury", "Solana", "SOL", "50", "dealer.login", "127.0.0.1", "2026-06-02 12:00:00.000 UTC", "/merchant/dashboard/rescan", "/merchant/rescan", `aria-label="Activity views"`, `href="/merchant/dashboard/activity/audit"`, `href="/merchant/dashboard/activity/payments"`, `href="/merchant/dashboard/activity/deposits"`, `aria-label="Integrations views"`, `href="/merchant/dashboard/domains"`, `href="/merchant/dashboard/products/index"`, `data-products-tab="domains"`, `data-products-tab="audit"`, `data-products-tab="payments"`, `data-products-tab="deposits"`, `merchant-audit-table`, `merchant-activity-payments-table`, `merchant-activity-deposits-table`} {
				if !strings.Contains(output, expected) {
					t.Fatalf("%s output missing %q", view, expected)
				}
			}
		}
		if view == "dealer/admin_dashboard" {
			output := buf.String()
			for _, expected := range []string{"Refunds", "Rescan", "/admin/refunds", "/admin/rescan", "Transaction rescan"} {
				if !strings.Contains(output, expected) {
					t.Fatalf("%s output missing %q", view, expected)
				}
			}
		}
	}
}

func TestDealerHomeKeepsPublicStylesWithSession(t *testing.T) {
	engine := html.New("./views", ".html")
	data := handlers.DealerPageData{
		Title:        "Crypto payment gateway",
		Active:       "home",
		HasSession:   true,
		DashboardURL: "/merchant/dashboard",
		LogoutURL:    "/merchant/logout",
		RegisterURL:  "/merchant/register",
		LoginURL:     "/merchant/login",
	}

	var buf bytes.Buffer
	if err := engine.Render(&buf, "dealer/home", data, "dealer/layout"); err != nil {
		t.Fatalf("dealer/home render failed: %v", err)
	}
	output := buf.String()
	for _, expected := range []string{`href="/assets/home.css"`, "home-public-body", "Dashboard'a git"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("home output missing %q", expected)
		}
	}
	for _, unexpected := range []string{`href="/assets/admin.css"`, "admin-vscode-root", "merchant-titlebar"} {
		if strings.Contains(output, unexpected) {
			t.Fatalf("home output unexpectedly contains %q", unexpected)
		}
	}
}

func TestGatewayViewsRenderCriticalStates(t *testing.T) {
	engine := html.New("./views", ".html")
	session := &models.PaymentSession{
		SessionToken:      "checkout-token",
		OrderID:           "ORDER-1001",
		Amount:            "25.00",
		Currency:          "USD",
		SelectedSymbol:    "USDT",
		DepositAddress:    "0x1111111111111111111111111111111111111111",
		ExpectedAmountRaw: "25000000",
	}

	tests := []struct {
		name     string
		view     string
		data     any
		expected []string
	}{
		{
			name: "checkout asset grid with unavailable quote",
			view: "gateway/checkout",
			data: map[string]any{
				"Session":        session,
				"Lang":           "en",
				"IsEnglish":      true,
				"AssetGroups":    []handlers.CheckoutAssetGroup{{Symbol: "PEPPER", Name: "PEPPER", ChainCount: 1, URL: "/checkout/checkout-token?asset=PEPPER", QuoteAvailable: false}},
				"SelectedSymbol": "",
				"Assets":         []handlers.CheckoutAssetOption{},
				"ExpiresAtUnix":  time.Now().Add(time.Hour).UnixMilli(),
			},
			expected: []string{"Gateway pricing", "PEPPER", "1 network"},
		},
		{
			name: "checkout selected asset with no usable networks",
			view: "gateway/checkout",
			data: map[string]any{
				"Session":        session,
				"Lang":           "en",
				"IsEnglish":      true,
				"AssetGroups":    []handlers.CheckoutAssetGroup{},
				"SelectedSymbol": "PEPPER",
				"Assets":         []handlers.CheckoutAssetOption{},
				"ExpiresAtUnix":  time.Now().Add(time.Hour).UnixMilli(),
			},
			expected: []string{"No payment networks are available", "Change asset"},
		},
		{
			name: "payment instruction page",
			view: "gateway/pay",
			data: map[string]any{
				"Session":              session,
				"Lang":                 "en",
				"IsEnglish":            true,
				"QRCodeURL":            "/checkout/checkout-token/qr.png",
				"PaymentURI":           "ethereum:0x1111111111111111111111111111111111111111",
				"ChainName":            "ethereum",
				"ChainLogoURL":         "/static/chains/ethereumchain.svg",
				"AmountDisplay":        "25 USDT",
				"ExpiresAtUnix":        time.Now().Add(time.Hour).UnixMilli(),
				"SelectedAssetLogoURL": "/static/coins/usdt.svg",
				"CheckoutState": map[string]any{
					"Status":  "active",
					"Payable": true,
				},
				"StatusMode":  "detecting",
				"StatusTitle": "Waiting for payment",
				"StatusBody":  "Send the exact amount to the address below.",
			},
			expected: []string{"Send exactly", "Payment QR", "Copy address"},
		},
		{
			name: "payment result page",
			view: "gateway/payment_result",
			data: map[string]any{
				"Title":      "Payment complete",
				"Message":    "Payment received successfully.",
				"Status":     "paid",
				"ResultKind": "success",
				"IsEnglish":  true,
			},
			expected: []string{"Payment complete", "Crypto Checkout", "paid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := engine.Render(&buf, tt.view, tt.data); err != nil {
				t.Fatalf("%s render failed: %v", tt.view, err)
			}
			output := buf.String()
			if strings.TrimSpace(output) == "" {
				t.Fatalf("%s rendered empty output", tt.view)
			}
			for _, expected := range tt.expected {
				if !strings.Contains(output, expected) {
					t.Fatalf("%s output missing %q", tt.view, expected)
				}
			}
		})
	}
}

func TestStandaloneDepositWalletProductClassification(t *testing.T) {
	tests := map[string]bool{
		"static:1:USDT":  true,
		"wallet:default": true,
		"wallet:app":     true,
		"ORDER-1001":     false,
		"wallet":         false,
		"":               false,
	}
	for productID, expected := range tests {
		if got := isStandaloneDepositWalletProduct(productID); got != expected {
			t.Fatalf("isStandaloneDepositWalletProduct(%q) = %v, want %v", productID, got, expected)
		}
	}
}

func TestPaymentRealtimeBroadcastEventMarksPaidTerminal(t *testing.T) {
	txHash := "0xabc"
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusPaid,
		TxHash:       &txHash,
	}
	event := paymentRealtimeBroadcastEvent(session)
	if event.Status != models.PaymentStatusPaid || !event.Paid || !event.Terminal || event.Payable {
		t.Fatalf("event state = %#v, want paid terminal non-payable", event)
	}
	if event.PaymentID != session.ID.String() || event.TxHash != txHash {
		t.Fatalf("event identifiers = %#v", event)
	}
	if event.SuccessPath != "/checkout/checkout-token/return/success" || event.CancelPath != "/checkout/checkout-token/cancel" {
		t.Fatalf("event paths = %#v", event)
	}
}

func TestPaymentRealtimeBroadcastEventMapsAwaitingPaymentToPayerState(t *testing.T) {
	txHash := "0xabc"
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusAwaitingPayment,
		TxHash:       &txHash,
	}
	event := paymentRealtimeBroadcastEvent(session)
	if event.Status != "confirming" || !event.Payable || event.Terminal || event.Paid {
		t.Fatalf("event state = %#v, want confirming payable nonterminal", event)
	}
	session.TxHash = nil
	event = paymentRealtimeBroadcastEvent(session)
	if event.Status != "active" || !event.Payable || event.Terminal || event.Paid {
		t.Fatalf("event state without tx = %#v, want active payable nonterminal", event)
	}
}
