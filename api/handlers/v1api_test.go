package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/models"
	"core/types"

	fiber "github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestV1WalletProductIDDefaultsToWallet(t *testing.T) {
	if got := v1WalletProductID(""); got != "wallet:default" {
		t.Fatalf("default product id = %q, want wallet:default", got)
	}
	if got := v1WalletProductID(" app-wallet "); got != "wallet:app-wallet" {
		t.Fatalf("product id = %q, want wallet:app-wallet", got)
	}
	if got := v1WalletDisplayProductID("wallet:default"); got != "wallet" {
		t.Fatalf("display product id = %q, want wallet", got)
	}
}

func TestV1PaymentResponseIncludesDonationLinkType(t *testing.T) {
	paidAt := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	session := models.PaymentSession{
		ID:               uuid.New(),
		SessionToken:     "donation-track",
		OrderID:          "donation-order",
		LinkType:         models.PaymentLinkTypeDonation,
		Amount:           "0",
		Currency:         "",
		Status:           models.PaymentStatusPaid,
		PaymentOutcome:   models.PaymentOutcomeDonation,
		MatchedAmountRaw: "42000000000000000",
		PaidAt:           &paidAt,
		CreatedAt:        paidAt.Add(-time.Minute),
	}

	resp := v1PaymentResponse(session)
	if resp["link_type"] != models.PaymentLinkTypeDonation {
		t.Fatalf("link_type = %#v, want donation", resp["link_type"])
	}
	if resp["matched_amount_raw"] != "42000000000000000" || resp["payment_outcome"] != models.PaymentOutcomeDonation {
		t.Fatalf("donation payment outcome fields missing: %#v", resp)
	}
}

func TestV1RefundLimitUsesDonationMatchedAmountWhenExpectedIsZero(t *testing.T) {
	session := &models.PaymentSession{
		LinkType:          models.PaymentLinkTypeDonation,
		ExpectedAmountRaw: "0",
		MatchedAmountRaw:  "997",
	}

	limit, err := v1RefundLimitRaw(context.Background(), V1APIDeps{}, session)
	if err != nil {
		t.Fatalf("v1RefundLimitRaw returned error: %v", err)
	}
	if limit.String() != "997" {
		t.Fatalf("limit = %s, want 997", limit.String())
	}
}

func TestV1OutboundResponsesIncludeLifecycleMetadata(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 30, 0, 0, time.UTC)
	domainID := uuid.New()
	walletID := uuid.New()
	withdrawal := models.WithdrawalRequest{
		ID:             uuid.New(),
		DomainID:       &domainID,
		WalletID:       walletID,
		Chain:          "ethereum",
		Symbol:         "ETH",
		Decimals:       18,
		ToAddress:      "0xto",
		AmountRaw:      "10",
		Status:         models.WithdrawalStatusProcessing,
		TxHash:         "0xtx",
		BroadcastedAt:  &now,
		FinalizedAt:    &now,
		IdempotencyKey: "payout-key",
		CorrelationID:  "corr-payout",
		CreatedAt:      now,
	}
	payout := v1PayoutResponse(withdrawal)
	for key, want := range map[string]any{
		"wallet_id":       walletID.String(),
		"broadcasted_at":  now.UTC().Format(time.RFC3339),
		"finalized_at":    now.UTC().Format(time.RFC3339),
		"idempotency_key": "payout-key",
		"correlation_id":  "corr-payout",
	} {
		if payout[key] != want {
			t.Fatalf("payout[%s] = %#v, want %#v; payload=%#v", key, payout[key], want, payout)
		}
	}

	refundWalletID := uuid.New()
	refund := models.Refund{
		ID:             uuid.New(),
		MerchantID:     uuid.New(),
		DomainID:       uuid.New(),
		PaymentID:      uuid.New(),
		WalletID:       &refundWalletID,
		Chain:          "ethereum",
		Symbol:         "ETH",
		Decimals:       18,
		ToAddress:      "0xrefund",
		AmountRaw:      "10",
		Status:         models.RefundStatusProcessing,
		TxHash:         "0xrefund",
		BroadcastedAt:  &now,
		FinalizedAt:    &now,
		IdempotencyKey: "refund-key",
		CorrelationID:  "corr-refund",
		CreatedAt:      now,
	}
	refundResp := v1RefundResponse(refund)
	for key, want := range map[string]any{
		"wallet_id":       refundWalletID.String(),
		"chain":           "ethereum",
		"symbol":          "ETH",
		"decimals":        uint8(18),
		"to_address":      "0xrefund",
		"broadcasted_at":  now.UTC().Format(time.RFC3339),
		"finalized_at":    now.UTC().Format(time.RFC3339),
		"idempotency_key": "refund-key",
		"correlation_id":  "corr-refund",
	} {
		if refundResp[key] != want {
			t.Fatalf("refund[%s] = %#v, want %#v; payload=%#v", key, refundResp[key], want, refundResp)
		}
	}
}

