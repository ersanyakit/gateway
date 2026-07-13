package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"core/types"

	"github.com/gofiber/fiber/v3"
	fiberhtml "github.com/gofiber/template/html/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type fakePriceOracle struct {
	prices map[string]string
}

func (f fakePriceOracle) Price(_ context.Context, symbol string, currency string) (*big.Rat, error) {
	price, ok := f.prices[symbol+"|"+currency]
	if !ok {
		price = f.prices[symbol+"|USD"]
	}
	rat, _ := new(big.Rat).SetString(price)
	return rat, nil
}

type failingPriceOracle struct{}

func (f failingPriceOracle) Price(context.Context, string, string) (*big.Rat, error) {
	return nil, errors.New("pricing unavailable")
}

type panicPriceOracle struct{}

func (p panicPriceOracle) Price(context.Context, string, string) (*big.Rat, error) {
	panic("checkout asset groups should not call the price oracle")
}

func TestCheckoutCanonicalSymbolAliases(t *testing.T) {
	tests := map[string]string{
		"WBTC": "BTC",
		"btc":  "BTC",
		"WETH": "ETH",
		"eth":  "ETH",
		"WCHZ": "CHZ",
		"chz":  "CHZ",
		"SOL":  "SOL",
	}
	for input, expected := range tests {
		if got := checkoutCanonicalSymbol(input); got != expected {
			t.Fatalf("checkoutCanonicalSymbol(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestCheckoutTemplateParses(t *testing.T) {
	if _, err := template.ParseFiles("../../views/gateway/checkout.html"); err != nil {
		t.Fatal(err)
	}
	if _, err := template.ParseFiles("../../views/gateway/payment_result.html"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckoutLanguageSwitcherMarkupContract(t *testing.T) {
	for _, path := range []string{"../../views/gateway/checkout.html", "../../views/gateway/pay.html", "../../views/gateway/payment_result.html"} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		html := string(body)
		for _, want := range []string{
			`class="crypto-lang-switch"`,
			`class="crypto-lang-options"`,
			`class="crypto-lang-flag" src="/static/icons/flags/tr.svg"`,
			`class="crypto-lang-flag" src="/static/icons/flags/gb.svg"`,
			`class="crypto-lang-name">Türkçe`,
			`class="crypto-lang-name">English`,
			`aria-current="page"`,
			`aria-label="Türkçe"`,
			`aria-label="English"`,
			`hreflang="tr"`,
			`hreflang="en"`,
		} {
			if !strings.Contains(html, want) {
				t.Fatalf("%s missing language switcher token %q", path, want)
			}
		}
		for _, forbidden := range []string{
			`crypto-lang-code`,
			`>TR<`,
			`>EN<`,
		} {
			if strings.Contains(html, forbidden) {
				t.Fatalf("%s should not render language code token %q", path, forbidden)
			}
		}
	}

	for _, path := range []string{"../../static/icons/flags/tr.svg", "../../static/icons/flags/gb.svg"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("checkout language flag asset missing: %s: %v", path, err)
		}
	}

	cssBody, err := os.ReadFile("../../views/assets/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBody)
	for _, want := range []string{
		`--pay-font: GoogleSans, "Google Sans", "Google Sans Text"`,
		`font-family: var(--pay-font);`,
		`.crypto-lang-options`,
		`.crypto-lang-flag`,
		`grid-template-columns: repeat(2, minmax(70px, 1fr));`,
		`grid-template-columns: repeat(2, minmax(48px, 1fr));`,
		`body.crypto-payment-flow.crypto-pay-page .crypto-lang-options`,
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("checkout language switcher CSS missing %q", want)
		}
	}
}

func TestCheckoutAssetSelectionDoesNotCallPriceOracle(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewERC20(
		constants.Ethereum,
		"0x1111111111111111111111111111111111111111",
		"PEPPER",
		"PEPPER",
		18,
	))
	session := models.PaymentSession{
		SessionToken: "checkout-token",
		Amount:       "10",
		Currency:     "USD",
		Wallet: models.Wallet{
			EthereumAddress: "0x2222222222222222222222222222222222222222",
		},
	}
	deps := PaymentHandlerDeps{
		AssetRegistry: registry,
		PriceOracle:   panicPriceOracle{},
	}

	options := checkoutAssetOptions(context.Background(), deps, session, "PEPPER")
	if len(options) != 1 {
		t.Fatalf("options = %d, want 1", len(options))
	}
	if !options[0].Available {
		t.Fatal("asset with an address should be selectable before the final quote step")
	}
	if options[0].QuoteAvailable {
		t.Fatal("asset options should not quote during route rendering")
	}
	if options[0].UnavailableReason != "" {
		t.Fatalf("unavailable reason = %q, want empty", options[0].UnavailableReason)
	}
	if options[0].AmountDisplay != "" {
		t.Fatalf("amount display = %q, want empty", options[0].AmountDisplay)
	}

	groupDeps := deps
	groupDeps.PriceOracle = panicPriceOracle{}
	groups := checkoutAssetGroups(context.Background(), groupDeps, session)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if groups[0].Symbol != "PEPPER" {
		t.Fatalf("group symbol = %q, want PEPPER", groups[0].Symbol)
	}
	if groups[0].QuoteAvailable {
		t.Fatal("group quote should be marked unavailable")
	}
	if groups[0].ChainCount != 1 {
		t.Fatalf("chain count = %d, want 1", groups[0].ChainCount)
	}
}

func TestHandleCheckoutRendersCombinedAssetNetworkSelection(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	registry.Register(asset.NewERC20(constants.Ethereum, "0x1111111111111111111111111111111111111111", "USDT", "Tether USD", 6))
	registry.Register(asset.NewERC20(constants.Ethereum, "0x3333333333333333333333333333333333333333", "WETH", "Wrapped Ether", 18))
	future := time.Now().Add(10 * time.Minute)
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusPending,
		Amount:       "10",
		Currency:     "USD",
		ExpiresAt:    &future,
		Wallet: models.Wallet{
			EthereumAddress: "0x2222222222222222222222222222222222222222",
		},
	}
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
	app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
	app.Get("/checkout/:token", HandleCheckout(PaymentHandlerDeps{
		PaymentRepo:   repo,
		AssetRegistry: registry,
		PriceOracle:   panicPriceOracle{},
	}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/checkout-token?lang=tr", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, html)
	}
	if !strings.Contains(html, `method="post" action="/checkout/checkout-token/select?lang=tr"`) {
		t.Fatalf("checkout did not render direct asset/network forms: %s", html)
	}
	if !strings.Contains(html, `name="symbol" value="ETH"`) || !strings.Contains(html, `name="symbol" value="USDT"`) {
		t.Fatalf("checkout did not render all asset symbols: %s", html)
	}
	if !strings.Contains(html, `data-symbol="ETH" data-network-row`) || !strings.Contains(html, `name="symbol" value="WETH"`) {
		t.Fatalf("checkout did not group wrapped assets under canonical picker symbols: %s", html)
	}
	if strings.Contains(html, `?asset=ETH`) || strings.Contains(html, `?asset=USDT`) {
		t.Fatalf("checkout still renders intermediate asset links: %s", html)
	}
}

func TestPaymentDepositAddressForChainRejectsMismatchedAddressFamilies(t *testing.T) {
	wallet := models.Wallet{
		EthereumAddress: "0x1111111111111111111111111111111111111111",
		SolanaAddress:   "So11111111111111111111111111111111111111112",
		TronAddress:     "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7",
		BitcoinAddress:  "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080",
	}
	if got := paymentDepositAddressForChain(wallet, constants.Solana); got != wallet.SolanaAddress {
		t.Fatalf("solana deposit address = %q, want %q", got, wallet.SolanaAddress)
	}
	wallet.SolanaAddress = wallet.EthereumAddress
	if got := paymentDepositAddressForChain(wallet, constants.Solana); got != "" {
		t.Fatalf("mismatched solana deposit address = %q, want empty", got)
	}
	if got := paymentDepositAddressForChain(wallet, constants.Ethereum); got != wallet.EthereumAddress {
		t.Fatalf("ethereum deposit address = %q, want %q", got, wallet.EthereumAddress)
	}
}

func TestCheckoutExpectedAmountRawUsesFiatPrice(t *testing.T) {
	session := models.PaymentSession{
		Amount:   "1",
		Currency: "USD",
	}
	eth := asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18)
	amountRaw, err := checkoutExpectedAmountRaw(context.Background(), fakePriceOracle{
		prices: map[string]string{
			"ETH|USD": "2000",
		},
	}, session, eth)
	if err != nil {
		t.Fatal(err)
	}
	if amountRaw != "500000000000000" {
		t.Fatalf("amount raw = %s, want 500000000000000", amountRaw)
	}
	if got := formatPaymentAmount(amountRaw, eth.GetDecimals(), eth.GetSymbol()); got != "0.0005 ETH" {
		t.Fatalf("amount display = %q, want 0.0005 ETH", got)
	}
}

func TestCheckoutExpectedAmountRawRoundsUp(t *testing.T) {
	session := models.PaymentSession{
		Amount:   "1",
		Currency: "USD",
	}
	usdc := asset.NewERC20(constants.Ethereum, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "USDC", "USDC", 6)
	amountRaw, err := checkoutExpectedAmountRaw(context.Background(), fakePriceOracle{
		prices: map[string]string{
			"USDC|USD": "3",
		},
	}, session, usdc)
	if err != nil {
		t.Fatal(err)
	}
	if amountRaw != "333334" {
		t.Fatalf("amount raw = %s, want 333334", amountRaw)
	}
}

func TestDonationPaymentURIOmitsAmount(t *testing.T) {
	chainID := constants.Ethereum
	session := models.PaymentSession{
		LinkType:         models.PaymentLinkTypeDonation,
		SelectedChainID:  &chainID,
		SelectedSymbol:   "ETH",
		SelectedDecimals: 18,
		DepositAddress:   "0x2222222222222222222222222222222222222222",
	}
	uri := paymentURI(session)
	if uri != "ethereum:0x2222222222222222222222222222222222222222@1" {
		t.Fatalf("donation payment uri = %q", uri)
	}
	if strings.Contains(uri, "amount=") || strings.Contains(uri, "value=") {
		t.Fatalf("donation payment uri should not encode an amount: %q", uri)
	}
}

func TestPreparePaymentCreateAssetSelectionQuotesSupportedAsset(t *testing.T) {
	registry := asset.NewRegistry()
	token := "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	registry.Register(asset.NewERC20(constants.Ethereum, token, "USDC", "USD Coin", 6))

	orderID := "order-1"
	amount := "10"
	currency := "USD"
	chainID := int64(constants.Ethereum)
	symbol := " usdc "
	tokenInput := " " + token + " "
	params := types.PaymentCreateParams{
		OrderID:  &orderID,
		Amount:   &amount,
		Currency: &currency,
		ChainID:  &chainID,
		Symbol:   &symbol,
		Token:    &tokenInput,
	}
	if err := params.Validate(); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(30 * time.Minute)
	selection, err := preparePaymentCreateAssetSelection(context.Background(), registry, fakePriceOracle{
		prices: map[string]string{"USDC|USD": "1"},
	}, params, expiresAt, now)
	if err != nil {
		t.Fatal(err)
	}
	if selection == nil {
		t.Fatal("selection should be prepared")
	}
	if selection.chainID != constants.Ethereum {
		t.Fatalf("chain id = %d, want ethereum", selection.chainID)
	}
	if selection.symbol != "USDC" {
		t.Fatalf("symbol = %q, want USDC", selection.symbol)
	}
	if selection.token == nil || *selection.token != token {
		t.Fatalf("token = %#v, want %q", selection.token, token)
	}
	if selection.expectedAmountRaw != "10000000" {
		t.Fatalf("expected raw = %q, want 10000000", selection.expectedAmountRaw)
	}
	if selection.price != "1" || selection.priceSource != "price_oracle" {
		t.Fatalf("quote = %q/%q, want 1/price_oracle", selection.price, selection.priceSource)
	}
	if !selection.quoteExpiresAt.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("quote expires = %s, want %s", selection.quoteExpiresAt, now.Add(15*time.Minute))
	}
}

