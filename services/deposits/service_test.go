package deposits

import (
	"context"
	"os"
	"strings"
	"testing"

	"core/asset"
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

func TestDepositAssetBoundaryRejectsUnsupportedAndMissingTokenIdentity(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	registeredToken := "0x1111111111111111111111111111111111111111"
	unknownToken := "0x2222222222222222222222222222222222222222"
	registry.Register(asset.NewERC20(constants.Ethereum, registeredToken, "USDC", "USD Coin", 6))
	svc := &Service{deps: Dependencies{AssetRegistry: registry}}

	cases := []struct {
		name      string
		eventType string
		token     *string
		want      bool
	}{
		{name: "native", eventType: "native_transfer", want: true},
		{name: "registered token", eventType: "token_transfer", token: &registeredToken, want: true},
		{name: "unknown token", eventType: "token_transfer", token: &unknownToken, want: false},
		{name: "missing token identity", eventType: "token_transfer", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fact := serviceTestFact("1:0xasset:log:1")
			fact.SourceEventType = tc.eventType
			fact.Token = tc.token
			got, err := svc.chainFactAssetSupported(fact)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("supported = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChainFactSourceAddressesIncludesEveryBitcoinInput(t *testing.T) {
	fact := serviceTestFact("0:btc:vout:0")
	fact.RawMetadataJSON = `{"from":"bc1first","from_addresses":["bc1first","bc1platform"," ","bc1platform"]}`
	got := chainFactSourceAddresses(fact)
	if strings.Join(got, ",") != "bc1first,bc1platform" {
		t.Fatalf("source addresses = %#v", got)
	}
}

func TestCorrectedChainFactReturnsBeforeDepositProcessing(t *testing.T) {
	svc := &Service{}
	fact := serviceTestFact("1:0xreorg:log:1")
	fact.Status = models.ChainFactStatusReorged

	summary, err := svc.ProcessFact(context.Background(), fact)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FactsProcessed != 1 || summary.Matched != 0 || summary.TransactionsRecorded != 0 {
		t.Fatalf("summary = %#v, want only fact processed", summary)
	}
}

func TestFailedChainFactReturnsBeforeDepositProcessing(t *testing.T) {
	svc := &Service{}
	fact := serviceTestFact("1:0xfailed:log:1")
	fact.RawMetadataJSON = `{"from":"0xsender","to":"0xowned","status":"failed"}`

	summary, err := svc.ProcessFact(context.Background(), fact)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FactsProcessed != 1 || summary.Matched != 0 || summary.TransactionsRecorded != 0 {
		t.Fatalf("summary = %#v, want failed fact rejected before deposit processing", summary)
	}
	tx := transactionParamFromChainFact(context.Background(), fact)
	if tx.Status == nil || *tx.Status != models.TransactionStatusFailed {
		t.Fatalf("failed fact transaction status = %#v", tx.Status)
	}
}

func TestDepositFinalityPropagatesChainStateErrors(t *testing.T) {
	body := extractFunctionBody(t, readDepositServiceSource(t), "factWithFinality")
	for _, required := range []string{
		"state, err := s.deps.ChainStateRepo.Get",
		"return fact, fmt.Errorf",
		"if state == nil",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("factWithFinality must propagate chain-state failures; missing %q", required)
		}
	}
}

func TestPendingDepositSkipsCorrectedChainFactBeforeSettlement(t *testing.T) {
	source := readDepositServiceSource(t)
	body := extractFunctionBody(t, source, "ProcessPendingDeposit")
	correctedIndex := strings.Index(body, "if chainFactCorrected(*fact)")
	if correctedIndex == -1 {
		t.Fatal("ProcessPendingDeposit must skip corrected chain facts")
	}
	settlementIndex := strings.Index(body, "ensureDepositTransaction")
	if settlementIndex == -1 {
		t.Fatal("ProcessPendingDeposit must call settlement for non-corrected facts")
	}
	if correctedIndex > settlementIndex {
		t.Fatal("corrected chain fact guard must run before settlement")
	}
}

func TestUnmatchedFactReturnsBeforeSettlementAdapter(t *testing.T) {
	source := readDepositServiceSource(t)
	body := extractFunctionBody(t, source, "ProcessFact")
	unmatchedIndex := strings.Index(body, "if wallet == nil")
	if unmatchedIndex == -1 {
		t.Fatal("ProcessFact must branch before deposit creation when wallet is not matched")
	}
	ignoredIndex := strings.Index(body, "MarkIgnored(ctx, fact.EventID, chainFactIgnoredReason(fact))")
	if ignoredIndex == -1 {
		t.Fatal("unmatched chain facts must be marked ignored instead of becoming deposits")
	}
	consumeIndex := strings.Index(body, "ConsumeChainFact(ctx, fact, wallet)")
	if consumeIndex == -1 {
		t.Fatal("ProcessFact must consume matched chain facts")
	}
	adapterIndex := strings.Index(body, "ensureDepositTransaction")
	if adapterIndex == -1 {
		t.Fatal("ProcessFact must call settlement adapter for matched deposits")
	}
	if !(unmatchedIndex < ignoredIndex && ignoredIndex < consumeIndex && consumeIndex < adapterIndex) {
		t.Fatal("unmatched chain facts must be ignored before deposit creation and settlement")
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

func TestFinalizedDepositSchedulesSweepJobBeforeSettlement(t *testing.T) {
	source := readDepositServiceSource(t)
	body := extractFunctionBody(t, source, "ensureDepositTransaction")
	markIndex := strings.Index(body, "MarkFinality(ctx, uniqueHash, fact.Confirmations, fact.ConfirmationsRequired, true)")
	sweepIndex := strings.Index(body, "enqueueFinalizedSweepJob(ctx, finalizedTx, wallet)")
	settleIndex := strings.Index(body, "settleFinalizedTransaction(ctx, finalizedTx, deposit)")
	if markIndex == -1 || sweepIndex == -1 || settleIndex == -1 {
		t.Fatalf("finalized path missing mark/sweep/settle calls")
	}
	if !(markIndex < sweepIndex && sweepIndex < settleIndex) {
		t.Fatal("finalized deposit must enqueue sweep job after finality and before settlement")
	}
}

func TestDepositServiceSweepSchedulingSkipsReserveWallets(t *testing.T) {
	source := readDepositServiceSource(t)
	body := extractFunctionBody(t, source, "enqueueFinalizedSweepJob")
	for _, token := range []string{
		"wallet.HDAddressId == 0",
		"SweepJobRepo.EnqueueForTransaction(ctx, *txModel)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("enqueueFinalizedSweepJob missing %q", token)
		}
	}
	for _, ghostCallback := range []string{"SweepLifecycleEnqueue", "WebhookEventSweepRequestedV1", "enqueueSweepLifecycleWebhook"} {
		if strings.Contains(source, ghostCallback) {
			t.Fatalf("deposit service must not retain non-transactional sweep lifecycle callback %q", ghostCallback)
		}
	}
}

func TestFinalizedSettlementUsesExplicitPaymentMatchResult(t *testing.T) {
	source := readDepositServiceSource(t)
	body := extractFunctionBody(t, source, "settleFinalizedTransaction")
	for _, token := range []string{
		"MatchFinalizedDeposit(ctx, *txModel, deposit)",
		"matchResult.LedgerEligible",
		"matchResult.Session",
		"postFinalizedDepositAvailable(ctx, *txModel, matchResult.Session)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("settleFinalizedTransaction missing %q", token)
		}
	}
	if strings.Contains(body, "MarkPaidByTransaction") {
		t.Fatal("settlement must consume explicit match result instead of paid-only wrapper")
	}
	if strings.Contains(body, "matchResult != nil && matchResult.Changed && matchResult.Session") {
		t.Fatal("ledger retry path must not require a newly changed payment match")
	}
}

func TestFinalizedSettlementKeepsLedgerAvailableForUnmatchedCheckoutDeposits(t *testing.T) {
	source := readDepositServiceSource(t)
	settleBody := extractFunctionBody(t, source, "settleFinalizedTransaction")
	if !strings.Contains(settleBody, "postFinalizedDepositAvailable(ctx, *txModel, nil)") {
		t.Fatal("unmatched finalized wallet deposits must still post available ledger entries")
	}
	if strings.Contains(settleBody, "isStandaloneDepositWalletProduct") {
		t.Fatal("finalized ledger availability must not be limited to standalone wallet products")
	}

	helperBody := extractFunctionBody(t, source, "postFinalizedDepositAvailable")
	for _, token := range []string{
		"FindByTxUniqueHash(ctx, txModel.UniqueHash)",
		"PostDepositAvailable(ctx, *matchedSession, txModel)",
		"PostStandaloneDepositAvailable(ctx, txModel)",
	} {
		if !strings.Contains(helperBody, token) {
			t.Fatalf("postFinalizedDepositAvailable missing %q", token)
		}
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