func TestV1WalletResponseIncludesProviderAddresses(t *testing.T) {
	createdAt := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	wallet := &models.Wallet{
		ID:                 uuid.New(),
		UserID:             "customer_42",
		ProductID:          "wallet:default",
		BitcoinAddress:     "bc1qwallet",
		EthereumAddress:    "0xeth",
		AvalancheAddress:   "0xavax",
		BinanceAddress:     "0xbnb",
		BaseAddress:        "0xbase",
		ArbitrumAddress:    "0xarb",
		UnichainAddress:    "0xuni",
		TronAddress:        "TWallet",
		SolanaAddress:      "SoWallet",
		ChilizAddress:      "0xchz",
		ChilizSpicyAddress: "0xspicy",
		CreatedAt:          createdAt,
	}

	resp := v1WalletResponse(wallet)
	if resp["product_id"] != "wallet" {
		t.Fatalf("product_id = %v, want wallet", resp["product_id"])
	}
	addresses, ok := resp["addresses"].(map[string]string)
	if !ok {
		t.Fatalf("addresses type = %T", resp["addresses"])
	}
	expected := map[string]string{
		"bitcoin":      "bc1qwallet",
		"ethereum":     "0xeth",
		"avalanche":    "0xavax",
		"bnbchain":     "0xbnb",
		"base":         "0xbase",
		"arbitrum":     "0xarb",
		"unichain":     "0xuni",
		"tron":         "TWallet",
		"solana":       "SoWallet",
		"chiliz":       "0xchz",
		"chiliz_spicy": "0xspicy",
	}
	for chain, want := range expected {
		if addresses[chain] != want {
			t.Fatalf("address[%s] = %q, want %q", chain, addresses[chain], want)
		}
	}
}

func TestV1StaticAddressResponseReturnsSelectedChainAddress(t *testing.T) {
	wallet := &models.Wallet{
		ID:                 uuid.New(),
		UserID:             "customer_42",
		ProductID:          "static:88888:CHZ",
		EthereumAddress:    "0xeth",
		ChilizSpicyAddress: "0xspicy",
	}

	resp := v1StaticAddressResponse(wallet, constants.ChilizSpicy, "CHZ", "Main wallet")

	if resp["chain"] != constants.ChainName(constants.ChilizSpicy) {
		t.Fatalf("chain = %v", resp["chain"])
	}
	if resp["symbol"] != "CHZ" {
		t.Fatalf("symbol = %v", resp["symbol"])
	}
	if resp["address"] != "0xspicy" {
		t.Fatalf("address = %v", resp["address"])
	}
	if resp["label"] != "Main wallet" {
		t.Fatalf("label = %v", resp["label"])
	}
}

func TestV1ResolveStaticAddressScopeDefaultsNativeAsset(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	chainID := int64(constants.Ethereum)

	scope, err := v1ResolveStaticAddressScope(registry, types.V1StaticAddressRequest{ChainID: &chainID})
	if err != nil {
		t.Fatalf("resolve scope: %v", err)
	}
	if scope.Symbol != "ETH" {
		t.Fatalf("symbol = %q, want ETH", scope.Symbol)
	}
	if scope.Token != "" {
		t.Fatalf("token = %q, want empty", scope.Token)
	}
	if scope.ProductID != "static:1:ETH" {
		t.Fatalf("product id = %q, want static:1:ETH", scope.ProductID)
	}
}

