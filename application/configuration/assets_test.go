package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"core/asset"
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
		{name: "solana chz", chainID: constants.Solana, identifier: "6eftxVbSAunVEoxUWdGhPdxg5UdsJ8Wkwy5w5YFuxouw", symbol: "CHZ", decimals: 8},
		{name: "base pepper", chainID: constants.Base, identifier: "0x5e985E4BCa4664E985f3FaF8140EbA25b10E28C2", symbol: "PEPPER", decimals: 18},
		{name: "solana pepper", chainID: constants.Solana, identifier: "GozPNCAseytzxCR3d2k8hTsTYkr4SDpuXy2RQAZFVx2g", symbol: "PEPPER", decimals: 3},
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
	if got := registry.CanonicalSymbol("WCHZ"); got != "CHZ" {
		t.Fatalf("WCHZ canonical = %q, want CHZ", got)
	}
	if got := registry.CanonicalSymbol("WAVAX"); got != "AVAX" {
		t.Fatalf("WAVAX canonical = %q, want AVAX", got)
	}
	if got := registry.LogoURL("WCHZ"); got != "/static/coins/chz.svg" {
		t.Fatalf("WCHZ logo = %q, want /static/coins/chz.svg", got)
	}
}

func TestNewAssetRegistryGroupsMultiChainAssets(t *testing.T) {
	registry := NewAssetRegistry()
	definitions := registry.ListDefinitions()
	bySymbol := make(map[string]int, len(definitions))
	for _, definition := range definitions {
		bySymbol[definition.Symbol] = len(definition.Deployments)
	}

	if bySymbol["CHZ"] < 4 {
		t.Fatalf("CHZ deployments = %d, want at least 4", bySymbol["CHZ"])
	}
	if bySymbol["PEPPER"] != 3 {
		t.Fatalf("PEPPER deployments = %d, want 3", bySymbol["PEPPER"])
	}
}

func TestNewAssetRegistryDefinitionsHaveLogos(t *testing.T) {
	registry := NewAssetRegistry()
	for _, definition := range registry.ListDefinitions() {
		if got := registry.LogoURL(definition.Symbol); got == "" {
			t.Fatalf("missing logo for asset %s", definition.Symbol)
		}
	}
}

func TestStaticLogoFilesExist(t *testing.T) {
	registry := NewAssetRegistry()
	for _, definition := range registry.ListDefinitions() {
		assertStaticAssetFile(t, registry.LogoURL(definition.Symbol))
	}
	for _, chainID := range constants.AllChainIDs() {
		assertStaticAssetFile(t, asset.ChainLogoURL(chainID))
	}
}

func assertStaticAssetFile(t *testing.T, url string) {
	t.Helper()
	if !strings.HasPrefix(url, "/static/") {
		t.Fatalf("static asset URL %q must start with /static/", url)
	}
	path := filepath.Join("..", "..", "static", strings.TrimPrefix(url, "/static/"))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("static asset %q is missing at %s: %v", url, path, err)
	}
	if info.IsDir() {
		t.Fatalf("static asset %q points to a directory: %s", url, path)
	}
}
