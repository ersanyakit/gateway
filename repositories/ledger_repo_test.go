package repositories

import (
	"testing"

	"core/models"
)

func TestReverseLedgerDirection(t *testing.T) {
	if got := reverseLedgerDirection(models.LedgerDirectionCredit); got != models.LedgerDirectionDebit {
		t.Fatalf("reverse credit = %q, want %q", got, models.LedgerDirectionDebit)
	}
	if got := reverseLedgerDirection(models.LedgerDirectionDebit); got != models.LedgerDirectionCredit {
		t.Fatalf("reverse debit = %q, want %q", got, models.LedgerDirectionCredit)
	}
}