func TestV1ResolveStaticAddressScopeRequiresAssetRegistry(t *testing.T) {
	chainID := int64(constants.Ethereum)

	_, err := v1ResolveStaticAddressScope(nil, types.V1StaticAddressRequest{
		ChainID: &chainID,
		Symbol:  "ETH",
	})
	if err == nil || !strings.Contains(err.Error(), "asset registry is not configured") {
		t.Fatalf("err = %v, want asset registry not configured", err)
	}
}

func TestV1ResolveStaticAddressScopeRequiresTokenForNonNativeAsset(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewERC20(constants.Ethereum, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "USDC", "USD Coin", 6))
	chainID := int64(constants.Ethereum)

	_, err := v1ResolveStaticAddressScope(registry, types.V1StaticAddressRequest{
		ChainID: &chainID,
		Symbol:  "USDC",
	})
	if err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("err = %v, want token required", err)
	}
}

func TestV1ResolveStaticAddressScopeIncludesTokenAndProduct(t *testing.T) {
	registry := asset.NewRegistry()
	token := "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	registry.Register(asset.NewERC20(constants.Ethereum, token, "USDC", "USD Coin", 6))
	chainID := int64(constants.Ethereum)

	scope, err := v1ResolveStaticAddressScope(registry, types.V1StaticAddressRequest{
		ChainID:   &chainID,
		Symbol:    "usdc",
		Token:     token,
		ProductID: "checkout",
	})
	if err != nil {
		t.Fatalf("resolve scope: %v", err)
	}
	wantToken := strings.ToLower(token)
	if scope.Symbol != "USDC" {
		t.Fatalf("symbol = %q, want USDC", scope.Symbol)
	}
	if scope.Token != wantToken {
		t.Fatalf("token = %q, want %q", scope.Token, wantToken)
	}
	wantProduct := "static:1:USDC:token:" + wantToken + ":product:checkout"
	if scope.ProductID != wantProduct {
		t.Fatalf("product id = %q, want %q", scope.ProductID, wantProduct)
	}
}

func TestV1ValidateStaticAddressChainReadyRejectsUnavailableChain(t *testing.T) {
	if err := v1ValidateStaticAddressChainReady(nil, constants.Ethereum); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("nil factory err = %v, want not ready", err)
	}
	factory := blockchain.NewChainFactory()
	if err := v1ValidateStaticAddressChainReady(factory, constants.Ethereum); err == nil || !strings.Contains(err.Error(), "unsupported chain_id") {
		t.Fatalf("empty factory err = %v, want unsupported chain_id", err)
	}
}

func TestV1StaticAddressResponseIncludesScopeMetadata(t *testing.T) {
	createdAt := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	scope := v1StaticAddressScope{
		ChainID:   constants.Ethereum,
		Symbol:    "USDC",
		Token:     "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
		ProductID: "static:1:USDC:token:0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48:product:checkout",
	}
	wallet := &models.Wallet{
		ID:              uuid.New(),
		UserID:          "customer_42",
		ProductID:       scope.ProductID,
		EthereumAddress: "0xeth",
		CreatedAt:       createdAt,
	}

	resp, err := v1StaticAddressResponseForScope(wallet, scope, "Main wallet")
	if err != nil {
		t.Fatalf("response: %v", err)
	}
	if resp["product_id"] != scope.ProductID {
		t.Fatalf("product_id = %v, want %s", resp["product_id"], scope.ProductID)
	}
	if resp["chain_id"] != int64(constants.Ethereum) {
		t.Fatalf("chain_id = %v, want %d", resp["chain_id"], constants.Ethereum)
	}
	if resp["token"] != scope.Token {
		t.Fatalf("token = %v, want %s", resp["token"], scope.Token)
	}
	if resp["created_at"] != createdAt.UTC().Format(time.RFC3339) {
		t.Fatalf("created_at = %v", resp["created_at"])
	}
}

