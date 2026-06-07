package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"core/constants"
	"core/models"

	fiber "github.com/gofiber/fiber/v3"
)

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
