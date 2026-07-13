package application

import (
	"testing"

	"core/constants"
)

func TestNewChainFactoryRegistersEverySupportedChain(t *testing.T) {
	factory := NewChainFactory()
	for _, chainID := range constants.AllChainIDs() {
		chain, err := factory.GetChainByID(chainID)
		if err != nil {
			t.Fatalf("chain id %d (%s) is not registered: %v", chainID, constants.ChainName(chainID), err)
		}
		if chain.ChainID() != chainID {
			t.Fatalf("chain id lookup returned %d, want %d", chain.ChainID(), chainID)
		}
		if chain.Name() == "" {
			t.Fatalf("chain id %d has empty runtime name", chainID)
		}
	}
}

func TestNewChainFactoryAliases(t *testing.T) {
	factory := NewChainFactory()
	tests := map[string]constants.ChainID{
		"binance":      constants.Binance,
		"bsc":          constants.Binance,
		"spicy":        constants.ChilizSpicy,
		"nile":         constants.TRONTestnet,
		"tron-nile":    constants.TRONTestnet,
		"trx-nile":     constants.TRONTestnet,
		"shasta":       constants.TRONTestnet,
		"trx-testnet":  constants.TRONTestnet,
		"tron-shasta":  constants.TRONTestnet,
		"arb":          constants.Arbitrum,
		"arbitrum-one": constants.Arbitrum,
	}
	for alias, expected := range tests {
		chain, err := factory.GetChain(alias)
		if err != nil {
			t.Fatalf("alias %q lookup failed: %v", alias, err)
		}
		if chain.ChainID() != expected {
			t.Fatalf("alias %q chain id = %d, want %d", alias, chain.ChainID(), expected)
		}
	}
}
