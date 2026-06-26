package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"core/api/handlers"
	"core/models"

	"github.com/gofiber/template/html/v3"
)

func TestDealerViewsRender(t *testing.T) {
	engine := html.New("./views", ".html")

	data := handlers.DealerPageData{
		Title:           "Üye işyeri girişi",
		Active:          "login",
		OIDCLoginURL:    "/auth/oidc/login",
		OIDCProvider:    "Kurumsal hesap",
		RegisterURL:     "/merchant/register",
		LoginURL:        "/merchant/login",
		OnboardingURL:   "/merchant/onboarding",
		RescanURL:       "/merchant/dashboard/rescan",
		MerchantID:      "merchant-id",
		MerchantName:    "Demo Merchant",
		MerchantEmail:   "demo@example.com",
		AdminPanel:      "rescan",
		AdminRescanURL:  "/admin/rescan",
		AdminRefundsURL: "/admin/refunds",
		AssetCount:      1,
		NetworkCount:    3,
		Balances: []handlers.DealerBalanceView{
			{
				Chain:         "Solana",
				Symbol:        "SOL",
				AmountDisplay: "50",
				Deposits:      2,
				Users:         1,
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
			for _, expected := range []string{"Bitcoin", "TRON", "Solana üzerinde SOL", "50", "dealer.login", "127.0.0.1", "2026-06-02 12:00:00.000 UTC", "/merchant/dashboard/rescan", "/merchant/rescan"} {
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
