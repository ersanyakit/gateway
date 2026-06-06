package handlers

import (
	"testing"

	"core/constants"
	"core/models"
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