func TestHandleCheckoutSelectAssetDonationDoesNotQuote(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	chainID := constants.Ethereum
	future := time.Now().Add(10 * time.Minute)
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "donation-token",
		LinkType:     models.PaymentLinkTypeDonation,
		Amount:       "0",
		Status:       models.PaymentStatusPending,
		ExpiresAt:    &future,
		Wallet: models.Wallet{
			EthereumAddress: "0x2222222222222222222222222222222222222222",
		},
	}
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
	app := fiber.New()
	app.Post("/checkout/:token/select", HandleCheckoutSelectAsset(PaymentHandlerDeps{
		PaymentRepo:   repo,
		AssetRegistry: registry,
		PriceOracle:   panicPriceOracle{},
	}))

	req := httptest.NewRequest(http.MethodPost, "/checkout/donation-token/select", strings.NewReader("chain_id=1&symbol=ETH"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", resp.StatusCode)
	}
	if repo.selectCalls != 1 || len(repo.quotes) != 0 {
		t.Fatalf("select/quotes = %d/%d, want 1/0", repo.selectCalls, len(repo.quotes))
	}
	if session.SelectedChainID == nil || *session.SelectedChainID != chainID || session.ExpectedAmountRaw != "" || session.Status != models.PaymentStatusAwaitingPayment {
		t.Fatalf("donation session selection = %#v", session)
	}
}

func TestPreparePaymentCreateAssetSelectionRejectsUnsupportedAsset(t *testing.T) {
	orderID := "order-1"
	amount := "10"
	currency := "USD"
	chainID := int64(constants.Ethereum)
	symbol := "USDT"
	params := types.PaymentCreateParams{
		OrderID:  &orderID,
		Amount:   &amount,
		Currency: &currency,
		ChainID:  &chainID,
		Symbol:   &symbol,
	}
	if err := params.Validate(); err != nil {
		t.Fatal(err)
	}

	_, err := preparePaymentCreateAssetSelection(
		context.Background(),
		asset.NewRegistry(),
		fakePriceOracle{prices: map[string]string{"USDT|USD": "1"}},
		params,
		time.Now().Add(30*time.Minute),
		time.Now(),
	)
	if err == nil {
		t.Fatal("unsupported selected asset should fail before create mutation")
	}
}

func TestPreparePaymentCreateAssetSelectionRequiresTokenForNonNativeAsset(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewERC20(
		constants.Ethereum,
		"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
		"USDC",
		"USD Coin",
		6,
	))

	orderID := "order-1"
	amount := "10"
	currency := "USD"
	chainID := int64(constants.Ethereum)
	symbol := "USDC"
	params := types.PaymentCreateParams{
		OrderID:  &orderID,
		Amount:   &amount,
		Currency: &currency,
		ChainID:  &chainID,
		Symbol:   &symbol,
	}
	if err := params.Validate(); err != nil {
		t.Fatal(err)
	}

	_, err := preparePaymentCreateAssetSelection(
		context.Background(),
		registry,
		fakePriceOracle{prices: map[string]string{"USDC|USD": "1"}},
		params,
		time.Now().Add(30*time.Minute),
		time.Now(),
	)
	if err == nil {
		t.Fatal("non-native selected asset should require token identifier")
	}
}

func TestPaymentCreateResponseBodyUsesV1EnvelopeAndAssetFields(t *testing.T) {
	chainID := constants.Ethereum
	token := "0xdAC17F958D2ee523a2206206994597C13D831ec7"
	expiresAt := time.Date(2026, 6, 27, 12, 30, 0, 0, time.UTC)
	sessionID := uuid.New()
	walletID := uuid.New()
	session := models.PaymentSession{
		ID:                sessionID,
		SessionToken:      "track-1",
		WalletID:          walletID,
		OrderID:           "order-1",
		Amount:            "10",
		Currency:          "USD",
		Status:            models.PaymentStatusAwaitingPayment,
		SelectedChainID:   &chainID,
		SelectedToken:     &token,
		SelectedSymbol:    "USDT",
		SelectedDecimals:  6,
		ExpectedAmountRaw: "10000000",
		DepositAddress:    "0x2222222222222222222222222222222222222222",
		ExpiresAt:         &expiresAt,
	}

	response := paymentCreateResponseBody(paymentCreateModeV1, session, "https://pay.example/checkout/track-1", walletID, expiresAt.Add(-time.Minute))
	if response["result"] != "ok" {
		t.Fatalf("result = %#v, want ok", response["result"])
	}
	data, ok := response["data"].(fiber.Map)
	if !ok {
		t.Fatalf("data type = %T, want fiber.Map", response["data"])
	}
	if data["payment_id"] != sessionID.String() {
		t.Fatalf("payment_id = %#v, want %s", data["payment_id"], sessionID)
	}
	if data["track_id"] != "track-1" || data["session_token"] != "track-1" {
		t.Fatalf("track/session token mismatch: %#v", data)
	}
	if data["link_type"] != models.PaymentLinkTypeFixed {
		t.Fatalf("link_type = %#v, want fixed", data["link_type"])
	}
	if data["chain_id"] != int64(constants.Ethereum) {
		t.Fatalf("chain_id = %#v, want %d", data["chain_id"], constants.Ethereum)
	}
	if data["token"] != token {
		t.Fatalf("token = %#v, want %s", data["token"], token)
	}
	if data["expected_amount_raw"] != "10000000" || data["deposit_address"] == "" {
		t.Fatalf("asset response fields missing: %#v", data)
	}

	legacy := paymentCreateResponseBody(paymentCreateModeLegacy, session, "https://pay.example/checkout/track-1", walletID, expiresAt.Add(-time.Minute))
	if legacy["link_type"] != models.PaymentLinkTypeFixed {
		t.Fatalf("legacy link_type = %#v, want fixed", legacy["link_type"])
	}
}

func TestPaymentSessionResponseStatusExpiresPendingSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(-time.Minute)
	session := models.PaymentSession{
		Status:    models.PaymentStatusPending,
		ExpiresAt: &expiresAt,
	}
	if got := paymentSessionResponseStatus(session, now); got != models.PaymentStatusExpired {
		t.Fatalf("status = %q, want expired", got)
	}

	session.Status = models.PaymentStatusPaid
	if got := paymentSessionResponseStatus(session, now); got != models.PaymentStatusPaid {
		t.Fatalf("paid status = %q, want paid", got)
	}
}

func TestDonationSessionDoesNotExpireFromExpiresAt(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(-time.Minute)
	session := models.PaymentSession{
		Status:    models.PaymentStatusAwaitingPayment,
		LinkType:  models.PaymentLinkTypeDonation,
		ExpiresAt: &expiresAt,
	}
	if got := paymentSessionResponseStatus(session, now); got != models.PaymentStatusAwaitingPayment {
		t.Fatalf("donation status = %q, want awaiting_payment", got)
	}
	if isSessionExpired(&session) {
		t.Fatal("donation session must not be treated as expired")
	}
	if got := checkoutExpiresAtUnix(&session); got != 0 {
		t.Fatalf("donation checkout expiry unix = %d, want 0", got)
	}

	session.Status = models.PaymentStatusExpired
	session.SelectedChainID = nil
	session.SelectedSymbol = ""
	session.DepositAddress = ""
	if got := paymentSessionResponseStatus(session, now); got != models.PaymentStatusPending {
		t.Fatalf("expired donation without asset status = %q, want pending", got)
	}

	chainID := constants.Ethereum
	session.SelectedChainID = &chainID
	session.SelectedSymbol = "ETH"
	session.DepositAddress = "0x2222222222222222222222222222222222222222"
	if got := paymentSessionResponseStatus(session, now); got != models.PaymentStatusAwaitingPayment {
		t.Fatalf("expired donation with asset status = %q, want awaiting_payment", got)
	}
	if paymentSessionTerminal(session) {
		t.Fatal("expired donation must not be terminal for checkout")
	}
}

