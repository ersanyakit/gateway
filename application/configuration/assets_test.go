package application

import (
	"testing"

	"core/constants"
)

func TestNewAssetRegistryHasNativeAssetForEverySupportedChain(t *testing.T) {
	registry := NewAssetRegistry()
	for _, chainID := range constants.AllChainIDs() {
		native, ok := registry.GetNative(chainID)
		if !ok {
			t.Fatalf("missing native asset for chain %d (%s)", chainID, constants.ChainName(chainID))
		}
		if native.GetSymbol() == "" || native.GetDecimals() == 0 {
			t.Fatalf("invalid native asset for chain %d: symbol=%q decimals=%d", chainID, native.GetSymbol(), native.GetDecimals())
		}
	}
}

func TestNewAssetRegistryKnownTokenLookups(t *testing.T) {
	registry := NewAssetRegistry()
	tests := []struct {
		name       string
		chainID    constants.ChainID
		identifier string
		symbol     string
		decimals   uint8
	}{
		{name: "ethereum usdc", chainID: constants.Ethereum, identifier: "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", symbol: "USDC", decimals: 6},
		{name: "base weth", chainID: constants.Base, identifier: "0x4200000000000000000000000000000000000006", symbol: "WETH", decimals: 18},
		{name: "solana usdt", chainID: constants.Solana, identifier: "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", symbol: "USDT", decimals: 6},
		{name: "tron usdt", chainID: constants.TRON, identifier: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", symbol: "USDT", decimals: 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, ok := registry.Get(tt.chainID, tt.identifier)
			if !ok {
				t.Fatalf("token %s not found", tt.identifier)
			}
			if token.GetSymbol() != tt.symbol {
				t.Fatalf("symbol = %q, want %q", token.GetSymbol(), tt.symbol)
			}
			if token.GetDecimals() != tt.decimals {
				t.Fatalf("decimals = %d, want %d", token.GetDecimals(), tt.decimals)
			}
		})
	}
}

func TestNewAssetRegistryAliases(t *testing.T) {
	registry := NewAssetRegistry()
	if got := registry.CanonicalSymbol("WBTC"); got != "BTC" {
		t.Fatalf("WBTC canonical = %q, want BTC", got)
	}
	if got := registry.CanonicalSymbol("WETH"); got != "ETH" {
		t.Fatalf("WETH canonical = %q, want ETH", got)
	}
}
