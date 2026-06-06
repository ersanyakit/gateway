package walletcore

import (
	"encoding/hex"
	"testing"

	"core/constants"
)

func TestGenerateAndValidateMnemonic(t *testing.T) {
	mnemonic, err := GenerateMnemonic(128)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidateMnemonic(mnemonic) {
		t.Fatalf("generated mnemonic should validate: %q", mnemonic)
	}
	if ValidateMnemonic("not a valid mnemonic") {
		t.Fatal("invalid mnemonic should not validate")
	}
}

func TestDerivePrivateKeyFormats(t *testing.T) {
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	tests := []struct {
		name    string
		chainID constants.ChainID
		path    string
		sizes   []int
	}{
		{name: "evm", chainID: constants.Ethereum, path: "m/44'/60'/0'/0/0", sizes: []int{32}},
		{name: "tron", chainID: constants.TRON, path: "m/44'/195'/0'/0/0", sizes: []int{32}},
		{name: "bitcoin", chainID: constants.Bitcoin, path: "m/86'/0'/0'/0/0", sizes: []int{32}},
		{name: "solana", chainID: constants.Solana, path: "m/44'/501'/0'/0'", sizes: []int{32, 64}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := DerivePrivateKey(mnemonic, tt.path, tt.chainID)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := hex.DecodeString(key)
			if err != nil {
				t.Fatal(err)
			}
			if !containsInt(tt.sizes, len(raw)) {
				t.Fatalf("private key size = %d, want one of %v", len(raw), tt.sizes)
			}
		})
	}
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