func TestV1StaticAddressResponseRequiresRequestedChainAddress(t *testing.T) {
	wallet := &models.Wallet{
		ID:     uuid.New(),
		UserID: "customer_42",
	}

	_, err := v1StaticAddressResponseForScope(wallet, v1StaticAddressScope{
		ChainID:   constants.Solana,
		Symbol:    "SOL",
		ProductID: "static:501:SOL",
	}, "")
	if err == nil || !strings.Contains(err.Error(), "address unavailable") {
		t.Fatalf("err = %v, want address unavailable", err)
	}
}

func TestV1OutboundCreateSourceContractUsesIdempotencyBeforeMutation(t *testing.T) {
	source := readHandlerSource(t, "v1api.go")
	for _, tc := range []struct {
		function      string
		mutationToken string
		metadataToken string
	}{
		{
			function:      "HandleV1PayoutCreate",
			mutationToken: "ensureV1ReserveWallet",
			metadataToken: "IdempotencyKey: idempotencyKey",
		},
		{
			function:      "HandleV1RefundCreate",
			mutationToken: "deps.RefundRepo.CreateWithHold",
			metadataToken: "IdempotencyKey: idempotencyKey",
		},
	} {
		body := extractHandlerFunctionBody(t, source, tc.function)
		for _, token := range []string{
			"beginV1CreateIdempotency",
			"replayV1CreateIdempotency",
			"completeV1CreateIdempotency",
			"failV1CreateIdempotency",
			tc.metadataToken,
			"CorrelationID:  correlationID",
		} {
			if !strings.Contains(body, token) {
				t.Fatalf("%s idempotency contract missing %q", tc.function, token)
			}
		}
		if strings.Index(body, "beginV1CreateIdempotency") > strings.Index(body, tc.mutationToken) {
			t.Fatalf("%s must begin idempotency before mutation %q", tc.function, tc.mutationToken)
		}
	}
}

func TestV1ValidateStaticAddressChainReadyFailsBeforeWalletMutation(t *testing.T) {
	if err := v1ValidateStaticAddressChainReady(nil, constants.Ethereum); err == nil || !strings.Contains(err.Error(), "chain factory is not ready") {
		t.Fatalf("nil chain factory err = %v, want not ready", err)
	}

	factory := blockchain.NewChainFactory()
	if err := v1ValidateStaticAddressChainReady(factory, constants.Ethereum); err == nil || !strings.Contains(err.Error(), "unsupported chain_id") {
		t.Fatalf("unregistered chain err = %v, want unsupported chain_id", err)
	}
}

func TestV1CreateStaticAddressWalletConcurrentSameScopeCreatesOneWallet(t *testing.T) {
	repo := newFakeStaticWalletRepo()
	domain := &models.Domain{ID: uuid.New(), MerchantID: uuid.New()}
	scope := v1StaticAddressScope{
		ChainID:   constants.Ethereum,
		Symbol:    "ETH",
		ProductID: "static:1:ETH",
	}

	const workers = 20
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wallet, err := v1CreateStaticAddressWallet(context.Background(), repo, nil, domain, "customer_42", scope)
			if err != nil {
				errs <- err
				return
			}
			ids <- wallet.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		t.Fatalf("create static wallet: %v", err)
	}
	var first uuid.UUID
	for id := range ids {
		if first == uuid.Nil {
			first = id
			continue
		}
		if id != first {
			t.Fatalf("wallet id = %s, want %s", id, first)
		}
	}
	if repo.createCount != 1 {
		t.Fatalf("create count = %d, want 1", repo.createCount)
	}
}

