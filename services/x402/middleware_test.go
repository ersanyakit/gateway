package x402

import (
	"context"
	"core/constants"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	x402sdk "github.com/x402-foundation/x402/go"
	x402http "github.com/x402-foundation/x402/go/http"
)

func TestLoadConfigFromEnvDisabledByDefault(t *testing.T) {
	t.Setenv("X402_ENABLED", "false")
	t.Setenv("X402_ROUTES", "GET /ignored-static-route")
	t.Setenv("X402_PAY_TO", "")

	config, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if config.Enabled {
		t.Fatal("x402 should be disabled unless explicitly enabled")
	}
}

func TestLoadConfigFromEnvKeepsCheckoutOutOfEnvironmentConfig(t *testing.T) {
	t.Setenv("X402_ENABLED", "false")

	config, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if config.Enabled || len(config.Routes) != 0 {
		t.Fatalf("static x402 should remain disabled: %+v", config)
	}
	base, ok := CheckoutNetworkForChain(constants.Base)
	if !ok || base != CheckoutNetworkBase {
		t.Fatalf("Base x402 network = %q, ok=%v", base, ok)
	}
	solana, ok := CheckoutNetworkForChain(constants.Solana)
	if !ok || solana != CheckoutNetworkSolana {
		t.Fatalf("Solana x402 network = %q, ok=%v", solana, ok)
	}
	if _, ok := CheckoutNetworkForChain(constants.Bitcoin); ok {
		t.Fatal("Bitcoin should not be advertised as an exact EVM/Solana checkout network")
	}
}

func TestLoadConfigFromEnvBuildsExactRoutes(t *testing.T) {
	t.Setenv("X402_ENABLED", "true")
	t.Setenv("X402_FACILITATOR_URL", "https://facilitator.example.test")
	t.Setenv("X402_ROUTES", "GET /api/paid, POST /api/compute")
	t.Setenv("X402_NETWORKS", "eip155:84532")
	t.Setenv("X402_NETWORK", "")
	t.Setenv("X402_PAY_TO", "0x1111111111111111111111111111111111111111")
	t.Setenv("X402_PAY_TO_EIP155_84532", "")
	t.Setenv("X402_PRICE", "$0.001")
	t.Setenv("X402_TIMEOUT", "12s")

	config, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if !config.Enabled || config.FacilitatorURL != "https://facilitator.example.test" {
		t.Fatalf("unexpected config: %+v", config)
	}
	if config.Timeout != 12*time.Second {
		t.Fatalf("Timeout = %s, want 12s", config.Timeout)
	}
	if len(config.Routes) != 2 || len(config.Networks) != 1 {
		t.Fatalf("routes/networks = %d/%d, want 2/1", len(config.Routes), len(config.Networks))
	}
	for pattern, route := range config.Routes {
		if len(route.Accepts) != 1 {
			t.Fatalf("route %q accepts = %d, want 1", pattern, len(route.Accepts))
		}
		accept := route.Accepts[0]
		if accept.Scheme != "exact" || accept.Network != x402sdk.Network("eip155:84532") || accept.PayTo != "0x1111111111111111111111111111111111111111" {
			t.Fatalf("route %q payment option = %+v", pattern, accept)
		}
	}
}

func TestLoadConfigFromEnvRejectsMissingPayTo(t *testing.T) {
	t.Setenv("X402_ENABLED", "true")
	t.Setenv("X402_ROUTES", "GET /api/paid")
	t.Setenv("X402_NETWORKS", "eip155:84532")
	t.Setenv("X402_PAY_TO", "")
	t.Setenv("X402_PAY_TO_EIP155_84532", "")

	_, err := LoadConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), "X402_PAY_TO_EIP155_84532") {
		t.Fatalf("LoadConfigFromEnv() error = %v, want missing pay-to error", err)
	}
}

func TestLoadConfigFromEnvSupportsSolanaPayToOverride(t *testing.T) {
	solanaNetwork := x402sdk.Network("solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1")
	t.Setenv("X402_ENABLED", "true")
	t.Setenv("X402_ROUTES", "GET /api/paid")
	t.Setenv("X402_NETWORKS", string(solanaNetwork))
	t.Setenv("X402_PAY_TO", "")
	t.Setenv(payToEnvName(solanaNetwork), "11111111111111111111111111111111")
	t.Setenv("X402_PRICE", "$0.001")

	config, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if got := config.PayTo[solanaNetwork]; got != "11111111111111111111111111111111" {
		t.Fatalf("Solana payTo = %q", got)
	}
	if !config.SyncFacilitatorOnStart {
		t.Fatal("facilitator synchronization should default to enabled")
	}
}

func TestNewMiddlewareReturnsX402PaymentRequiredInFiber(t *testing.T) {
	config := Config{
		Enabled:                true,
		FacilitatorURL:         "https://facilitator.example.test",
		Timeout:                5 * time.Second,
		Networks:               []x402sdk.Network{"eip155:84532"},
		SyncFacilitatorOnStart: false,
		Routes: x402http.RoutesConfig{
			"GET /paid": {
				Accepts: x402http.PaymentOptions{
					{
						Scheme:  "exact",
						Price:   x402sdk.Price("$0.001"),
						Network: "eip155:84532",
						PayTo:   "0x1111111111111111111111111111111111111111",
					},
				},
				Description: "Paid test resource",
				MimeType:    "application/json",
			},
		},
	}

	app := fiber.New()
	app.Use(NewMiddleware(config))
	app.Get("/paid", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"paid": true})
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/paid", nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, body = %s; want 402", response.StatusCode, body)
	}
	if response.Header.Get("PAYMENT-REQUIRED") == "" {
		t.Fatal("PAYMENT-REQUIRED header is missing")
	}
}

