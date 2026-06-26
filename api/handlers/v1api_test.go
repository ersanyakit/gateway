package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"core/constants"
	"core/models"

	fiber "github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
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
		UserID:             "customer_42",
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

func TestV1StaticWalletListItemParsesProductScope(t *testing.T) {
	wallet := models.Wallet{
		ID:              uuid.New(),
		UserID:          "customer_42",
		ProductID:       "static:1:USDT",
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
	if item["address"] != "0xeth" {
		t.Fatalf("address = %v", item["address"])
	}
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
