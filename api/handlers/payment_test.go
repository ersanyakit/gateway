package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
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
	if data["chain_id"] != int64(constants.Ethereum) {
		t.Fatalf("chain_id = %#v, want %d", data["chain_id"], constants.Ethereum)
	}
	if data["token"] != token {
		t.Fatalf("token = %#v, want %s", data["token"], token)
	}
	if data["expected_amount_raw"] != "10000000" || data["deposit_address"] == "" {
		t.Fatalf("asset response fields missing: %#v", data)
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
			name: "failed terminal",
			session: models.PaymentSession{
				Status:    models.PaymentStatusFailed,
				ExpiresAt: &future,
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

func TestHandleCheckoutRedirectsTerminalStateToPay(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	session := &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "checkout-token",
		Status:       models.PaymentStatusUnderpaid,
		ExpiresAt:    &future,
	}
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{session}}
	app := fiber.New()
	app.Get("/checkout/:token", HandleCheckout(PaymentHandlerDeps{PaymentRepo: repo}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/checkout/checkout-token", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/checkout/checkout-token/pay" {
		t.Fatalf("location = %q, want /checkout/checkout-token/pay", got)
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

type fakePaymentSessionRepo struct {
	createCalls int
	selectCalls int
	sessions    []*models.PaymentSession
	quotes      []*models.PriceQuote
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

func (r *fakePaymentSessionRepo) DB() *gorm.DB {
	return nil
}

func (r *fakePaymentSessionRepo) MarkWebhookAttempt(context.Context, uuid.UUID, bool, error) error {
	return nil
}

type fakePaymentIdempotencyRepo struct {
	beginCalls    int
	completeCalls int
	failCalls     int
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
