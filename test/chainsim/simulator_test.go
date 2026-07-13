package chainsim

import (
	"testing"

	"core/constants"
	"core/models"
	"core/repositories"
)

func TestSimulatorBuildsDeterministicChainFacts(t *testing.T) {
	cases := []struct {
		name     string
		chainID  constants.ChainID
		token    string
		symbol   string
		logIndex string
		wantID   string
	}{
		{name: "evm-token", chainID: constants.Ethereum, token: "0xToken", symbol: "USDC", logIndex: "log:7", wantID: "1:0xtx:log:7"},
		{name: "bitcoin-native", chainID: constants.Bitcoin, symbol: "BTC", logIndex: "vout:1", wantID: "0:btctx:vout:1"},
		{name: "solana-native", chainID: constants.Solana, symbol: "SOL", logIndex: "ix:2", wantID: "99999999:soltx:ix:2"},
		{name: "tron-token", chainID: constants.TRON, token: "TRC20Token", symbol: "USDT", logIndex: "log:3", wantID: "99999998:trontx:log:3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sim := New(tc.chainID)
			block := sim.EmitBlock(tc.name + "-block")
			txHash := map[constants.ChainID]string{
				constants.Ethereum: "0xTx",
				constants.Bitcoin:  "btcTx",
				constants.Solana:   "solTx",
				constants.TRON:     "tronTx",
			}[tc.chainID]
			transfer := sim.EmitTokenTransfer(block, txHash, tc.logIndex, "sender", "receiver", "100", tc.symbol, tc.token, 6)
			if tc.token == "" {
				transfer = sim.EmitNativeTransfer(block, txHash, tc.logIndex, "sender", "receiver", "100", tc.symbol)
			}

			fact, err := repositories.BuildChainFact(repositories.ChainFactBuildParams{
				EventType:             transfer.EventType,
				Transaction:           transfer.TransactionParam(),
				Confirmations:         6,
				ConfirmationsRequired: 6,
			})
			if err != nil {
				t.Fatal(err)
			}
			if fact.EventID != tc.wantID {
				t.Fatalf("event id = %q, want %q", fact.EventID, tc.wantID)
			}
			if fact.Status != models.ChainFactStatusObserved || !fact.Finalized || fact.AmountRaw != "100" {
				t.Fatalf("fact = %#v", fact)
			}
		})
	}
}

func TestSimulatorReorgDropsReplacedTransfers(t *testing.T) {
	sim := New(constants.Ethereum)
	first := sim.EmitBlock("block-1")
	second := sim.EmitBlock("block-2a")
	sim.EmitNativeTransfer(first, "0xfirst", "log:0", "a", "b", "10", "ETH")
	sim.EmitNativeTransfer(second, "0xreorged", "log:0", "a", "b", "20", "ETH")

	reorged := sim.Reorg(1, "block-2b")
	if len(reorged) != 1 || reorged[0].Hash != "block-2a" || reorged[0].Canonical {
		t.Fatalf("reorged blocks = %#v", reorged)
	}
	sim.EmitNativeTransfer(sim.blocks[len(sim.blocks)-1], "0xreplacement", "log:0", "a", "b", "30", "ETH")

	params := sim.TransactionParams()
	if len(params) != 2 {
		t.Fatalf("canonical tx count = %d, want 2", len(params))
	}
	if *params[1].Hash != "0xreplacement" || *params[1].ParentHash != "block-1" {
		t.Fatalf("replacement tx = %#v", params[1])
	}
}
