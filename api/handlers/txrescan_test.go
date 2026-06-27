package handlers

import (
	"errors"
	"testing"

	"core/constants"
	"core/services/txrescan"
)

func TestParseTxRescanChainAcceptsIDsAndSlugs(t *testing.T) {
	tests := map[string]constants.ChainID{
		"1":            constants.Ethereum,
		"ethereum":     constants.Ethereum,
		"ETHEREUM":     constants.Ethereum,
		"base":         constants.Base,
		"bnbchain":     constants.Binance,
		"bsc":          constants.Binance,
		"bitcoin":      constants.Bitcoin,
		"solana":       constants.Solana,
		"tron":         constants.TRON,
		"chiliz-spicy": constants.ChilizSpicy,
		"arbitrum-one": constants.Arbitrum,
		"99999998":     constants.TRON,
		"99999999":     constants.Solana,
	}
	for input, expected := range tests {
		got, err := parseTxRescanChain(input)
		if err != nil {
			t.Fatalf("parseTxRescanChain(%q) error: %v", input, err)
		}
		if got != expected {
			t.Fatalf("parseTxRescanChain(%q) = %d, want %d", input, got, expected)
		}
	}
}

func TestParseTxRescanChainRejectsUnsupportedIDs(t *testing.T) {
	for _, input := range []string{"", "554576", "unknown-chain"} {
		if _, err := parseTxRescanChain(input); err == nil {
			t.Fatalf("parseTxRescanChain(%q) should fail", input)
		}
	}
}

func TestTxRescanMessages(t *testing.T) {
	if got := txRescanSuccessMessage(&txrescan.Result{Chain: "ethereum", Events: 2}); got != "ethereum tx yeniden tarandı: 2 event işlendi." {
		t.Fatalf("success message = %q", got)
	}
	got := txRescanSuccessMessage(&txrescan.Result{
		Chain:                "tron",
		Events:               1,
		DepositsMatched:      1,
		TransactionsRecorded: 1,
		DepositsFinalized:    1,
	})
	want := "tron tx yeniden tarandı: 1 event işlendi, 1 deposit eşleşti, 1 transaction kaydedildi, 1 deposit finalize oldu."
	if got != want {
		t.Fatalf("detailed success message = %q, want %q", got, want)
	}
	tests := map[error]string{
		txrescan.ErrTransactionNotFound: "Tx blockchain üzerinde bulunamadı.",
		txrescan.ErrUnauthorizedTx:      "Bu tx üye işyeri wallet adresleriyle eşleşmiyor.",
		txrescan.ErrUnsupportedChain:    "Bu blockchain için rescan desteklenmiyor.",
		errors.New("custom error"):      "custom error",
	}
	for err, expected := range tests {
		if got := txRescanErrorMessage(err); got != expected {
			t.Fatalf("error message for %v = %q, want %q", err, got, expected)
		}
	}
}

func TestFirstNonEmptyTrimsForDecisionButReturnsOriginal(t *testing.T) {
	if got := firstNonEmpty("", "   ", " tx "); got != " tx " {
		t.Fatalf("firstNonEmpty = %q", got)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Fatalf("empty firstNonEmpty = %q", got)
	}
}