func TestNewCheckoutMiddlewareUsesDynamicSessionPayment(t *testing.T) {
	const (
		network = x402sdk.Network("eip155:8453")
		payTo   = "0x2222222222222222222222222222222222222222"
		asset   = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
		amount  = "1250000"
	)
	config := Config{
		FacilitatorURL:         "https://facilitator.example.test",
		Timeout:                5 * time.Second,
		SyncFacilitatorOnStart: false,
	}

	app := fiber.New()
	app.Use(NewCheckoutMiddleware(config, func(ctx context.Context, token string) (CheckoutPayment, error) {
		if token != "session-123" {
			t.Fatalf("resolver token = %q, want session-123", token)
		}
		return CheckoutPayment{
			Network: network,
			PayTo:   payTo,
			Asset:   asset,
			Amount:  amount,
		}, nil
	}))
	app.Get("/checkout/:token/pay", func(c fiber.Ctx) error {
		return c.SendString("paid checkout")
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/session-123/pay", nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, body = %s; want 402", response.StatusCode, body)
	}

	encoded := response.Header.Get("PAYMENT-REQUIRED")
	if encoded == "" {
		t.Fatal("PAYMENT-REQUIRED header is missing")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode PAYMENT-REQUIRED: %v", err)
	}
	var required struct {
		Accepts []struct {
			Network string `json:"network"`
			PayTo   string `json:"payTo"`
			Asset   string `json:"asset"`
			Amount  string `json:"amount"`
		} `json:"accepts"`
	}
	if err := json.Unmarshal(decoded, &required); err != nil {
		t.Fatalf("unmarshal PAYMENT-REQUIRED: %v", err)
	}
	if len(required.Accepts) != 1 {
		t.Fatalf("accepts = %d, want 1", len(required.Accepts))
	}
	accept := required.Accepts[0]
	if accept.Network != string(network) || accept.PayTo != payTo || accept.Asset != asset || accept.Amount != amount {
		t.Fatalf("dynamic payment requirement = %+v", accept)
	}
}

func TestNewCheckoutMiddlewareServesHostedPageForBrowserNavigation(t *testing.T) {
	config := Config{
		FacilitatorURL: "https://facilitator.example.test",
	}
	resolverCalls := 0

	app := fiber.New()
	app.Use(NewCheckoutMiddleware(config, func(context.Context, string) (CheckoutPayment, error) {
		resolverCalls++
		return CheckoutPayment{}, errors.New("browser navigation must not resolve an x402 payment")
	}))
	app.Get("/checkout/:token/pay", func(c fiber.Ctx) error {
		return c.Type("html").SendString("<main>branded checkout</main>")
	})

	request := httptest.NewRequest(http.MethodGet, "/checkout/session-123/pay?lang=tr", nil)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("Sec-Fetch-Mode", "navigate")
	request.Header.Set("Sec-Fetch-Dest", "document")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, body = %s; want hosted checkout", response.StatusCode, body)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if got := string(body); got != "<main>branded checkout</main>" {
		t.Fatalf("body = %q, want branded checkout", got)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolverCalls)
	}
}

func TestCheckoutPrefersHostedPage(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{name: "html accept", headers: map[string]string{"Accept": "text/html"}, want: true},
		{name: "xhtml accept", headers: map[string]string{"Accept": "application/xhtml+xml"}, want: true},
		{name: "browser navigation", headers: map[string]string{"Sec-Fetch-Mode": "navigate"}, want: true},
		{name: "document destination", headers: map[string]string{"Sec-Fetch-Dest": "document"}, want: true},
		{name: "json client", headers: map[string]string{"Accept": "application/json"}, want: false},
		{name: "generic client", headers: map[string]string{"Accept": "*/*"}, want: false},
		{name: "unrelated subtype", headers: map[string]string{"Accept": "application/not-text/html-data"}, want: false},
		{name: "v2 payment retry", headers: map[string]string{"Accept": "text/html", "PAYMENT-SIGNATURE": "payload"}, want: false},
		{name: "v1 payment retry", headers: map[string]string{"Accept": "text/html", "X-PAYMENT": "payload"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/checkout/session/pay", func(c fiber.Ctx) error {
				if got := checkoutPrefersHostedPage(c); got != tt.want {
					t.Fatalf("checkoutPrefersHostedPage() = %v, want %v", got, tt.want)
				}
				return c.SendStatus(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/checkout/session/pay", nil)
			for name, value := range tt.headers {
				request.Header.Set(name, value)
			}
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			response.Body.Close()
		})
	}
}

func TestNewCheckoutMiddlewarePassesIneligibleSessionsThrough(t *testing.T) {
	config := Config{
		FacilitatorURL: "https://facilitator.example.test",
	}

	app := fiber.New()
	app.Use(NewCheckoutMiddleware(config, func(context.Context, string) (CheckoutPayment, error) {
		return CheckoutPayment{}, ErrCheckoutNotEligible
	}))
	app.Get("/checkout/:token/pay", func(c fiber.Ctx) error {
		return c.SendString("normal checkout")
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/session-123/pay", nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
}
