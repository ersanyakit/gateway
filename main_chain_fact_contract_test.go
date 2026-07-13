package main

import (
	"context"
	"core/asset"
	"core/constants"
	"core/models"
	"core/repositories"
	"core/types"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
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
	if !strings.Contains(body, "chainFactEligibleForPersistence") || !strings.Contains(body, "chainFactAddressOwnedByPlatform") {
		t.Fatal("handleChainIndexerEvent must filter observations through platform wallet ownership")
	}
	if !strings.Contains(body, "chainFactAssetSupported") {
		t.Fatal("handleChainIndexerEvent must reject assets outside the configured registry")
	}
	if !strings.Contains(body, "chainFactSameMerchantInternalTransfer") {
		t.Fatal("handleChainIndexerEvent must reject same-merchant internal custody transfers")
	}
}

func TestChainFactAssetSupportRejectsUnknownTokens(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	registeredToken := "0x1111111111111111111111111111111111111111"
	unknownToken := "0x2222222222222222222222222222222222222222"
	registry.Register(asset.NewERC20(constants.Ethereum, registeredToken, "USDC", "USD Coin", 6))

	for _, tt := range []struct {
		name  string
		token *string
		want  bool
	}{
		{name: "registered native", want: true},
		{name: "registered token", token: &registeredToken, want: true},
		{name: "unknown token", token: &unknownToken, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			eventType := "native_transfer"
			if tt.token != nil {
				eventType = "token_transfer"
			}
			got, err := chainFactAssetSupported(registry, eventType, types.TransactionParam{ChainID: constants.Ethereum, Token: tt.token})
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("supported = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChainFactAssetSupportRejectsTokenEventWithoutTokenIdentity(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))

	got, err := chainFactAssetSupported(registry, "token_transfer", types.TransactionParam{ChainID: constants.Ethereum})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("token event without a token identity must not be treated as a native transfer")
	}
}

func TestChainFactOwnershipHotPathUsesAuthoritativeMemoryIndex(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := extractMainFunctionBody(t, string(sourceBytes), "chainFactAddressOwnedByPlatform")
	for _, required := range []string{"addrIndex.Ready()", "addrIndex.Get(chainID, address)"} {
		if !strings.Contains(body, required) {
			t.Fatalf("ownership hot path missing %q", required)
		}
	}
	for _, forbidden := range []string{"WalletRepo", "FindByChainAddress", "walletForAddress"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("ownership hot path must not query the database through %q", forbidden)
		}
	}
}

func TestChainFactPersistenceRequiresPositiveInboundToOwnedAddress(t *testing.T) {
	ownedAddress := "0xowned"
	otherAddress := "0xother"
	positiveAmount := "42"
	zeroAmount := "0"
	confirmedStatus := models.TransactionStatusConfirmed
	failedStatus := models.TransactionStatusFailed
	pendingStatus := models.TransactionStatusPending
	lookupErr := errors.New("lookup unavailable")

	tests := []struct {
		name        string
		tx          types.TransactionParam
		lookup      chainFactAddressOwnershipLookup
		want        bool
		wantErr     error
		wantLookups int
		wantAddress string
	}{
		{
			name: "owned inbound transfer",
			tx:   types.TransactionParam{ChainID: constants.Ethereum, To: &ownedAddress, Amount: &positiveAmount, Status: &confirmedStatus},
			want: true, wantLookups: 1, wantAddress: ownedAddress,
		},
		{
			name: "unowned inbound transfer",
			tx:   types.TransactionParam{ChainID: constants.Ethereum, To: &otherAddress, Amount: &positiveAmount, Status: &confirmedStatus},
			want: false, wantLookups: 1, wantAddress: otherAddress,
		},
		{
			name: "missing recipient",
			tx:   types.TransactionParam{ChainID: constants.Ethereum, Amount: &positiveAmount, Status: &confirmedStatus},
			want: false,
		},
		{
			name: "zero amount",
			tx:   types.TransactionParam{ChainID: constants.Ethereum, To: &ownedAddress, Amount: &zeroAmount, Status: &confirmedStatus},
			want: false,
		},
		{
			name: "failed transfer",
			tx:   types.TransactionParam{ChainID: constants.Ethereum, To: &ownedAddress, Amount: &positiveAmount, Status: &failedStatus},
			want: false,
		},
		{
			name: "pending transfer",
			tx:   types.TransactionParam{ChainID: constants.Ethereum, To: &ownedAddress, Amount: &positiveAmount, Status: &pendingStatus},
			want: false,
		},
		{
			name: "missing status",
			tx:   types.TransactionParam{ChainID: constants.Ethereum, To: &ownedAddress, Amount: &positiveAmount},
			want: false,
		},
		{
			name: "lookup failure",
			tx:   types.TransactionParam{ChainID: constants.Ethereum, To: &ownedAddress, Amount: &positiveAmount, Status: &confirmedStatus},
			want: false, wantErr: lookupErr, wantLookups: 1, wantAddress: ownedAddress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookups := 0
			lookup := tt.lookup
			if lookup == nil {
				lookup = func(_ context.Context, _ constants.ChainID, address string) (bool, error) {
					lookups++
					if address != tt.wantAddress {
						t.Fatalf("lookup address = %q, want %q", address, tt.wantAddress)
					}
					if tt.wantErr != nil {
						return false, tt.wantErr
					}
					return address == ownedAddress, nil
				}
			}
			got, err := chainFactEligibleForPersistence(context.Background(), tt.tx, lookup)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("eligible = %v, want %v", got, tt.want)
			}
			if lookups != tt.wantLookups {
				t.Fatalf("lookup calls = %d, want %d", lookups, tt.wantLookups)
			}
		})
	}
}