func TestV1CreateStaticAddressWalletScopesByDomain(t *testing.T) {
	repo := newFakeStaticWalletRepo()
	scope := v1StaticAddressScope{
		ChainID:   constants.Ethereum,
		Symbol:    "ETH",
		ProductID: "static:1:ETH",
	}
	domainA := &models.Domain{ID: uuid.New(), MerchantID: uuid.New()}
	domainB := &models.Domain{ID: uuid.New(), MerchantID: domainA.MerchantID}

	walletA, err := v1CreateStaticAddressWallet(context.Background(), repo, nil, domainA, "customer_42", scope)
	if err != nil {
		t.Fatalf("create domain A: %v", err)
	}
	walletB, err := v1CreateStaticAddressWallet(context.Background(), repo, nil, domainB, "customer_42", scope)
	if err != nil {
		t.Fatalf("create domain B: %v", err)
	}
	if walletA.ID == walletB.ID {
		t.Fatalf("same wallet id for different domains: %s", walletA.ID)
	}
	if repo.createCount != 2 {
		t.Fatalf("create count = %d, want 2", repo.createCount)
	}
}

func TestV1StaticWalletListItemParsesProductScope(t *testing.T) {
	wallet := models.Wallet{
		ID:              uuid.New(),
		UserID:          "customer_42",
		ProductID:       "static:1:USDT:token:0xdac17f958d2ee523a2206206994597c13d831ec7:product:checkout",
		EthereumAddress: "0xeth",
		CreatedAt:       time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC),
	}
	item := v1StaticWalletListItem(wallet)
	if item["chain"] != constants.ChainName(constants.Ethereum) {
		t.Fatalf("chain = %v", item["chain"])
	}
	if item["chain_id"] != int64(constants.Ethereum) {
		t.Fatalf("chain_id = %v", item["chain_id"])
	}
	if item["symbol"] != "USDT" {
		t.Fatalf("symbol = %v", item["symbol"])
	}
	if item["token"] != "0xdac17f958d2ee523a2206206994597c13d831ec7" {
		t.Fatalf("token = %v", item["token"])
	}
	if item["address"] != "0xeth" {
		t.Fatalf("address = %v", item["address"])
	}
}

type fakeStaticWalletRepo struct {
	mu          sync.Mutex
	wallets     map[string]*models.Wallet
	byID        map[uuid.UUID]*models.Wallet
	createCount int
}

func newFakeStaticWalletRepo() *fakeStaticWalletRepo {
	return &fakeStaticWalletRepo{
		wallets: make(map[string]*models.Wallet),
		byID:    make(map[uuid.UUID]*models.Wallet),
	}
}

func (r *fakeStaticWalletRepo) Create(params types.WalletParams) (*models.Wallet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := strings.Join([]string{*params.MerchantId, *params.DomainId, *params.ProductId, *params.UserId}, "|")
	if existing := r.wallets[key]; existing != nil {
		clone := *existing
		return &clone, nil
	}

	merchantID, err := uuid.Parse(*params.MerchantId)
	if err != nil {
		return nil, err
	}
	domainID, err := uuid.Parse(*params.DomainId)
	if err != nil {
		return nil, err
	}
	r.createCount++
	wallet := &models.Wallet{
		ID:              uuid.New(),
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       *params.ProductId,
		UserID:          *params.UserId,
		HDAddressId:     uint32(r.createCount),
		EthereumAddress: "0xeth",
		CreatedAt:       time.Now().UTC(),
	}
	r.wallets[key] = wallet
	r.byID[wallet.ID] = wallet
	clone := *wallet
	return &clone, nil
}

func (r *fakeStaticWalletRepo) EnsureAllAddresses(context.Context, uuid.UUID, *blockchain.ChainFactory) error {
	return nil
}

func (r *fakeStaticWalletRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	wallet := r.byID[id]
	if wallet == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *wallet
	return &clone, nil
}

func TestHandleV1CommonAddressQRCodeReturnsPNG(t *testing.T) {
	app := fiber.New()
	app.Get("/api/v1/common/qrcode", HandleV1CommonAddressQRCode())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/common/qrcode?address=0xabc&size=128", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("content-type = %q", resp.Header.Get("Content-Type"))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	pngSignature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if !bytes.HasPrefix(body, pngSignature) {
		t.Fatalf("body is not a PNG")
	}
}

func TestHandleV1CommonAddressQRCodeRequiresAddress(t *testing.T) {
	app := fiber.New()
	app.Get("/api/v1/common/qrcode", HandleV1CommonAddressQRCode())

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/common/qrcode", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
