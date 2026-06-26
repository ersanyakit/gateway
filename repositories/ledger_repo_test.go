package repositories

import (
	"errors"
	"testing"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestReverseLedgerDirection(t *testing.T) {
	if got := reverseLedgerDirection(models.LedgerDirectionCredit); got != models.LedgerDirectionDebit {
		t.Fatalf("reverse credit = %q, want %q", got, models.LedgerDirectionDebit)
	}
	if got := reverseLedgerDirection(models.LedgerDirectionDebit); got != models.LedgerDirectionCredit {
		t.Fatalf("reverse debit = %q, want %q", got, models.LedgerDirectionCredit)
	}
}

func TestLedgerRefundAssetFromSessionRequiresSelectedChain(t *testing.T) {
	_, _, _, err := ledgerRefundAssetFromSession(models.PaymentSession{})
	if err == nil {
		t.Fatal("missing selected chain should fail")
	}
}

func TestLedgerRefundAssetFromSessionNormalizesSymbol(t *testing.T) {
	chainID := constants.Ethereum
	_, symbol, decimals, err := ledgerRefundAssetFromSession(models.PaymentSession{
		SelectedChainID:  &chainID,
		SelectedSymbol:   " usdc ",
		SelectedDecimals: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if symbol != "USDC" {
		t.Fatalf("symbol = %q, want USDC", symbol)
	}
	if decimals != 6 {
		t.Fatalf("decimals = %d, want 6", decimals)
	}
}

func TestLedgerRefundAssetFromSessionFallsBackToChainName(t *testing.T) {
	chainID := constants.Bitcoin
	_, symbol, _, err := ledgerRefundAssetFromSession(models.PaymentSession{SelectedChainID: &chainID})
	if err != nil {
		t.Fatal(err)
	}
	if symbol != "BITCOIN" {
		t.Fatalf("symbol = %q, want BITCOIN", symbol)
	}
}

func TestLedgerIdempotencyKeys(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	cases := map[string]string{
		withdrawalHoldKey(id):  "withdrawal-hold:11111111-1111-1111-1111-111111111111",
		withdrawalDebitKey(id): "withdrawal-debit:11111111-1111-1111-1111-111111111111",
		refundHoldKey(id):      "refund-hold:11111111-1111-1111-1111-111111111111",
		refundDebitKey(id):     "refund-debit:11111111-1111-1111-1111-111111111111",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("key = %q, want %q", got, want)
		}
	}
}

func TestInsufficientAvailableBalanceErrorIsComparable(t *testing.T) {
	err := errors.Join(ErrInsufficientAvailableBalance)
	if !errors.Is(err, ErrInsufficientAvailableBalance) {
		t.Fatal("insufficient balance sentinel should be comparable")
	}
}
