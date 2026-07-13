package repositories

import (
	"errors"
	"testing"

	"core/constants"
	"core/models"
	"core/types"
)

func FuzzBuildChainFactTransferAmountAddressAsset(f *testing.F) {
	f.Add(int64(constants.Ethereum), "0xABC", "log:1", "100", "0xFrom", "0xTo", "ETH", "", "123", models.TransactionStatusConfirmed)
	f.Add(int64(constants.Bitcoin), "btc-tx", "vout:0", "1", "bc1sender", "bc1receiver", "BTC", "", "7", models.TransactionStatusConfirmed)
	f.Add(int64(constants.Solana), "solsig", "ix:2", "0", "source", "dest", "SOL", "", "9", models.TransactionStatusConfirmed)
	f.Add(int64(constants.TRON), "tron-tx", "log:0", "-1", "from", "to", "USDT", "contract", "12", models.TransactionStatusConfirmed)
	f.Add(int64(constants.Ethereum), "0xbad", "log:0", "not-a-number", "from", "to", "ETH", "", "12", models.TransactionStatusConfirmed)
	f.Add(int64(constants.Ethereum), "0xplus", "log:0", "+1", "from", "to", "ETH", "", "12", models.TransactionStatusConfirmed)

	f.Fuzz(func(t *testing.T, chainIDValue int64, txHash, logIndex, amount, from, to, symbol, token, block, status string) {
		tx := types.TransactionParam{
			ChainID:  constants.ChainID(chainIDValue),
			Hash:     fuzzStringPtr(limitFuzzString(txHash, 160)),
			Block:    fuzzStringPtr(limitFuzzString(block, 32)),
			From:     fuzzStringPtr(limitFuzzString(from, 180)),
			To:       fuzzStringPtr(limitFuzzString(to, 180)),
			Symbol:   fuzzStringPtr(limitFuzzString(symbol, 32)),
			Decimals: 18,
			Amount:   fuzzStringPtr(limitFuzzString(amount, 128)),
			LogIndex: fuzzStringPtr(limitFuzzString(logIndex, 80)),
			Status:   fuzzStringPtr(limitFuzzString(status, 40)),
		}
		if token = limitFuzzString(token, 180); token != "" {
			tx.Token = &token
		}
		if tx.Block != nil && *tx.Block != "" {
			blockHash := "block-" + *tx.Block
			tx.BlockHash = &blockHash
		}

		fact, err := BuildChainFact(ChainFactBuildParams{
			EventType:             "native_transfer",
			Transaction:           tx,
			Confirmations:         1,
			ConfirmationsRequired: 1,
		})
		if err != nil {
			if !errors.Is(err, ErrChainFactInvalid) {
				t.Fatalf("unexpected error: %v", err)
			}
			return
		}
		if !chainFactPositiveRaw(fact.AmountRaw) {
			t.Fatalf("accepted non-positive transfer amount %q", fact.AmountRaw)
		}
		if fact.ObservedAddress == "" || fact.Symbol == "" || fact.EventID == "" {
			t.Fatalf("accepted incomplete fact: %#v", fact)
		}
	})
}

func fuzzStringPtr(value string) *string {
	return &value
}

func limitFuzzString(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