func TestMainDispatcherSubscriberUsesChainIndexerEventHandler(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	source := string(sourceBytes)
	if !strings.Contains(source, "handleChainIndexerEvent(ctx, event)") {
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
	callIndex := strings.Index(source, "err := handleChainIndexerEvent(ctx, event)")
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

func TestLegacyPaymentDepositHandlerUsesExplicitMatchResult(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := extractMainFunctionBody(t, string(sourceBytes), "handlePaymentDeposit")
	for _, token := range []string{
		"PaymentRepo.MatchFinalizedTransaction",
		"matchResult.LedgerEligible",
		"LedgerRepo.PostDepositAvailable",
		"createPaymentWebhookDelivery",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("handlePaymentDeposit missing explicit matching token %q", token)
		}
	}
	if strings.Contains(body, "PaymentRepo.MarkPaidByTransaction") {
		t.Fatal("handlePaymentDeposit must not use paid-only matching wrapper")
	}
}

func TestPaymentRealtimeBroadcastTreatsExplicitOutcomeStatusesAsTerminal(t *testing.T) {
	tests := []struct {
		status  string
		outcome string
	}{
		{status: models.PaymentStatusUnderpaid, outcome: models.PaymentOutcomeUnderpaid},
		{status: models.PaymentStatusOverpaid, outcome: models.PaymentOutcomeOverpaid},
		{status: models.PaymentStatusPartialPaid, outcome: models.PaymentOutcomePartialUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.status+"/"+tt.outcome, func(t *testing.T) {
			session := &models.PaymentSession{
				ID:             uuid.New(),
				SessionToken:   "token-" + tt.status,
				Status:         tt.status,
				PaymentOutcome: tt.outcome,
			}
			event := paymentRealtimeBroadcastEvent(session)
			if event.Status != tt.status || !event.Terminal || event.Payable || event.Paid {
				t.Fatalf("event for %s = %#v, want terminal non-payable non-paid", tt.status, event)
			}
			if event.ResultPath != "/checkout/"+session.SessionToken+"/pay" {
				t.Fatalf("event result path = %q", event.ResultPath)
			}
		})
	}
}

func TestPaymentRealtimeBroadcastKeepsAggregatePartialPayable(t *testing.T) {
	session := &models.PaymentSession{
		ID:             uuid.New(),
		SessionToken:   "token-partial-aggregate",
		Status:         models.PaymentStatusPartialPaid,
		PaymentOutcome: models.PaymentOutcomePartialAggregating,
	}
	event := paymentRealtimeBroadcastEvent(session)
	if event.Status != models.PaymentStatusPartialPaid || event.Terminal || !event.Payable || event.Paid {
		t.Fatalf("aggregate partial event = %#v, want payable nonterminal non-paid", event)
	}
	if event.ResultPath != "" {
		t.Fatalf("aggregate partial event result path = %q, want empty", event.ResultPath)
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
		"CreateScopedOpenIfMissing",
		"ReconciliationScope",
		"AffectedResourceIDs",
		"Evidence",
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

func TestLedgerInvariantReasonIsBoundedAndDistinctForLongKeys(t *testing.T) {
	merchantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	base := repositories.LedgerInvariantIssue{
		MerchantID: merchantID,
		ChainID:    int64(constants.Ethereum),
	}
	first := base
	first.IdempotencyKey = strings.Repeat("same-prefix-", 20) + "first"
	second := base
	second.IdempotencyKey = strings.Repeat("same-prefix-", 20) + "second"

	firstReason := ledgerInvariantReason(first)
	secondReason := ledgerInvariantReason(second)
	if len(firstReason) > 120 || len(secondReason) > 120 {
		t.Fatalf("ledger invariant reasons must fit DB size: %q %q", firstReason, secondReason)
	}
	if firstReason == secondReason {
		t.Fatalf("long invariant reasons collided: %q", firstReason)
	}
	if !strings.Contains(firstReason, ":h=") || !strings.Contains(secondReason, ":h=") {
		t.Fatalf("truncated invariant reasons must include hash suffix: %q %q", firstReason, secondReason)
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
