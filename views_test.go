package main

import (
	"bytes"
	"strings"
	"testing"

	"core/api/handlers"

	"github.com/gofiber/template/html/v3"
)

func TestDealerViewsRender(t *testing.T) {
	engine := html.New("./views", ".html")

	data := handlers.DealerPageData{
		Title:           "Bayi girişi",
		Active:          "login",
		OIDCLoginURL:    "/auth/oidc/login",
		OIDCProvider:    "Kurumsal hesap",
		RegisterURL:     "/dealer/register",
		LoginURL:        "/dealer/login",
		OnboardingURL:   "/dealer/onboarding",
		RescanURL:       "/dealer/dashboard/rescan",
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
				Description: "Bayi e-posta ile giriş yaptı.",
				IPAddress:   "127.0.0.1",
				UserAgent:   "Go test browser",
				Method:      "POST",
				Path:        "/dealer/login",
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
			for _, expected := range []string{"Bitcoin", "TRON", "Solana üzerinde SOL", "50", "dealer.login", "127.0.0.1", "2026-06-02 12:00:00.000 UTC", "/dealer/dashboard/rescan", "/dealer/rescan"} {
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
