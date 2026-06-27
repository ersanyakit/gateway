package deposits

import (
	"context"
	"os"
	"strings"
	"testing"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestConfirmationsForBlockUsesProcessedHeadFallback(t *testing.T) {
	if got := confirmationsForBlock(100, 110, 108); got != 11 {
		t.Fatalf("confirmations = %d, want 11", got)
	}
	if got := confirmationsForBlock(111, 110, 110); got != 0 {
		t.Fatalf("future block confirmations = %d, want 0", got)
	}
	if got := confirmationsForBlock(100, 110, 0); got != 11 {
		t.Fatalf("fallback processed head confirmations = %d, want 11", got)
	}
}

func TestTransactionParamFromChainFactUsesObservedDepositAddress(t *testing.T) {
	fact := serviceTestFact("1:0xabc:log:1")
	fact.RawMetadataJSON = `{"from":"0xsender","to":"0xother"}`
	fact.Finalized = true

	tx := transactionParamFromChainFact(context.Background(), fact)
	if tx.To == nil || *tx.To != fact.ObservedAddress {
		t.Fatalf("to = %#v, want observed address %q", tx.To, fact.ObservedAddress)
	}
	if tx.From == nil || *tx.From != "0xsender" {
		t.Fatalf("from = %#v, want metadata from", tx.From)
	}
	if tx.Status == nil || *tx.Status != models.TransactionStatusConfirmed {
		t.Fatalf("status = %#v, want confirmed", tx.Status)
	}
	if tx.Block == nil || *tx.Block != "123" || tx.LogIndex == nil || *tx.LogIndex != "log:1" {
		t.Fatalf("tx metadata = %#v", tx)
	}
}

func TestDepositSafetyHelpers(t *testing.T) {
	if !positiveAmount("100") || positiveAmount("0") || positiveAmount("-1") || positiveAmount("1.5") {
		t.Fatal("positiveAmount should accept only positive integer raw amounts")
	}
	if !isStandaloneDepositWalletProduct("static:user") || !isStandaloneDepositWalletProduct("wallet:user") {
		t.Fatal("standalone wallet product prefixes should be recognized")
	}
	if isStandaloneDepositWalletProduct("checkout:order") {
		t.Fatal("checkout product should not be treated as standalone wallet")
	}
}

func TestUnmatchedFactReturnsBeforeSettlementAdapter(t *testing.T) {
	source := readDepositServiceSource(t)
	body := extractFunctionBody(t, source, "ProcessFact")
	unmatchedIndex := strings.Index(body, "if deposit.WalletID == nil")
	if unmatchedIndex == -1 {
		t.Fatal("ProcessFact must branch on unmatched deposits")
	}
	adapterIndex := strings.Index(body, "ensureDepositTransaction")
	if adapterIndex == -1 {
		t.Fatal("ProcessFact must call settlement adapter for matched deposits")
	}
	if unmatchedIndex > adapterIndex {
		t.Fatal("unmatched deposits must return before settlement adapter")
	}
}

func TestPendingFinalityDoesNotSettlePaymentOrAvailableLedger(t *testing.T) {
	source := readDepositServiceSource(t)
	body := extractFunctionBody(t, source, "ensureDepositTransaction")
	guardIndex := strings.Index(body, "if !fact.Finalized")
	if guardIndex == -1 {
		t.Fatal("ensureDepositTransaction must guard pre-finality deposits")
	}
	preFinality := body[:guardIndex]
	for _, forbidden := range []string{
		"MarkPaidByTransaction",
		"PostDepositAvailable",
		"PostStandaloneDepositAvailable",
		"enqueueSweepJob",
	} {
		if strings.Contains(preFinality, forbidden) {
			t.Fatalf("pre-finality path must not call %q", forbidden)
		}
	}
	if settleIndex := strings.Index(body, "settleFinalizedTransaction"); settleIndex == -1 || settleIndex < guardIndex {
		t.Fatal("settlement must happen only after finality guard")
	}
	settleIndex := strings.Index(body, "settleFinalizedTransaction")
	preSettlement := body[guardIndex:settleIndex]
	if !strings.Contains(preSettlement, "MarkFinality(ctx, uniqueHash, fact.Confirmations, fact.ConfirmationsRequired, false)") {
		t.Fatal("pre-finality path must persist non-finalized transaction state")
	}
	if !strings.Contains(preSettlement, "return summary, err") {
		t.Fatal("pre-finality path must return before settlement")
	}
	if !strings.Contains(preSettlement, "MarkFinality(ctx, uniqueHash, fact.Confirmations, fact.ConfirmationsRequired, true)") {
		t.Fatal("finalized path must mark transaction final before settlement")
	}
}

func readDepositServiceSource(t *testing.T) string {
	t.Helper()
	sourceBytes, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("read service.go: %v", err)
	}
	return string(sourceBytes)
}

func extractFunctionBody(t *testing.T, source, functionName string) string {
	t.Helper()
	start := strings.Index(source, "func ")
	for start != -1 {
		remaining := source[start:]
		if strings.Contains(remaining[:min(len(remaining), 120)], functionName+"(") {
			open := strings.Index(remaining, "{")
			if open == -1 {
				break
			}
			index := start + open
			depth := 0
			for i := index; i < len(source); i++ {
				switch source[i] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						return source[index : i+1]
					}
				}
			}
		}
		next := strings.Index(remaining[5:], "func ")
		if next == -1 {
			break
		}
		start += 5 + next
	}
	t.Fatalf("function %s not found", functionName)
	return ""
}

func serviceTestFact(eventID string) models.ChainFact {
	return models.ChainFact{
		ID:                    uuid.New(),
		EventID:               eventID,
		ChainID:               constants.Ethereum,
		BlockNumber:           123,
		BlockHash:             "0xblock",
		TxHash:                "0xabc",
		LogIndex:              "log:1",
		ObservedAddress:       "0xto",
		Direction:             models.ChainFactDirectionTo,
		Symbol:                "ETH",
		Decimals:              18,
		AmountRaw:             "100",
		Confirmations:         3,
		ConfirmationsRequired: 12,
		SourceEventType:       "native_transfer",
		RawMetadataJSON:       `{}`,
	}
}
