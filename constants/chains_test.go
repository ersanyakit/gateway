package constants

import "testing"

func TestAllChainIDsAreSupportedAndNamed(t *testing.T) {
	seen := map[ChainID]bool{}
	for _, chainID := range AllChainIDs() {
		if seen[chainID] {
			t.Fatalf("duplicate chain id in AllChainIDs: %d", chainID)
		}
		seen[chainID] = true
		if !IsSupportedChainID(chainID) {
			t.Fatalf("chain id %d from AllChainIDs is not supported", chainID)
		}
		if ChainName(chainID) == "" {
			t.Fatalf("chain id %d has empty chain name", chainID)
		}
	}

	for chainID := range chainNames {
		if !seen[chainID] {
			t.Fatalf("supported chain id %d is missing from AllChainIDs", chainID)
		}
	}
}

func TestKnownChainNames(t *testing.T) {
	tests := map[ChainID]string{
		Bitcoin:     "bitcoin",
		Ethereum:    "ethereum",
		Base:        "base",
		Arbitrum:    "arbitrum",
		Binance:     "bnbchain",
		Unichain:    "unichain",
		Avalanche:   "avalanche",
		Chiliz:      "chiliz",
		ChilizSpicy: "chiliz-spicy",
		Solana:      "solana",
		TRON:        "tron",
	}
	for chainID, expected := range tests {
		if got := ChainName(chainID); got != expected {
			t.Fatalf("ChainName(%d) = %q, want %q", chainID, got, expected)
		}
	}
}

func TestUnsupportedChainID(t *testing.T) {
	const unsupported ChainID = 554576
	if IsSupportedChainID(unsupported) {
		t.Fatalf("chain id %d must not be supported", unsupported)
	}
	if got := ChainName(unsupported); got != "" {
		t.Fatalf("unsupported ChainName(%d) = %q, want empty", unsupported, got)
	}
}