func TestCheckoutPayerStateMapsLifecycleStates(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)
	past := now.Add(-time.Minute)
	txHash := "0xabc"

	tests := []struct {
		name     string
		session  models.PaymentSession
		status   string
		paid     bool
		terminal bool
		payable  bool
		mode     string
	}{
		{
			name: "pending asset selection",
			session: models.PaymentSession{
				Status:    models.PaymentStatusPending,
				ExpiresAt: &future,
			},
			status: checkoutStatePending,
			mode:   "detecting",
		},
		{
			name: "active awaiting payment",
			session: models.PaymentSession{
				Status:    models.PaymentStatusAwaitingPayment,
				ExpiresAt: &future,
			},
			status:  checkoutStateActive,
			payable: true,
			mode:    "detecting",
		},
		{
			name: "confirming when chain transaction exists but not paid",
			session: models.PaymentSession{
				Status:    models.PaymentStatusAwaitingPayment,
				TxHash:    &txHash,
				ExpiresAt: &future,
			},
			status:  checkoutStateConfirming,
			payable: true,
			mode:    "confirming",
		},
		{
			name: "paid stays terminal after expiry",
			session: models.PaymentSession{
				Status:    models.PaymentStatusPaid,
				ExpiresAt: &past,
			},
			status:   checkoutStatePaid,
			paid:     true,
			terminal: true,
			mode:     "paid",
		},
		{
			name: "expired nonterminal",
			session: models.PaymentSession{
				Status:    models.PaymentStatusPending,
				ExpiresAt: &past,
			},
			status:   checkoutStateExpired,
			terminal: true,
			mode:     "expired",
		},
		{
			name: "reorg failed terminal with tx hash",
			session: models.PaymentSession{
				Status:               models.PaymentStatusFailed,
				TxHash:               &txHash,
				PaymentOutcomeReason: "matched transaction was reorged",
				ExpiresAt:            &future,
			},
			status:   checkoutStateFailed,
			terminal: true,
			mode:     "failed",
		},
		{
			name: "underpaid terminal",
			session: models.PaymentSession{
				Status:    models.PaymentStatusUnderpaid,
				ExpiresAt: &future,
			},
			status:   checkoutStateUnderpaid,
			terminal: true,
			mode:     "underpaid",
		},
		{
			name: "overpaid terminal",
			session: models.PaymentSession{
				Status:    models.PaymentStatusOverpaid,
				ExpiresAt: &future,
			},
			status:   checkoutStateOverpaid,
			terminal: true,
			mode:     "overpaid",
		},
		{
			name: "partial paid terminal",
			session: models.PaymentSession{
				Status:    models.PaymentStatusPartialPaid,
				ExpiresAt: &future,
			},
			status:   checkoutStatePartialPaid,
			terminal: true,
			mode:     "partial_paid",
		},
		{
			name: "aggregate partial remains payable",
			session: models.PaymentSession{
				Status:         models.PaymentStatusPartialPaid,
				PaymentOutcome: models.PaymentOutcomePartialAggregating,
				ExpiresAt:      &future,
			},
			status:  checkoutStatePartialPaid,
			payable: true,
			mode:    "partial_paid",
		},
		{
			name: "expired aggregate partial becomes terminal expired",
			session: models.PaymentSession{
				Status:         models.PaymentStatusPartialPaid,
				PaymentOutcome: models.PaymentOutcomePartialAggregating,
				ExpiresAt:      &past,
			},
			status:   checkoutStateExpired,
			terminal: true,
			mode:     "expired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := checkoutPayerState(tt.session, now)
			if state.Status != tt.status || state.Paid != tt.paid || state.Terminal != tt.terminal || state.Payable != tt.payable || state.Mode != tt.mode {
				t.Fatalf("state = %#v, want status=%q paid=%v terminal=%v payable=%v mode=%q", state, tt.status, tt.paid, tt.terminal, tt.payable, tt.mode)
			}
		})
	}
}

func TestCheckoutStatusPayloadUsesSafePayerState(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)
	txHash := "0xabc"
	session := models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusAwaitingPayment,
		TxHash:       &txHash,
		ExpiresAt:    &future,
	}

	payload := checkoutStatusPayload(session, now)
	if payload["success"] != true || payload["status"] != checkoutStateConfirming || payload["paid"] != false {
		t.Fatalf("payload status = %#v", payload)
	}
	if payload["payment_id"] != session.ID.String() || payload["tx_hash"] != txHash {
		t.Fatalf("payload identifiers = %#v", payload)
	}
	if payload["success_path"] != "/checkout/checkout-token/return/success" || payload["cancel_path"] != "/checkout/checkout-token/cancel" {
		t.Fatalf("payload paths = %#v", payload)
	}
	if payload["terminal"] != false || payload["payable"] != true {
		t.Fatalf("payload state flags = %#v", payload)
	}
	if _, ok := payload["result_path"]; ok {
		t.Fatalf("non-terminal payload should not include result_path: %#v", payload)
	}
	failedSession := session
	failedSession.Status = models.PaymentStatusFailed
	failedSession.TxHash = nil
	failedPayload := checkoutStatusPayload(failedSession, now)
	if failedPayload["status"] != checkoutStateFailed || failedPayload["terminal"] != true || failedPayload["result_path"] != "/checkout/checkout-token/pay" {
		t.Fatalf("failed payload = %#v", failedPayload)
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "diagnostics", "webhook_last_error"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("payload leaked forbidden field %q: %#v", forbidden, payload)
		}
	}
}

func TestCheckoutRealtimeEventUsesSafePayerState(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)
	txHash := "0xabc"
	session := models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusAwaitingPayment,
		TxHash:       &txHash,
		ExpiresAt:    &future,
	}

	event := checkoutRealtimeEvent(session, now)
	if event.Status != checkoutStateConfirming || event.Paid || !event.Payable || event.Terminal {
		t.Fatalf("event state = %#v", event)
	}
	if event.PaymentID != session.ID.String() || event.TxHash != txHash {
		t.Fatalf("event identifiers = %#v", event)
	}
	if event.ResultPath != "" {
		t.Fatalf("non-terminal event result path = %q, want empty", event.ResultPath)
	}

	partial := session
	partial.Status = models.PaymentStatusPartialPaid
	partial.PaymentOutcome = models.PaymentOutcomePartialAggregating
	partial.TxHash = nil
	event = checkoutRealtimeEvent(partial, now)
	if event.Status != checkoutStatePartialPaid || event.Paid || !event.Payable || event.Terminal {
		t.Fatalf("aggregate partial event = %#v, want payable nonterminal", event)
	}
	if event.ResultPath != "" {
		t.Fatalf("aggregate partial event result path = %q, want empty", event.ResultPath)
	}

	partial.PaymentOutcome = models.PaymentOutcomePartialUnsupported
	event = checkoutRealtimeEvent(partial, now)
	if event.Status != checkoutStatePartialPaid || event.Paid || event.Payable || !event.Terminal {
		t.Fatalf("terminal partial event = %#v, want terminal non-payable", event)
	}
	if event.ResultPath != "/checkout/checkout-token/pay" {
		t.Fatalf("terminal partial event result path = %q", event.ResultPath)
	}
}

func TestHandleCheckoutStatusUsesPayerStatePayload(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	txHash := "0xabc"
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusAwaitingPayment,
		TxHash:       &txHash,
		ExpiresAt:    &future,
	}
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
	app := fiber.New()
	app.Get("/checkout/:token/status.json", HandleCheckoutStatus(PaymentHandlerDeps{PaymentRepo: repo}))

	req := httptest.NewRequest(http.MethodGet, "/checkout/checkout-token/status.json", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != checkoutStateConfirming || body["paid"] != false {
		t.Fatalf("body state = %#v", body)
	}
	if body["payment_id"] != session.ID.String() || body["tx_hash"] != txHash {
		t.Fatalf("body identifiers = %#v", body)
	}
}

func TestHandleCheckoutStatusDoesNotReportExpiredWhenPersistenceFails(t *testing.T) {
	expiredAt := time.Now().Add(-time.Minute)
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusAwaitingPayment,
		ExpiresAt:    &expiredAt,
	}
	repo := &fakePaymentSessionRepo{
		sessions:  []*models.PaymentSession{session},
		expireErr: errors.New("database unavailable"),
	}
	app := fiber.New()
	app.Get("/checkout/:token/status.json", HandleCheckoutStatus(PaymentHandlerDeps{PaymentRepo: repo}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/checkout-token/status.json", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if repo.expireCalls != 1 {
		t.Fatalf("expire calls = %d, want 1", repo.expireCalls)
	}
	if session.Status != models.PaymentStatusAwaitingPayment {
		t.Fatalf("in-memory status = %q, want unchanged awaiting_payment", session.Status)
	}
}

