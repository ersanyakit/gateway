package main

import (
	"core/constants"
	"os"
	"strings"
	"testing"
)

func TestChainIndexerEventHandlerDoesNotMutateBusinessState(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := extractMainFunctionBody(t, string(sourceBytes), "handleChainIndexerEvent")
	for _, forbidden := range []string{
		"TransactionRepo.Create",
		"PaymentRepo.MarkPaidByTransaction",
		"LedgerRepo.",
		"WebhookDeliveryRepo.",
		"handleDepositWebhook",
		"handlePaymentDeposit",
		"enqueueSweepJob",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("handleChainIndexerEvent must not mutate business state through %q", forbidden)
		}
	}
	if !strings.Contains(body, "recordChainFactObservation") {
		t.Fatal("handleChainIndexerEvent must persist a chain fact")
	}
}

func TestMainDispatcherSubscriberUsesChainIndexerEventHandler(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	source := string(sourceBytes)
	if !strings.Contains(source, "handleChainIndexerEvent(mainCtx, event)") {
		t.Fatal("dispatcher subscriber must route observed transactions through handleChainIndexerEvent")
	}
	for _, forbidden := range []string{
		"TransactionRepo.Create(*tx)",
		"handlePaymentDeposit(mainCtx",
		"handleDepositWebhook(mainCtx",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("dispatcher subscriber still contains direct business mutation %q", forbidden)
		}
	}
}

func TestMainDispatcherSubscriberAcksAfterChainFactPersistence(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	source := string(sourceBytes)
	callIndex := strings.Index(source, "err := handleChainIndexerEvent(mainCtx, event)")
	if callIndex == -1 {
		t.Fatal("dispatcher subscriber must call handleChainIndexerEvent")
	}
	ackIndex := strings.Index(source[callIndex:], "event.Ack <- err")
	if ackIndex == -1 {
		t.Fatal("dispatcher subscriber must ack with the chain fact persistence error")
	}
	if earlierAck := strings.Index(source[callIndex:callIndex+ackIndex], "event.Ack <-"); earlierAck != -1 {
		t.Fatal("dispatcher subscriber must not ack before chain fact persistence completes")
	}
}

func TestDepositFactWorkerOwnsDepositBoundary(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	source := string(sourceBytes)
	for _, token := range []string{
		"func startDepositFactWorker(",
		"func processDepositFacts(",
		"depositsvc.New(",
		"service.ProcessBatch(ctx, 200)",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("deposit boundary worker missing %q", token)
		}
	}
	body := extractMainFunctionBody(t, source, "processDepositFacts")
	for _, forbidden := range []string{
		"PaymentRepo.MarkPaidByTransaction",
		"LedgerRepo.",
		"WebhookDeliveryRepo.",
		"handleDepositWebhook",
		"handlePaymentDeposit",
		"enqueueSweepJob",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("deposit fact worker must not directly mutate settlement through %q", forbidden)
		}
	}
}

func TestLedgerInvariantReconciliationLogsScopedContext(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	source := string(sourceBytes)
	body := extractMainFunctionBody(t, source, "runLedgerInvariantReconciliation")
	for _, token := range []string{
		"ledgerInvariantReason(issue)",
		"ledgerInvariantCorrelationID(issue)",
		"correlation_id=%s",
		"merchant=%s",
		"domain=%s",
		"chain=%d",
		"token=%s",
		"symbol=%s",
		"net=%s",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("ledger invariant reconciliation missing scoped token %q", token)
		}
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "raw_signature"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("ledger invariant reconciliation must not log secret marker %q", forbidden)
		}
	}
	if !strings.Contains(source, "func ledgerInvariantReason(") || !strings.Contains(source, "func ledgerInvariantDomainID(") {
		t.Fatal("ledger invariant reconciliation helper functions are missing")
	}
}

func TestChainConfirmationRequirementDefaults(t *testing.T) {
	t.Setenv("FINALITY_CONFIRMATIONS_DEFAULT", "")
	t.Setenv("CHAIN_0_CONFIRMATIONS", "")
	t.Setenv("BITCOIN_CONFIRMATIONS", "")
	t.Setenv("CHAIN_99999999_CONFIRMATIONS", "")
	t.Setenv("SOLANA_CONFIRMATIONS", "")
	t.Setenv("CHAIN_99999998_CONFIRMATIONS", "")
	t.Setenv("TRON_CONFIRMATIONS", "")
	t.Setenv("CHAIN_1_CONFIRMATIONS", "")
	t.Setenv("ETHEREUM_CONFIRMATIONS", "")

	cases := []struct {
		name    string
		chainID constants.ChainID
		want    uint
	}{
		{name: "bitcoin", chainID: constants.Bitcoin, want: 3},
		{name: "solana", chainID: constants.Solana, want: 1},
		{name: "tron", chainID: constants.TRON, want: 20},
		{name: "evm-default", chainID: constants.Ethereum, want: 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chainConfirmationRequirement(tc.chainID); got != tc.want {
				t.Fatalf("chainConfirmationRequirement(%d) = %d, want %d", tc.chainID, got, tc.want)
			}
		})
	}
}

func TestLedgerInvariantReconciliationUsesScopedContext(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	source := string(sourceBytes)
	body := extractMainFunctionBody(t, source, "runLedgerInvariantReconciliation")
	for _, token := range []string{
		"correlationID",
		"issue.MerchantID",
		"domainID",
		"ledgerInvariantReason(issue)",
		"ledgerInvariantCorrelationID(issue)",
		"CreateOpenIfMissing",
		"Reconciliation job opened correlation_id=%s merchant=%s domain=%s",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("ledger invariant reconciliation missing scoped context token %q", token)
		}
	}
	for _, token := range []string{
		`"ledger_invariant:" + issue.IdempotencyKey`,
		`fmt.Sprintf("ledger_invariant:%s:%s:%d:%s"`,
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("ledger invariant helper missing token %q", token)
		}
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "raw_signature"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("ledger invariant reconciliation must not log sensitive token %q", forbidden)
		}
	}
}

func extractMainFunctionBody(t *testing.T, source, functionName string) string {
	t.Helper()
	start := strings.Index(source, "func "+functionName+"(")
	if start == -1 {
		t.Fatalf("function %s not found", functionName)
	}
	open := strings.Index(source[start:], "{")
	if open == -1 {
		t.Fatalf("function %s has no opening brace", functionName)
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
	t.Fatalf("function %s has no closing brace", functionName)
	return ""
}
