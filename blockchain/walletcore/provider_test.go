package walletcore

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"core/constants"
)

func TestTrustWalletCoreProviderIsDefaultBuild(t *testing.T) {
	trustWalletProvider, err := os.ReadFile("provider_trustwalletcore.go")
	if err != nil {
		t.Fatal(err)
	}
	trustWalletSource := string(trustWalletProvider)
	fallbackProvider, err := os.ReadFile("provider_fallback.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(trustWalletSource, "//go:build !walletcorefallback") {
		t.Fatal("Trust Wallet Core provider must remain the default build; do not gate it behind an opt-in tag")
	}
	featureFallback := strings.Index(trustWalletSource, "#ifndef __has_feature")
	firstTrustWalletInclude := strings.Index(trustWalletSource, "#include <TrustWalletCore/")
	if featureFallback < 0 ||
		!strings.Contains(trustWalletSource, "#define __has_feature(feature) 0") ||
		firstTrustWalletInclude < 0 || featureFallback > firstTrustWalletInclude {
		t.Fatal("Trust Wallet Core cgo preamble must keep the GCC-compatible __has_feature fallback")
	}
	if !strings.Contains(string(fallbackProvider), "//go:build walletcorefallback") {
		t.Fatal("fallback provider must remain opt-in only; money and wallet flows require Trust Wallet Core by default")
	}
}

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
		{name: "tron-testnet", chainID: constants.TRONTestnet, path: "m/44'/195'/0'/0/0", sizes: []int{32}},
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

func TestDeriveWalletAddresses(t *testing.T) {
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	tests := []struct {
		name          string
		chainID       constants.ChainID
		path          string
		addressPrefix string
	}{
		{name: "evm", chainID: constants.Ethereum, path: "m/44'/60'/0'/0/0", addressPrefix: "0x"},
		{name: "tron", chainID: constants.TRON, path: "m/44'/195'/0'/0/0", addressPrefix: "T"},
		{name: "tron-testnet", chainID: constants.TRONTestnet, path: "m/44'/195'/0'/0/0", addressPrefix: "T"},
		{name: "bitcoin", chainID: constants.Bitcoin, path: "m/86'/0'/0'/0/0", addressPrefix: "bc1p"},
		{name: "solana", chainID: constants.Solana, path: "m/44'/501'/0'/0'", addressPrefix: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wallet, err := DeriveWallet(mnemonic, tt.path, tt.chainID)
			if err != nil {
				if strings.Contains(err.Error(), "walletcorefallback") {
					t.Skip(err)
				}
				t.Fatal(err)
			}
			if wallet.PrivateKey == "" {
				t.Fatal("private key is empty")
			}
			if wallet.Address == "" {
				t.Fatal("address is empty")
			}
			if tt.addressPrefix != "" && !strings.HasPrefix(wallet.Address, tt.addressPrefix) {
				t.Fatalf("address %q does not have prefix %q", wallet.Address, tt.addressPrefix)
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