func TestHandleCheckoutRendersTerminalStateWithoutPayLoop(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusCanceled,
		ExpiresAt:    &future,
	}
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
	app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
	app.Get("/checkout/:token", HandleCheckout(PaymentHandlerDeps{PaymentRepo: repo}))
	app.Get("/checkout/:token/pay", HandleCheckoutPay(PaymentHandlerDeps{PaymentRepo: repo}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/checkout-token", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("checkout status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{"Iptal edildi", "Gateway Pay", "crypto-flow-select", "crypto-lang-switch", "crypto-result-panel"} {
		if !strings.Contains(html, want) {
			t.Fatalf("terminal checkout missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, "/checkout/checkout-token/pay") || strings.Contains(html, "checkout.js") || strings.Contains(html, "Crypto Checkout") {
		t.Fatalf("terminal checkout rendered unexpected page:\n%s", html)
	}

	payResp, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/checkout-token/pay", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer payResp.Body.Close()
	if payResp.StatusCode != fiber.StatusOK {
		t.Fatalf("pay status = %d, want 200", payResp.StatusCode)
	}
	payBody, err := io.ReadAll(payResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	payHTML := string(payBody)
	if !strings.Contains(payHTML, "Iptal edildi") || strings.Contains(payHTML, "data-checkout-status") || strings.Contains(payHTML, "status.json") {
		t.Fatalf("terminal pay rendered polling page:\n%s", payHTML)
	}
}

func TestCheckoutMutationHandlersRejectGETWithoutMutation(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	for _, tt := range []struct {
		name    string
		path    string
		handler func(PaymentHandlerDeps) fiber.Handler
	}{
		{name: "select", path: "/checkout/checkout-token/select", handler: HandleCheckoutSelectAsset},
		{name: "change", path: "/checkout/checkout-token/change", handler: HandleCheckoutChangeAsset},
		{name: "cancel", path: "/checkout/checkout-token/cancel", handler: HandleCheckoutCancel},
	} {
		t.Run(tt.name, func(t *testing.T) {
			session := &models.PaymentSession{
				ID:           uuid.New(),
				SessionToken: "checkout-token",
				Status:       models.PaymentStatusAwaitingPayment,
				ExpiresAt:    &future,
			}
			repo := &fakeCheckoutMutationRepo{fakePaymentSessionRepo: &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}}
			app := fiber.New()
			app.Get(tt.path, tt.handler(PaymentHandlerDeps{PaymentRepo: repo}))

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, tt.path, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != fiber.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", resp.StatusCode)
			}
			if got := resp.Header.Get("Allow"); got != fiber.MethodPost {
				t.Fatalf("Allow = %q, want POST", got)
			}
			if repo.resetCalls != 0 || repo.cancelCalls != 0 {
				t.Fatalf("GET mutated checkout: reset=%d cancel=%d", repo.resetCalls, repo.cancelCalls)
			}
		})
	}
}

func TestHandleCheckoutChangeAssetPostPreservesLanguage(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	for _, tt := range []struct {
		name   string
		path   string
		cookie string
	}{
		{name: "query language", path: "/checkout/checkout-token/change?lang=en"},
		{name: "cookie language", path: "/checkout/checkout-token/change", cookie: "gateway_lang=en"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			session := &models.PaymentSession{
				ID:                uuid.New(),
				SessionToken:      "checkout-token",
				Status:            models.PaymentStatusAwaitingPayment,
				SelectedSymbol:    "ETH",
				ExpectedAmountRaw: "1000000000000000000",
				DepositAddress:    "0x2222222222222222222222222222222222222222",
				ExpiresAt:         &future,
			}
			repo := &fakeCheckoutMutationRepo{fakePaymentSessionRepo: &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}}
			app := fiber.New()
			app.Post("/checkout/:token/change", HandleCheckoutChangeAsset(PaymentHandlerDeps{PaymentRepo: repo}))
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			if tt.cookie != "" {
				req.Header.Set("Cookie", tt.cookie)
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != fiber.StatusSeeOther {
				t.Fatalf("status = %d, want 303", resp.StatusCode)
			}
			if got := resp.Header.Get("Location"); got != "/checkout/checkout-token?lang=en" {
				t.Fatalf("Location = %q", got)
			}
			if !strings.Contains(strings.Join(resp.Header.Values("Set-Cookie"), ";"), "gateway_lang=en") {
				t.Fatalf("language cookie missing: %v", resp.Header.Values("Set-Cookie"))
			}
			if repo.resetCalls != 1 || session.Status != models.PaymentStatusPending {
				t.Fatalf("reset calls/status = %d/%s", repo.resetCalls, session.Status)
			}
		})
	}
}

func TestHandleCheckoutCancelPostPreservesLanguageBeforeMerchantRedirect(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusAwaitingPayment,
		CancelURL:    "https://merchant.example/payment-canceled",
		ExpiresAt:    &future,
	}
	repo := &fakeCheckoutMutationRepo{fakePaymentSessionRepo: &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}}
	app := fiber.New()
	app.Post("/checkout/:token/cancel", HandleCheckoutCancel(PaymentHandlerDeps{PaymentRepo: repo}))
	req := httptest.NewRequest(http.MethodPost, "/checkout/checkout-token/cancel?lang=en", nil)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusSeeOther || resp.Header.Get("Location") != session.CancelURL {
		t.Fatalf("status/location = %d/%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if !strings.Contains(strings.Join(resp.Header.Values("Set-Cookie"), ";"), "gateway_lang=en") {
		t.Fatalf("language cookie missing: %v", resp.Header.Values("Set-Cookie"))
	}
	if repo.cancelCalls != 1 || session.Status != models.PaymentStatusCanceled {
		t.Fatalf("cancel calls/status = %d/%s", repo.cancelCalls, session.Status)
	}
}

func TestHandleCheckoutCancelDoesNotReportSuccessWhenRepositoryFails(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusAwaitingPayment,
		CancelURL:    "https://merchant.example/payment-canceled",
		ExpiresAt:    &future,
	}
	repo := &fakeCheckoutMutationRepo{
		fakePaymentSessionRepo: &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}},
		cancelErr:              errors.New("database unavailable"),
	}
	app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
	app.Post("/checkout/:token/cancel", HandleCheckoutCancel(PaymentHandlerDeps{PaymentRepo: repo}))

	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/checkout/checkout-token/cancel?lang=en", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); location != "" {
		t.Fatalf("failed cancellation redirected as success to %q", location)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Could not cancel this checkout.") {
		t.Fatalf("failure page missing cancellation error:\n%s", body)
	}
	if repo.cancelCalls != 1 || session.Status != models.PaymentStatusAwaitingPayment {
		t.Fatalf("failed cancellation mutated session: calls=%d status=%s", repo.cancelCalls, session.Status)
	}
}

