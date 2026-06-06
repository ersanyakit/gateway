package asset

import (
	"testing"

	"core/constants"
)

func TestRegistryRegisterLookupAndNative(t *testing.T) {
	registry := NewRegistry()
	eth := NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18)
	usdc := NewERC20(constants.Ethereum, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "USDC", "USDC", 6)

	registry.Register(eth)
	registry.Register(usdc)

	native, ok := registry.GetNative(constants.Ethereum)
	if !ok {
		t.Fatal("native asset was not registered")
	}
	if native.GetSymbol() != "ETH" {
		t.Fatalf("native symbol = %q, want ETH", native.GetSymbol())
	}

	token, ok := registry.Get(constants.Ethereum, "0xa0B86991c6218b36c1d19d4a2e9eb0ce3606eb48")
	if !ok {
		t.Fatal("token lookup should be case-insensitive")
	}
	if token.GetDecimals() != 6 {
		t.Fatalf("token decimals = %d, want 6", token.GetDecimals())
	}
	if !registry.Exists(constants.Ethereum, usdc.GetIdentifier()) {
		t.Fatal("registered token should exist")
	}
}

func TestRegistryAliases(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterAlias("WBTC", "BTC")
	registry.RegisterAlias("weth", "ETH")

	if got := registry.CanonicalSymbol(" wbtc "); got != "BTC" {
		t.Fatalf("canonical WBTC = %q, want BTC", got)
	}
	if got := registry.CanonicalSymbol("sol"); got != "SOL" {
		t.Fatalf("canonical SOL = %q, want SOL", got)
	}
	if !registry.IsAlias("WETH") {
		t.Fatal("WETH should be an alias")
	}
	if registry.IsAlias("ETH") {
		t.Fatal("ETH should not be an alias")
	}
}

func TestRegistryListReturnsNilForMissingChain(t *testing.T) {
	registry := NewRegistry()
	if list := registry.ListByChain(constants.Solana); list != nil {
		t.Fatalf("missing chain list = %#v, want nil", list)
	}
}