func TestCheckoutRedirectsPreserveLanguage(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	tests := []struct {
		name       string
		method     string
		route      string
		requestURL string
		cookie     string
		status     string
		handler    func(PaymentHandlerDeps) fiber.Handler
		want       string
	}{
		{
			name:       "selected checkout query to pay",
			method:     http.MethodGet,
			route:      "/checkout/:token",
			requestURL: "/checkout/checkout-token?lang=en",
			status:     models.PaymentStatusAwaitingPayment,
			handler:    HandleCheckout,
			want:       "/checkout/checkout-token/pay?lang=en",
		},
		{
			name:       "pending pay cookie to checkout",
			method:     http.MethodGet,
			route:      "/checkout/:token/pay",
			requestURL: "/checkout/checkout-token/pay",
			cookie:     "gateway_lang=en",
			status:     models.PaymentStatusPending,
			handler:    HandleCheckoutPay,
			want:       "/checkout/checkout-token?lang=en",
		},
		{
			name:       "nonpaid success return query to pay",
			method:     http.MethodGet,
			route:      "/checkout/:token/return/success",
			requestURL: "/checkout/checkout-token/return/success?lang=en",
			status:     models.PaymentStatusAwaitingPayment,
			handler:    HandleCheckoutSuccessReturn,
			want:       "/checkout/checkout-token/pay?lang=en",
		},
		{
			name:       "paid change query to success",
			method:     http.MethodPost,
			route:      "/checkout/:token/change",
			requestURL: "/checkout/checkout-token/change?lang=en",
			status:     models.PaymentStatusPaid,
			handler:    HandleCheckoutChangeAsset,
			want:       "/checkout/checkout-token/return/success?lang=en",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &models.PaymentSession{
				ID:           uuid.New(),
				SessionToken: "checkout-token",
				Status:       tt.status,
				ExpiresAt:    &future,
			}
			repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
			app := fiber.New()
			if tt.method == http.MethodPost {
				app.Post(tt.route, tt.handler(PaymentHandlerDeps{PaymentRepo: repo}))
			} else {
				app.Get(tt.route, tt.handler(PaymentHandlerDeps{PaymentRepo: repo}))
			}
			req := httptest.NewRequest(tt.method, tt.requestURL, nil)
			if tt.cookie != "" {
				req.Header.Set("Cookie", tt.cookie)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != fiber.StatusSeeOther {
				t.Fatalf("status = %d, want 303", resp.StatusCode)
			}
			if got := resp.Header.Get("Location"); got != tt.want {
				t.Fatalf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandleCheckoutTerminalResultDoesNotPresentExpectedAmountAsPaid(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	for _, status := range []string{
		models.PaymentStatusCanceled,
		models.PaymentStatusExpired,
		models.PaymentStatusFailed,
	} {
		t.Run(status, func(t *testing.T) {
			session := &models.PaymentSession{
				ID:                uuid.New(),
				SessionToken:      "terminal-" + status,
				Status:            status,
				Amount:            "10",
				Currency:          "USD",
				SelectedSymbol:    "USDT",
				SelectedDecimals:  6,
				ExpectedAmountRaw: "10000000",
				ExpiresAt:         &future,
			}
			repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
			app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
			app.Get("/checkout/:token", HandleCheckout(PaymentHandlerDeps{PaymentRepo: repo}))

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/"+session.SessionToken+"?lang=en", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("checkout status = %d, want 200", resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			html := string(body)
			for _, forbidden := range []string{"crypto-result-amount-panel", "Paid amount", "10 USDT"} {
				if strings.Contains(html, forbidden) {
					t.Fatalf("%s result presents expected amount as paid through %q:\n%s", status, forbidden, html)
				}
			}
		})
	}
}

func TestHandleCheckoutPaidResultUsesLocalizedStatusLabel(t *testing.T) {
	chainID := constants.Avalanche
	session := &models.PaymentSession{
		ID:               uuid.New(),
		SessionToken:     "paid-checkout-token",
		Status:           models.PaymentStatusPaid,
		SelectedChainID:  &chainID,
		SelectedSymbol:   "AVAX",
		SelectedDecimals: 18,
		MatchedAmountRaw: "1000000000000000000000",
	}
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
	app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
	app.Get("/checkout/:token", HandleCheckout(PaymentHandlerDeps{PaymentRepo: repo}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/paid-checkout-token?lang=tr", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("checkout status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"Ödendi",
		"Ödeme başarıyla alındı.",
		"Tamamlandı",
		"1000 AVAX",
		"Avalanche",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("paid checkout result missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, ">paid<") {
		t.Fatalf("paid checkout result must not expose raw payment status:\n%s", html)
	}
	if strings.Contains(html, "crypto-result-chip") {
		t.Fatalf("paid checkout result must not render result summary chip:\n%s", html)
	}
}

func TestCheckoutSuccessReturnUsesCheckoutResultDesign(t *testing.T) {
	productID := uuid.New()
	session := &models.PaymentSession{
		ID:             uuid.New(),
		SessionToken:   "paid-token",
		ProductID:      productID.String(),
		OrderID:        "order-paid",
		Amount:         "25",
		Currency:       "USD",
		Status:         models.PaymentStatusPaid,
		SelectedSymbol: "ETH",
	}
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
	productRepo := fakePaymentProductRepo{product: &models.Product{
		ID:      productID,
		Name:    "Premium Plan",
		LogoURL: "/uploads/product-logo.svg",
	}}
	app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
	app.Get("/checkout/:token/return/success", HandleCheckoutSuccessReturn(PaymentHandlerDeps{PaymentRepo: repo, ProductRepo: productRepo}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/paid-token/return/success?lang=en", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("success return status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"Payment complete",
		"Gateway Pay",
		"crypto-payment-result-page crypto-flow-select",
		"crypto-lang-switch",
		"crypto-product-strip compact crypto-result-product-strip",
		"crypto-product-logo compact",
		"/uploads/product-logo.svg",
		"Premium Plan",
		"/checkout/paid-token/return/success?lang=tr",
		"crypto-result-panel",
		"Completed",
		"order-paid",
		"25 USD",
		"ETH",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("success result page missing %q in:\n%s", want, html)
		}
	}
	for _, forbidden := range []string{"Crypto Checkout", "checkout.js", "status.json", "crypto-result-chip"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("success result page contains legacy/polling token %q in:\n%s", forbidden, html)
		}
	}
	if strings.Contains(html, ">paid<") {
		t.Fatalf("success result page must not expose raw payment status:\n%s", html)
	}

	respTR, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/paid-token/return/success?lang=tr", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer respTR.Body.Close()
	trBody, err := io.ReadAll(respTR.Body)
	if err != nil {
		t.Fatal(err)
	}
	trHTML := string(trBody)
	for _, want := range []string{
		"Ödeme tamamlandı",
		"Ödeme başarıyla alındı.",
		"Ödeme durumu",
		"Tamamlandı",
		"Yatırılan tutar",
		"İşlem detayları",
		"/uploads/product-logo.svg",
		"Dekont yazdır",
		"Checkout'a dön",
	} {
		if !strings.Contains(trHTML, want) {
			t.Fatalf("TR success result page missing %q in:\n%s", want, trHTML)
		}
	}
	if strings.Contains(trHTML, ">paid<") {
		t.Fatalf("TR success result page must not expose raw payment status:\n%s", trHTML)
	}
}

func TestCheckoutSuccessReturnUsesMatchedDonationAmountAndAssetLogo(t *testing.T) {
	chainID := constants.Ethereum
	session := &models.PaymentSession{
		ID:               uuid.New(),
		SessionToken:     "donation-paid-token",
		LinkType:         models.PaymentLinkTypeDonation,
		Status:           models.PaymentStatusPaid,
		SelectedChainID:  &chainID,
		SelectedSymbol:   "ETH",
		SelectedDecimals: 18,
		MatchedAmountRaw: "1230000000000000000",
	}
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
	app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
	app.Get("/checkout/:token/return/success", HandleCheckoutSuccessReturn(PaymentHandlerDeps{PaymentRepo: repo}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/donation-paid-token/return/success?lang=tr", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"1.23 ETH",
		"/static/coins/eth.svg",
		"Ethereum",
		"Dekont yazdır",
		"crypto-checkmark-path",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("donation success result missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, "Ödeyen belirler") || strings.Contains(html, "Payer decides") {
		t.Fatalf("donation success result must show matched amount, not payer-decides copy:\n%s", html)
	}
}

func TestPayTemplateRendersStateCopyAndContractFields(t *testing.T) {
	tmpl, err := template.ParseFiles("../../views/gateway/pay.html")
	if err != nil {
		t.Fatal(err)
	}
	chainID := constants.Ethereum
	txHash := "0xabc"
	session := models.PaymentSession{
		SessionToken:      "checkout-token",
		OrderID:           "order-1",
		Amount:            "1",
		Currency:          "USD",
		Status:            models.PaymentStatusAwaitingPayment,
		SelectedChainID:   &chainID,
		SelectedSymbol:    "ETH",
		SelectedDecimals:  18,
		ExpectedAmountRaw: "500000000000000",
		DepositAddress:    "0x2222222222222222222222222222222222222222",
		TxHash:            &txHash,
	}
	state := checkoutPayerState(session, time.Now())
	var rendered bytes.Buffer
	err = tmpl.Execute(&rendered, fiber.Map{
		"Session":       &session,
		"Lang":          "en",
		"IsEnglish":     true,
		"QRCodeURL":     "/checkout/checkout-token/qr.png",
		"PaymentURI":    paymentURI(session),
		"ChainName":     constants.ChainName(chainID),
		"AmountDisplay": formatPaymentAmount(session.ExpectedAmountRaw, session.SelectedDecimals, session.SelectedSymbol),
		"CheckoutState": state,
		"StatusMode":    state.Mode,
		"StatusTitle":   state.TitleEN,
		"StatusBody":    state.BodyEN,
	})
	if err != nil {
		t.Fatal(err)
	}
	html := rendered.String()
	for _, want := range []string{
		"0.0005 ETH",
		"0x2222222222222222222222222222222222222222",
		"/checkout/checkout-token/qr.png",
		"data-copy-target=\"depositAddress\"",
		"Payment confirming",
		"data-checkout-status=\"confirming\"",
		"crypto-lang-options",
		"crypto-lang-flag",
		"crypto-lang-name",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered pay template missing %q in:\n%s", want, html)
		}
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "diagnostics"} {
		if strings.Contains(strings.ToLower(html), forbidden) {
			t.Fatalf("rendered pay template leaked forbidden string %q", forbidden)
		}
	}

	terminal := checkoutPaymentState{
		Status:   checkoutStateUnderpaid,
		Mode:     "underpaid",
		TitleEN:  "Underpaid",
		BodyEN:   "The received amount is below the expected amount.",
		Terminal: true,
	}
	rendered.Reset()
	err = tmpl.Execute(&rendered, fiber.Map{
		"Session":       &session,
		"Lang":          "en",
		"IsEnglish":     true,
		"QRCodeURL":     "/checkout/checkout-token/qr.png",
		"PaymentURI":    paymentURI(session),
		"ChainName":     constants.ChainName(chainID),
		"AmountDisplay": formatPaymentAmount(session.ExpectedAmountRaw, session.SelectedDecimals, session.SelectedSymbol),
		"CheckoutState": terminal,
		"StatusMode":    terminal.Mode,
		"StatusTitle":   terminal.TitleEN,
		"StatusBody":    terminal.BodyEN,
	})
	if err != nil {
		t.Fatal(err)
	}
	html = rendered.String()
	if !strings.Contains(html, "data-checkout-status=\"underpaid\"") || !strings.Contains(html, "Underpaid") {
		t.Fatalf("terminal pay template missing underpaid state:\n%s", html)
	}
	for _, forbidden := range []string{"data-copy-target=\"depositAddress\"", "/checkout/checkout-token/qr.png", "Send exactly"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("terminal pay template still looks payable through %q:\n%s", forbidden, html)
		}
	}
}

func TestPayTemplateRealtimeUsesServerAvailabilityForExceptionOutcomes(t *testing.T) {
	body, err := os.ReadFile("../../views/gateway/pay.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		`var isTerminal = data.terminal === true;`,
		`var isPayable = data.payable === true;`,
		`setPaymentAvailability(isPayable, isTerminal);`,
		`data.status === "partial_paid" && isPayable && !isTerminal`,
		`if (isTerminal) {`,
		"Overpaid",
		"Partial payment received",
		"The received amount is above the expected amount",
		"Send the remaining amount to complete the payment",
		`window.location.href = data.result_path;`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("pay template realtime availability handling missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`var terminalModes = ["paid", "expired", "canceled", "failed", "underpaid", "overpaid", "partial_paid"];`,
		`data.status === "expired" || data.status === "canceled" || data.status === "failed" || data.status === "underpaid" || data.status === "overpaid" || data.status === "partial_paid"`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("pay template must not infer terminal state from status using %q", forbidden)
		}
	}
}

func TestCheckoutResultAmountDisplayDoesNotPresentExpectedAmountAsPaid(t *testing.T) {
	tests := []struct {
		name    string
		session models.PaymentSession
		want    string
	}{
		{
			name: "canceled token checkout without receipt",
			session: models.PaymentSession{
				Status:            models.PaymentStatusCanceled,
				Amount:            "10",
				Currency:          "USD",
				SelectedSymbol:    "USDT",
				SelectedDecimals:  6,
				ExpectedAmountRaw: "10000000",
			},
		},
		{
			name: "expired fiat checkout without receipt",
			session: models.PaymentSession{
				Status:   models.PaymentStatusExpired,
				Amount:   "25",
				Currency: "USD",
			},
		},
		{
			name: "failed token checkout without receipt",
			session: models.PaymentSession{
				Status:            models.PaymentStatusFailed,
				SelectedSymbol:    "USDT",
				SelectedDecimals:  6,
				ExpectedAmountRaw: "10000000",
				MatchedAmountRaw:  "2500000",
			},
		},
		{
			name: "unsuccessful checkout with received amount",
			session: models.PaymentSession{
				Status:            models.PaymentStatusUnderpaid,
				SelectedSymbol:    "USDT",
				SelectedDecimals:  6,
				ExpectedAmountRaw: "10000000",
				MatchedAmountRaw:  "2500000",
			},
			want: "2.5 USDT",
		},
		{
			name: "paid token checkout may use expected amount fallback",
			session: models.PaymentSession{
				Status:            models.PaymentStatusPaid,
				SelectedSymbol:    "USDT",
				SelectedDecimals:  6,
				ExpectedAmountRaw: "10000000",
			},
			want: "10 USDT",
		},
		{
			name: "paid fiat checkout may use requested amount fallback",
			session: models.PaymentSession{
				Status:   models.PaymentStatusPaid,
				Amount:   "25",
				Currency: "USD",
			},
			want: "25 USD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkoutResultAmountDisplay(&tt.session); got != tt.want {
				t.Fatalf("checkoutResultAmountDisplay() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPaymentResultTemplateRendersCopyableReceiptReferences(t *testing.T) {
	tmpl, err := template.ParseFiles("../../views/gateway/payment_result.html")
	if err != nil {
		t.Fatal(err)
	}
	var rendered bytes.Buffer
	err = tmpl.Execute(&rendered, fiber.Map{
		"Title":                "Payment complete",
		"Message":              "Payment received successfully.",
		"Status":               models.PaymentStatusPaid,
		"StatusLabel":          "Completed",
		"ResultKind":           "success",
		"IsEnglish":            true,
		"CheckoutURL":          "/checkout/result-token?lang=en",
		"LangTRURL":            "/checkout/result-token?lang=tr",
		"LangENURL":            "/checkout/result-token?lang=en",
		"OrderID":              "order-1",
		"AmountDisplay":        "10 ETH",
		"Currency":             "USD",
		"SelectedSymbol":       "ETH",
		"SelectedAssetLogoURL": "/static/coins/eth.svg",
		"ReceiptRefs": []fiber.Map{
			{"ID": "receiptPaymentID", "Label": "Payment ID", "Value": "payment-1"},
			{"ID": "receiptSessionToken", "Label": "Track ID", "Value": "track-1"},
			{"ID": "receiptTxHash", "Label": "Transaction", "Value": "0xabc"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	html := rendered.String()
	for _, want := range []string{
		"Gateway Pay",
		"crypto-payment-result-page crypto-flow-select",
		"crypto-lang-switch",
		"crypto-result-panel",
		"crypto-result-amount-panel",
		"crypto-result-asset-mark",
		"crypto-result-meta-grid",
		"crypto-checkmark-path",
		"Completed",
		"/static/coins/eth.svg",
		"Receipt references",
		`id="receiptPaymentID"`,
		`data-copy-target="receiptPaymentID"`,
		`data-print-receipt`,
		`window.print()`,
		`id="receiptSessionToken"`,
		`data-copy-target="receiptTxHash"`,
		`document.querySelectorAll("[data-copy-target]")`,
		"/checkout/result-token?lang=tr",
		"order-1",
		"10 ETH",
		"ETH",
		"0xabc",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("payment result template missing %q in:\n%s", want, html)
		}
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "diagnostics", "Crypto Checkout", "checkout.js", "crypto-result-chip"} {
		if strings.Contains(strings.ToLower(html), strings.ToLower(forbidden)) {
			t.Fatalf("payment result template leaked forbidden string %q", forbidden)
		}
	}
}

func TestPaymentInvoiceUsesCheckoutQualityBranding(t *testing.T) {
	productID := uuid.New()
	chainID := constants.Avalanche
	paidAt := time.Date(2026, 6, 30, 5, 15, 0, 0, time.UTC)
	session := &models.PaymentSession{
		ID:              uuid.New(),
		SessionToken:    "invoice-token",
		ProductID:       productID.String(),
		OrderID:         "order-invoice-1",
		Amount:          "77",
		Currency:        "USD",
		Status:          models.PaymentStatusPaid,
		SelectedChainID: &chainID,
		SelectedSymbol:  "AVAX",
		TxHash:          stringPtr("0xabc"),
		PaidAt:          &paidAt,
		CreatedAt:       paidAt.Add(-time.Hour),
		Domain: models.Domain{
			DomainURL: "https://merchant.example",
		},
	}
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
	productRepo := fakePaymentProductRepo{product: &models.Product{
		ID:          productID,
		Name:        "Research Cave Annual",
		Description: "Premium subscription",
		LogoURL:     "/uploads/product-logo.svg",
	}}
	app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
	app.Get("/invoice/:token", HandlePaymentInvoice(PaymentHandlerDeps{PaymentRepo: repo, ProductRepo: productRepo}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/invoice/invoice-token", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("invoice status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"invoice-product-strip",
		"invoice-product-logo",
		"/uploads/product-logo.svg",
		"Research Cave Annual",
		"Premium subscription",
		"invoice-status paid",
		"Tamamlandı",
		"77 USD",
		"invoice-coin-mark",
		"/static/coins/avax.svg",
		"AVAX · Avalanche",
		"Yazdır / PDF",
		"Linki kopyala",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("invoice page missing %q in:\n%s", want, html)
		}
	}
	for _, forbidden := range []string{">paid<", "crypto-result-chip"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("invoice page must not expose %q:\n%s", forbidden, html)
		}
	}
}

func TestPaymentInvoiceUsesGatewayBrandFallbackWhenProductLogoMissing(t *testing.T) {
	productID := uuid.New()
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "invoice-token",
		ProductID:    productID.String(),
		OrderID:      "order-invoice-2",
		Amount:       "25",
		Currency:     "USD",
		Status:       models.PaymentStatusPending,
		CreatedAt:    time.Date(2026, 6, 30, 5, 15, 0, 0, time.UTC),
		Domain: models.Domain{
			DomainURL: "https://merchant.example",
		},
	}
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
	productRepo := fakePaymentProductRepo{product: &models.Product{
		ID:          productID,
		Name:        "Demo Product",
		Description: "Logo missing",
	}}
	app := fiber.New(fiber.Config{Views: fiberhtml.New("../../views", ".html")})
	app.Get("/invoice/:token", HandlePaymentInvoice(PaymentHandlerDeps{PaymentRepo: repo, ProductRepo: productRepo}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/invoice/invoice-token", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("invoice status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"invoice-product-avatar",
		`<svg viewBox="0 0 24 24">`,
		`M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z`,
		"Demo Product",
		"Ödeme bekleniyor",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("invoice page missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, ">D</div>") {
		t.Fatalf("invoice page fell back to text avatar instead of brand mark:\n%s", html)
	}
}

func TestCheckoutReceiptRefsArePayerSafe(t *testing.T) {
	txHash := "0xabc"
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "track-1",
		OrderID:      "order-1",
		TxHash:       &txHash,
	}
	refs := checkoutReceiptRefs(session)
	if len(refs) != 4 {
		t.Fatalf("receipt refs = %#v, want four safe refs", refs)
	}
	joined := strings.ToLower(fmt.Sprint(refs))
	for _, want := range []string{"payment id", "track id", "order id", "transaction", "track-1", "order-1", "0xabc"} {
		if !strings.Contains(joined, strings.ToLower(want)) {
			t.Fatalf("receipt refs missing %q: %#v", want, refs)
		}
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "diagnostics", "webhook_last_error"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("receipt refs leaked forbidden field %q: %#v", forbidden, refs)
		}
	}
}

func TestPayTemplateRendersDonationWithoutExactAmount(t *testing.T) {
	tmpl, err := template.ParseFiles("../../views/gateway/pay.html")
	if err != nil {
		t.Fatal(err)
	}
	chainID := constants.Ethereum
	session := models.PaymentSession{
		SessionToken:      "donation-token",
		OrderID:           "donation-order",
		LinkType:          models.PaymentLinkTypeDonation,
		Status:            models.PaymentStatusAwaitingPayment,
		SelectedChainID:   &chainID,
		SelectedSymbol:    "ETH",
		SelectedDecimals:  18,
		DepositAddress:    "0x2222222222222222222222222222222222222222",
		ExpectedAmountRaw: "",
	}
	state := checkoutPayerState(session, time.Now())
	var rendered bytes.Buffer
	err = tmpl.Execute(&rendered, fiber.Map{
		"Session":        &session,
		"Lang":           "en",
		"IsEnglish":      true,
		"IsDonation":     true,
		"QRCodeURL":      "/checkout/donation-token/qr.png",
		"PaymentURI":     paymentURI(session),
		"ChainName":      constants.ChainName(chainID),
		"AmountDisplay":  "ETH",
		"CheckoutState":  state,
		"StatusMode":     state.Mode,
		"StatusTitle":    state.TitleEN,
		"StatusBody":     state.BodyEN,
		"CheckoutURL":    "/checkout/donation-token?lang=en",
		"ChangeAssetURL": "/checkout/donation-token/change?lang=en",
		"CancelURL":      "/checkout/donation-token/cancel?lang=en",
		"LangTRURL":      "/checkout/donation-token/pay?lang=tr",
		"LangENURL":      "/checkout/donation-token/pay?lang=en",
	})
	if err != nil {
		t.Fatal(err)
	}
	html := rendered.String()
	for _, want := range []string{"Chosen amount ETH", "Send your chosen amount", "Choose the amount in your wallet", "No expiry", "crypto-lang-switch", "crypto-lang-options", "crypto-lang-flag", "crypto-lang-name", `aria-current="page"`, `href="/checkout/donation-token?lang=en"`, `method="post" action="/checkout/donation-token/change?lang=en"`, `method="post" action="/checkout/donation-token/cancel?lang=en"`, "/checkout/donation-token/pay?lang=tr", "ethereum:0x2222222222222222222222222222222222222222@1"} {
		if !strings.Contains(html, want) {
			t.Fatalf("donation template missing %q in:\n%s", want, html)
		}
	}
	for _, forbidden := range []string{"Exact amount", "Send exactly", "?value=", "amount=", "0 ETH", "data-countdown-unix", "crypto-progress-bar", `href="/checkout/donation-token/change?lang=en"`, `href="/checkout/donation-token/cancel?lang=en"`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("donation template contains fixed amount copy %q in:\n%s", forbidden, html)
		}
	}
}

func TestCheckoutLocalizedURLPreservesLanguageAndSelectedAsset(t *testing.T) {
	if got := checkoutLocalizedURL("token-1", "", "en", "eth"); got != "/checkout/token-1?asset=ETH&lang=en" {
		t.Fatalf("localized checkout url = %q", got)
	}
	if got := checkoutLocalizedURL("token-1", "/pay", "tr", "eth"); got != "/checkout/token-1/pay?lang=tr" {
		t.Fatalf("localized pay url = %q", got)
	}
}

func TestPaymentCreateErrorUsesV1EnvelopeForConflict(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		return paymentCreateError(c, paymentCreateModeV1, fiber.StatusConflict, "idempotency conflict")
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["result"] != "error" || body["message"] != "idempotency conflict" {
		t.Fatalf("body = %#v, want v1 error envelope", body)
	}
}

type countingPriceOracle struct {
	prices map[string]string
	calls  int
}

func (o *countingPriceOracle) Price(_ context.Context, symbol string, currency string) (*big.Rat, error) {
	o.calls++
	price, ok := o.prices[symbol+"|"+currency]
	if !ok {
		price = o.prices[symbol+"|USD"]
	}
	rat, ok := new(big.Rat).SetString(price)
	if !ok {
		return nil, errors.New("invalid test price")
	}
	return rat, nil
}

type fakePaymentWalletRepo struct {
	createCalls int
	wallets     []*models.Wallet
}

func (r *fakePaymentWalletRepo) Create(params types.WalletParams) (*models.Wallet, error) {
	r.createCalls++
	merchantID, _ := uuid.Parse(*params.MerchantId)
	domainID, _ := uuid.Parse(*params.DomainId)
	wallet := &models.Wallet{
		ID:              uuid.New(),
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       *params.ProductId,
		UserID:          *params.UserId,
		EthereumAddress: "0x2222222222222222222222222222222222222222",
		SolanaAddress:   "So11111111111111111111111111111111111111112",
		BitcoinAddress:  "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080",
		TronAddress:     "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	r.wallets = append(r.wallets, wallet)
	return wallet, nil
}

func (r *fakePaymentWalletRepo) EnsureAllAddresses(context.Context, uuid.UUID, *blockchain.ChainFactory) error {
	return nil
}

type fakePaymentProductRepo struct {
	product *models.Product
}

func (r fakePaymentProductRepo) FindByID(_ context.Context, id uuid.UUID) (*models.Product, error) {
	if r.product == nil || r.product.ID != id {
		return nil, gorm.ErrRecordNotFound
	}
	return r.product, nil
}

type fakePaymentSessionRepo struct {
	createCalls        int
	selectCalls        int
	expireCalls        int
	markWebhookCalls   int
	markWebhookSuccess bool
	expireErr          error
	sessions           []*models.PaymentSession
	quotes             []*models.PriceQuote
}

func (r *fakePaymentSessionRepo) Create(_ context.Context, session *models.PaymentSession) error {
	r.createCalls++
	if session.ID == uuid.Nil {
		session.ID = uuid.New()
	}
	if session.SessionToken == "" {
		session.SessionToken = "session-" + session.ID.String()
	}
	if session.Status == "" {
		session.Status = models.PaymentStatusPending
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now()
	}
	session.UpdatedAt = session.CreatedAt
	r.sessions = append(r.sessions, session)
	return nil
}

func (r *fakePaymentSessionRepo) FindByID(_ context.Context, id uuid.UUID) (*models.PaymentSession, error) {
	for _, session := range r.sessions {
		if session.ID == id {
			return session, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakePaymentSessionRepo) FindByToken(_ context.Context, token string) (*models.PaymentSession, error) {
	for _, session := range r.sessions {
		if session.SessionToken == token {
			return session, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakePaymentSessionRepo) SelectAsset(_ context.Context, token string, chainID constants.ChainID, symbol string, assetToken *string, decimals uint8, amountRaw string, depositAddress string, quote *models.PriceQuote) (*models.PaymentSession, error) {
	r.selectCalls++
	session, err := r.FindByToken(context.Background(), token)
	if err != nil {
		return nil, err
	}
	session.SelectedChainID = &chainID
	session.SelectedToken = assetToken
	session.SelectedSymbol = symbol
	session.SelectedDecimals = decimals
	session.ExpectedAmountRaw = amountRaw
	session.DepositAddress = depositAddress
	session.Status = models.PaymentStatusAwaitingPayment
	session.UpdatedAt = time.Now()
	if quote != nil {
		if quote.ID == uuid.Nil {
			quote.ID = uuid.New()
		}
		quote.PaymentID = session.ID
		r.quotes = append(r.quotes, quote)
	}
	return session, nil
}

func (r *fakePaymentSessionRepo) ResetSelection(context.Context, string) (*models.PaymentSession, error) {
	return nil, errors.New("not implemented")
}

func (r *fakePaymentSessionRepo) Cancel(context.Context, string) (*models.PaymentSession, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (r *fakePaymentSessionRepo) Expire(ctx context.Context, token string) (*models.PaymentSession, bool, error) {
	r.expireCalls++
	if r.expireErr != nil {
		return nil, false, r.expireErr
	}
	session, err := r.FindByToken(ctx, token)
	if err != nil {
		return nil, false, err
	}
	if paymentSessionTerminal(*session) {
		return session, false, nil
	}
	session.Status = models.PaymentStatusExpired
	session.WebhookEvent = constants.WebhookEventPaymentExpired
	session.UpdatedAt = time.Now()
	return session, true, nil
}

type fakeCheckoutMutationRepo struct {
	*fakePaymentSessionRepo
	resetCalls  int
	cancelCalls int
	cancelErr   error
}

func (r *fakeCheckoutMutationRepo) ResetSelection(ctx context.Context, token string) (*models.PaymentSession, error) {
	r.resetCalls++
	session, err := r.FindByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	session.SelectedChainID = nil
	session.SelectedToken = nil
	session.SelectedSymbol = ""
	session.SelectedDecimals = 0
	session.ExpectedAmountRaw = ""
	session.DepositAddress = ""
	session.Status = models.PaymentStatusPending
	return session, nil
}

func (r *fakeCheckoutMutationRepo) Cancel(ctx context.Context, token string) (*models.PaymentSession, bool, error) {
	r.cancelCalls++
	if r.cancelErr != nil {
		return nil, false, r.cancelErr
	}
	session, err := r.FindByToken(ctx, token)
	if err != nil {
		return nil, false, err
	}
	if !paymentSessionTerminal(*session) {
		session.Status = models.PaymentStatusCanceled
		session.WebhookEvent = constants.WebhookEventPaymentFailed
	}
	return session, false, nil
}

func (r *fakePaymentSessionRepo) DB() *gorm.DB {
	return nil
}

func (r *fakePaymentSessionRepo) MarkWebhookAttempt(_ context.Context, _ uuid.UUID, delivered bool, _ error) error {
	r.markWebhookCalls++
	r.markWebhookSuccess = delivered
	return nil
}

type fakePaymentWebhookDeliveryRepo struct {
	enqueueCalls int
	markCalls    int
}

func (r *fakePaymentWebhookDeliveryRepo) EnqueuePayment(context.Context, models.Domain, models.PaymentSession) (*models.WebhookDelivery, bool, error) {
	r.enqueueCalls++
	return &models.WebhookDelivery{ID: uuid.New()}, true, nil
}

func (r *fakePaymentWebhookDeliveryRepo) MarkAttempt(context.Context, uuid.UUID, bool, error) error {
	r.markCalls++
	return nil
}

func TestDeliverPaymentWebhookQueuesWithoutInlineNotifier(t *testing.T) {
	paymentRepo := &fakePaymentSessionRepo{}
	deliveryRepo := &fakePaymentWebhookDeliveryRepo{}
	session := &models.PaymentSession{
		ID:           uuid.New(),
		MerchantID:   uuid.New(),
		DomainID:     uuid.New(),
		WalletID:     uuid.New(),
		WebhookEvent: constants.WebhookEventPaymentSucceeded,
		Domain: models.Domain{
			ID:         uuid.New(),
			MerchantID: uuid.New(),
			WebhookURL: "http://127.0.0.1/webhook",
		},
	}

	deliverPaymentWebhook(context.Background(), PaymentHandlerDeps{
		PaymentRepo:         paymentRepo,
		WebhookDeliveryRepo: deliveryRepo,
		Notifier:            nil,
	}, session)

	if deliveryRepo.enqueueCalls != 1 {
		t.Fatalf("enqueue calls = %d, want 1", deliveryRepo.enqueueCalls)
	}
	if deliveryRepo.markCalls != 0 {
		t.Fatalf("delivery mark calls = %d, want 0 before boundary delivery", deliveryRepo.markCalls)
	}
	if paymentRepo.markWebhookCalls != 0 {
		t.Fatalf("payment webhook mark calls = %d, want 0 before boundary delivery", paymentRepo.markWebhookCalls)
	}
}

type fakePaymentIdempotencyRepo struct {
	beginCalls    int
	completeCalls int
	failCalls     int
	completeErr   error
	failErr       error
	records       map[string]*models.IdempotencyKey
}

func newFakePaymentIdempotencyRepo() *fakePaymentIdempotencyRepo {
	return &fakePaymentIdempotencyRepo{records: map[string]*models.IdempotencyKey{}}
}

func (r *fakePaymentIdempotencyRepo) RequestHash(payload any) (string, error) {
	return (&repositories.IdempotencyRepo{}).RequestHash(payload)
}

func (r *fakePaymentIdempotencyRepo) Begin(_ context.Context, domainID, merchantID uuid.UUID, key, requestHash string, ttl time.Duration) (*models.IdempotencyKey, bool, error) {
	r.beginCalls++
	scope := domainID.String() + ":" + key
	if current := r.records[scope]; current != nil {
		if current.RequestHash != requestHash {
			return current, false, repositories.ErrIdempotencyConflict
		}
		return current, false, nil
	}
	expiresAt := time.Now().Add(ttl)
	record := &models.IdempotencyKey{
		ID:          uuid.New(),
		DomainID:    domainID,
		MerchantID:  merchantID,
		Key:         key,
		RequestHash: requestHash,
		Status:      models.IdempotencyStatusInProgress,
		ExpiresAt:   &expiresAt,
	}
	r.records[scope] = record
	return record, true, nil
}

func (r *fakePaymentIdempotencyRepo) Complete(_ context.Context, id uuid.UUID, sessionID uuid.UUID, responseBody string) error {
	r.completeCalls++
	if r.completeErr != nil {
		return r.completeErr
	}
	for _, record := range r.records {
		if record.ID == id {
			record.Status = models.IdempotencyStatusCompleted
			record.PaymentSessionID = &sessionID
			record.ResponseBody = responseBody
			record.Error = ""
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (r *fakePaymentIdempotencyRepo) Fail(_ context.Context, id uuid.UUID, errText string) error {
	r.failCalls++
	if r.failErr != nil {
		return r.failErr
	}
	for _, record := range r.records {
		if record.ID == id {
			record.Status = models.IdempotencyStatusFailed
			record.Error = errText
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func newPaymentCreateHandlerTestDeps(oracle *countingPriceOracle, registry *asset.Registry) (PaymentHandlerDeps, *fakePaymentWalletRepo, *fakePaymentSessionRepo, *fakePaymentIdempotencyRepo) {
	domain := &models.Domain{
		ID:         uuid.New(),
		MerchantID: uuid.New(),
		APIKey:     "key-1",
	}
	walletRepo := &fakePaymentWalletRepo{}
	paymentRepo := &fakePaymentSessionRepo{}
	idempotencyRepo := newFakePaymentIdempotencyRepo()
	deps := PaymentHandlerDeps{
		DomainRepo:      fakeV1DomainLookup{byKey: map[string]*models.Domain{"key-1": domain}},
		WalletRepo:      walletRepo,
		PaymentRepo:     paymentRepo,
		AssetRegistry:   registry,
		PriceOracle:     oracle,
		IdempotencyRepo: idempotencyRepo,
	}
	return deps, walletRepo, paymentRepo, idempotencyRepo
}

func performPaymentCreateRequest(t *testing.T, app *fiber.App, path string, body string, idempotencyKey string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "key-1")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode response %q: %v", string(raw), err)
		}
	}
	return resp.StatusCode, decoded
}

func TestHandlePaymentCreateSelectedAssetPersistsQuoteAndCachesRetry(t *testing.T) {
	registry := asset.NewRegistry()
	token := "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	registry.Register(asset.NewERC20(constants.Ethereum, token, "USDC", "USD Coin", 6))
	oracle := &countingPriceOracle{prices: map[string]string{"USDC|USD": "1"}}
	deps, walletRepo, paymentRepo, _ := newPaymentCreateHandlerTestDeps(oracle, registry)

	app := fiber.New()
	app.Post("/payments/create", HandlePaymentCreate(deps))

	payload := `{"order_id":"order-1","amount":"10","currency":"USD","chain_id":1,"symbol":"usdc","token":"` + token + `"}`
	status, first := performPaymentCreateRequest(t, app, "/payments/create", payload, "idem-1")
	if status != fiber.StatusCreated {
		t.Fatalf("status = %d, want 201: %#v", status, first)
	}
	if first["success"] != true {
		t.Fatalf("success = %#v, want true", first["success"])
	}
	if first["status"] != models.PaymentStatusAwaitingPayment {
		t.Fatalf("status field = %#v, want awaiting_payment", first["status"])
	}
	if first["chain_id"] != float64(constants.Ethereum) || first["symbol"] != "USDC" || first["token"] != token {
		t.Fatalf("selected asset fields = %#v", first)
	}
	if first["expected_amount_raw"] != "10000000" || first["deposit_address"] == "" {
		t.Fatalf("quote/address fields missing: %#v", first)
	}
	if walletRepo.createCalls != 1 || paymentRepo.createCalls != 1 || paymentRepo.selectCalls != 1 {
		t.Fatalf("mutation calls wallet/create/select = %d/%d/%d, want 1/1/1", walletRepo.createCalls, paymentRepo.createCalls, paymentRepo.selectCalls)
	}
	if len(paymentRepo.quotes) != 1 {
		t.Fatalf("quotes = %d, want 1", len(paymentRepo.quotes))
	}
	quote := paymentRepo.quotes[0]
	if quote.PaymentID != paymentRepo.sessions[0].ID || quote.ExpectedAmountRaw != "10000000" || quote.Price != "1" || quote.PriceSource != "price_oracle" {
		t.Fatalf("quote snapshot = %#v", quote)
	}
	if oracle.calls != 1 {
		t.Fatalf("oracle calls after first create = %d, want 1", oracle.calls)
	}

	oracle.prices["USDC|USD"] = "2"
	status, retry := performPaymentCreateRequest(t, app, "/payments/create", payload, "idem-1")
	if status != fiber.StatusOK {
		t.Fatalf("retry status = %d, want 200: %#v", status, retry)
	}
	if retry["payment_id"] != first["payment_id"] || retry["checkout_url"] != first["checkout_url"] || retry["expected_amount_raw"] != "10000000" {
		t.Fatalf("retry response not stable: first=%#v retry=%#v", first, retry)
	}
	if walletRepo.createCalls != 1 || paymentRepo.createCalls != 1 || paymentRepo.selectCalls != 1 || len(paymentRepo.quotes) != 1 {
		t.Fatalf("retry mutated state wallet/create/select/quotes = %d/%d/%d/%d", walletRepo.createCalls, paymentRepo.createCalls, paymentRepo.selectCalls, len(paymentRepo.quotes))
	}
	if oracle.calls != 1 {
		t.Fatalf("retry recalculated quote; oracle calls = %d, want 1", oracle.calls)
	}
}

func TestHandlePaymentCreateIdempotencyConflictDoesNotCreateDuplicateState(t *testing.T) {
	registry := asset.NewRegistry()
	token := "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	registry.Register(asset.NewERC20(constants.Ethereum, token, "USDC", "USD Coin", 6))
	oracle := &countingPriceOracle{prices: map[string]string{"USDC|USD": "1"}}
	deps, walletRepo, paymentRepo, _ := newPaymentCreateHandlerTestDeps(oracle, registry)

	app := fiber.New()
	app.Post("/payments/create", HandlePaymentCreate(deps))

	firstPayload := `{"order_id":"order-1","amount":"10","currency":"USD","chain_id":1,"symbol":"USDC","token":"` + token + `"}`
	status, first := performPaymentCreateRequest(t, app, "/payments/create", firstPayload, "idem-conflict")
	if status != fiber.StatusCreated {
		t.Fatalf("first status = %d, want 201: %#v", status, first)
	}
	conflictPayload := `{"order_id":"order-1","amount":"11","currency":"USD","chain_id":1,"symbol":"USDC","token":"` + token + `"}`
	status, conflict := performPaymentCreateRequest(t, app, "/payments/create", conflictPayload, "idem-conflict")
	if status != fiber.StatusConflict {
		t.Fatalf("conflict status = %d, want 409: %#v", status, conflict)
	}
	if conflict["success"] != false || conflict["error"] == "" {
		t.Fatalf("legacy conflict envelope = %#v", conflict)
	}
	if walletRepo.createCalls != 1 || paymentRepo.createCalls != 1 || paymentRepo.selectCalls != 1 || len(paymentRepo.quotes) != 1 {
		t.Fatalf("conflict mutated state wallet/create/select/quotes = %d/%d/%d/%d", walletRepo.createCalls, paymentRepo.createCalls, paymentRepo.selectCalls, len(paymentRepo.quotes))
	}
}

func TestHandlePaymentCreateDoesNotReportSuccessWhenIdempotencyCompletionFails(t *testing.T) {
	registry := asset.NewRegistry()
	oracle := &countingPriceOracle{prices: map[string]string{}}
	deps, walletRepo, paymentRepo, idempotencyRepo := newPaymentCreateHandlerTestDeps(oracle, registry)
	idempotencyRepo.completeErr = errors.New("database unavailable")

	app := fiber.New()
	app.Post("/payments/create", HandlePaymentCreate(deps))
	payload := `{"order_id":"order-complete-failure","amount":"10","currency":"USD"}`
	status, body := performPaymentCreateRequest(t, app, "/payments/create", payload, "idem-complete-failure")

	if status != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %#v", status, body)
	}
	if body["success"] != false || body["error"] != "idempotency completion failed" {
		t.Fatalf("body = %#v, want explicit idempotency completion failure", body)
	}
	if idempotencyRepo.completeCalls != 1 {
		t.Fatalf("complete calls = %d, want 1", idempotencyRepo.completeCalls)
	}
	if walletRepo.createCalls != 1 || paymentRepo.createCalls != 1 {
		t.Fatalf("resource creation calls wallet/payment = %d/%d, want 1/1", walletRepo.createCalls, paymentRepo.createCalls)
	}
}

func TestHandlePaymentCreateUnsupportedSelectedAssetDoesNotCreateSessionOrWallet(t *testing.T) {
	registry := asset.NewRegistry()
	oracle := &countingPriceOracle{prices: map[string]string{"USDC|USD": "1"}}
	deps, walletRepo, paymentRepo, idempotencyRepo := newPaymentCreateHandlerTestDeps(oracle, registry)

	app := fiber.New()
	app.Post("/payments/create", HandlePaymentCreate(deps))

	payload := `{"order_id":"order-unsupported","amount":"10","currency":"USD","chain_id":1,"symbol":"USDC"}`
	status, body := performPaymentCreateRequest(t, app, "/payments/create", payload, "idem-unsupported")
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %#v", status, body)
	}
	if walletRepo.createCalls != 0 || paymentRepo.createCalls != 0 || paymentRepo.selectCalls != 0 || len(paymentRepo.quotes) != 0 {
		t.Fatalf("unsupported asset mutated state wallet/create/select/quotes = %d/%d/%d/%d", walletRepo.createCalls, paymentRepo.createCalls, paymentRepo.selectCalls, len(paymentRepo.quotes))
	}
	if idempotencyRepo.failCalls != 1 {
		t.Fatalf("idempotency fail calls = %d, want 1", idempotencyRepo.failCalls)
	}
	if oracle.calls != 0 {
		t.Fatalf("unsupported asset called oracle = %d, want 0", oracle.calls)
	}
}

func TestHandlePaymentCreateV1SuccessAndConflictUseV1Envelope(t *testing.T) {
	registry := asset.NewRegistry()
	token := "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	registry.Register(asset.NewERC20(constants.Ethereum, token, "USDC", "USD Coin", 6))
	oracle := &countingPriceOracle{prices: map[string]string{"USDC|USD": "1"}}
	deps, walletRepo, paymentRepo, _ := newPaymentCreateHandlerTestDeps(oracle, registry)

	app := fiber.New()
	app.Post("/api/v1/payment/create", handlePaymentCreate(deps, paymentCreateModeV1))

	firstPayload := `{"order_id":"order-v1","amount":"10","currency":"USD","chain_id":1,"symbol":"USDC","token":"` + token + `"}`
	status, first := performPaymentCreateRequest(t, app, "/api/v1/payment/create", firstPayload, "idem-v1")
	if status != fiber.StatusCreated {
		t.Fatalf("first status = %d, want 201: %#v", status, first)
	}
	if first["result"] != "ok" {
		t.Fatalf("v1 success envelope = %#v", first)
	}
	data, ok := first["data"].(map[string]any)
	if !ok {
		t.Fatalf("v1 data = %T, want object", first["data"])
	}
	if data["chain_id"] != float64(constants.Ethereum) || data["expected_amount_raw"] != "10000000" {
		t.Fatalf("v1 data missing selected asset fields: %#v", data)
	}

	conflictPayload := `{"order_id":"order-v1","amount":"11","currency":"USD","chain_id":1,"symbol":"USDC","token":"` + token + `"}`
	status, conflict := performPaymentCreateRequest(t, app, "/api/v1/payment/create", conflictPayload, "idem-v1")
	if status != fiber.StatusConflict {
		t.Fatalf("conflict status = %d, want 409: %#v", status, conflict)
	}
	if conflict["result"] != "error" || conflict["message"] == "" {
		t.Fatalf("v1 conflict envelope = %#v", conflict)
	}
	if _, exists := conflict["success"]; exists {
		t.Fatalf("v1 conflict leaked legacy success field: %#v", conflict)
	}
	if walletRepo.createCalls != 1 || paymentRepo.createCalls != 1 || paymentRepo.selectCalls != 1 || len(paymentRepo.quotes) != 1 {
		t.Fatalf("v1 conflict mutated state wallet/create/select/quotes = %d/%d/%d/%d", walletRepo.createCalls, paymentRepo.createCalls, paymentRepo.selectCalls, len(paymentRepo.quotes))
	}
}
