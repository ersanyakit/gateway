package main

import (
	"core/models"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExecuteAutoSweepDepositWithJobUsesReservedTokenAndNativeTronSweep(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "executeAutoSweepDepositWithJob")
	for _, token := range []string{
		"chain.WithdrawToken(ctx, *userDetails, *txModel.Token, txModel.Amount, reserveAddr)",
		"constants.IsTRONChain(txModel.ChainID)",
		"chain.SweepTo(ctx, *userDetails, reserveAddr)",
		"chain.Withdraw(ctx, *userDetails, txModel.Amount, reserveAddr)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("executeAutoSweepDepositWithJob missing sweep transfer token %q", token)
		}
	}
	if strings.Contains(body, "chain.SweepERC20To(") {
		t.Fatal("executeAutoSweepDepositWithJob must not use full-token sweep; ledger amount stays txModel.Amount")
	}
}

func TestExecuteAutoSweepDepositWithJobRequiresDurableJobAndLedgerRepo(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "executeAutoSweepDepositWithJob")
	for _, token := range []string{
		"job == nil",
		"job.ID == uuid.Nil",
		"coreApplication.CORE.Router.LedgerRepo == nil",
		"repositories.ErrLedgerReservationRequired",
		"LedgerRepo.CreateSweepHold(ctx, *job, *txModel)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("executeAutoSweepDepositWithJob missing durable job/ledger guard token %q", token)
		}
	}
}

func TestSweepSchedulingUsesDurableJobFromFinalizedPaths(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	immediateBody := extractMainSweepFunctionBody(t, source, "handleDepositWebhook")
	if !strings.Contains(immediateBody, "enqueueSweepJob(ctx, txModel)") {
		t.Fatal("immediate finalized deposit path must enqueue a durable sweep job")
	}

	finalityBody := extractMainSweepFunctionBody(t, source, "finalizePendingTransactions")
	for _, token := range []string{
		"handlePaymentDeposit(ctx, notifier, finalized)",
		"enqueueSweepJob(ctx, finalized)",
	} {
		if !strings.Contains(finalityBody, token) {
			t.Fatalf("pending-finality path missing durable sweep scheduling token %q", token)
		}
	}
}

func TestEnqueueSweepJobSkipsReserveWalletsAndEmitsRequestedOnlyOnCreate(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "enqueueSweepJob")
	for _, token := range []string{
		"userWallet.HDAddressId == 0",
		"SweepJobRepo.EnqueueForTransaction(ctx, *txModel)",
		"if created && job != nil",
		"constants.WebhookEventSweepRequestedV1",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("enqueueSweepJob missing idempotent scheduling token %q", token)
		}
	}
	skipIndex := strings.Index(body, "userWallet.HDAddressId == 0")
	enqueueIndex := strings.Index(body, "SweepJobRepo.EnqueueForTransaction(ctx, *txModel)")
	eventIndex := strings.Index(body, "constants.WebhookEventSweepRequestedV1")
	createdIndex := strings.Index(body, "if created && job != nil")
	if skipIndex == -1 || enqueueIndex == -1 || skipIndex > enqueueIndex {
		t.Fatal("enqueueSweepJob must skip reserve/non-user wallets before creating sweep jobs")
	}
	if createdIndex == -1 || eventIndex == -1 || createdIndex > eventIndex {
		t.Fatal("sweep.requested.v1 must only be emitted when EnqueueForTransaction creates a new job")
	}
}

func TestShouldAttemptSweepPrefundUsesParentJobRetryWindow(t *testing.T) {
	t.Setenv("SWEEP_PREFUND_RETRY_AFTER", "10m")
	if !shouldAttemptSweepPrefund(nil) {
		t.Fatal("nil job should allow prefund attempt")
	}

	now := time.Now()
	if !shouldAttemptSweepPrefund(&models.SweepJob{}) {
		t.Fatal("job without prefunded_at should allow prefund attempt")
	}
	if shouldAttemptSweepPrefund(&models.SweepJob{PrefundedAt: &now}) {
		t.Fatal("recent prefund should suppress duplicate prefund attempt")
	}
	if shouldAttemptSweepPrefund(&models.SweepJob{PrefundLastAttemptAt: &now}) {
		t.Fatal("recent failed prefund attempt should suppress duplicate prefund attempt")
	}
	old := now.Add(-11 * time.Minute)
	if !shouldAttemptSweepPrefund(&models.SweepJob{PrefundedAt: &old, PrefundLastAttemptAt: &old}) {
		t.Fatal("prefund older than retry window should allow another attempt")
	}
}

func TestSweepJobLockTimeoutCannotExpireBeforeExecutionTimeout(t *testing.T) {
	t.Setenv("SWEEP_JOB_LOCK_TIMEOUT", "10s")
	if got := sweepJobLockTimeout(); got <= sweepJobExecutionTimeout {
		t.Fatalf("lock timeout = %s, must exceed execution timeout %s", got, sweepJobExecutionTimeout)
	}
	t.Setenv("SWEEP_JOB_LOCK_TIMEOUT", "91s")
	if got := sweepJobLockTimeout(); got != sweepJobExecutionTimeout+30*time.Second {
		t.Fatalf("lock timeout = %s, want execution timeout plus safety buffer", got)
	}
}

func TestFinalizeProcessingTransfersRequiresFinalityEvidence(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "finalizeProcessingTransfers")
	for _, token := range []string{
		"outboundTerminalTransaction(ctx, router.TransactionRepo",
		"continue",
		"models.WithdrawalStatusFinalized",
		"constants.WebhookEventPayoutFinalizedV1",
		"constants.WebhookEventPayoutFailedV1",
		"constants.WebhookEventRefundSucceededV1",
		"constants.WebhookEventRefundFailedV1",
		"openOutboundLifecycleReconciliation",
		"terminalTx.Status == models.TransactionStatusFailed",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("finalizeProcessingTransfers missing finality evidence token %q", token)
		}
	}
	finalityIndex := strings.Index(body, "outboundTerminalTransaction(ctx, router.TransactionRepo")
	withdrawalFinalizeIndex := strings.Index(body, "router.WithdrawalRepo.FinalizeProcessingWithLedger")
	refundFinalizeIndex := strings.Index(body, "router.RefundRepo.MarkSucceededWithLedger")
	if finalityIndex == -1 || withdrawalFinalizeIndex == -1 || refundFinalizeIndex == -1 {
		t.Fatal("finalizer is missing finality checks or terminal update calls")
	}
	if finalityIndex > withdrawalFinalizeIndex {
		t.Fatal("withdrawal finalization must be gated by finality evidence before ledger debit")
	}
}

func TestProcessSweepJobsRequiresTxHashAndMarksSuccessBeforeRelease(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "processSweepJobs")
	for _, token := range []string{
		"sweep broadcast missing transaction hash",
		"router.SweepJobRepo.MarkFailed(ctx, job.ID, err)",
		"openSweepLedgerReconciliation(ctx, job, txModel, \"sweep_mark_succeeded_failed\", err)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("processSweepJobs missing guard token %q", token)
		}
	}
	successIndex := strings.Index(body, "router.SweepJobRepo.MarkSucceeded(ctx, job.ID, txHash)")
	releaseIndex := strings.Index(body, "router.LedgerRepo.PostSweepRelease(ctx, job, *txModel, txHash)")
	if successIndex == -1 || releaseIndex == -1 {
		t.Fatalf("processSweepJobs missing success/release calls")
	}
	if successIndex > releaseIndex {
		t.Fatal("processSweepJobs must mark the sweep job succeeded before posting ledger release")
	}
}

func TestProcessSweepJobsSchedulesMissingFinalizedSweepsBeforeClaim(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "processSweepJobs")
	scheduleIndex := strings.Index(body, "scheduleMissingFinalizedSweepJobs(ctx, router)")
	claimIndex := strings.Index(body, "router.SweepJobRepo.ClaimDue(ctx, 25, sweepJobLockTimeout())")
	if scheduleIndex == -1 || claimIndex == -1 {
		t.Fatal("processSweepJobs must schedule missing finalized sweeps before claim")
	}
	if scheduleIndex > claimIndex {
		t.Fatal("missing finalized sweeps must be scheduled before ClaimDue")
	}
}

func TestSweepDeadLetterUnclassifiedFailureOpensReconciliation(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "releaseSweepHoldOnPreBroadcastDeadLetter")
	if !strings.Contains(body, "\"sweep_dead_letter_broadcast_uncertain\"") {
		t.Fatal("unclassified sweep dead-letter failures must open reconciliation")
	}
}

func TestProcessSweepJobsDeadLettersBroadcastUncertainBeforeRetry(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "processSweepJobs")
	for _, token := range []string{
		"sweepFailureBroadcastUncertain(err)",
		"markSweepBroadcastUncertainAndReconcile(ctx, router, job, txModel, \"sweep_broadcast_uncertain\", err)",
		"markSweepBroadcastUncertainAndReconcile(ctx, router, job, txModel, \"sweep_broadcast_missing_tx_hash\", err)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("processSweepJobs missing broadcast-uncertain guard token %q", token)
		}
	}

	executeStart := strings.Index(body, "result, err := executeSweepJob")
	if executeStart == -1 {
		t.Fatal("processSweepJobs missing executeSweepJob call")
	}
	executeErrBranch := body[executeStart:]
	uncertainIndex := strings.Index(executeErrBranch, "sweepFailureBroadcastUncertain(err)")
	failedIndex := strings.Index(executeErrBranch, "router.SweepJobRepo.MarkFailed(ctx, job.ID, err)")
	if uncertainIndex == -1 || failedIndex == -1 || uncertainIndex > failedIndex {
		t.Fatal("processSweepJobs must route broadcast-uncertain failures before retryable MarkFailed")
	}
}

func TestProcessSweepJobsRoutesRecoveredProcessingToReconciliationBeforeExecution(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "processSweepJobs")
	staleIndex := strings.Index(body, "job.Status == models.SweepJobStatusProcessing")
	if staleIndex == -1 {
		t.Fatal("processSweepJobs must detect reclaimed stale processing jobs")
	}
	reconcileIndex := strings.Index(body, "\"sweep_stale_processing_recovered\"")
	executeIndex := strings.Index(body, "result, err := executeSweepJob")
	if reconcileIndex == -1 || executeIndex == -1 {
		t.Fatal("processSweepJobs missing stale reconciliation or execute call")
	}
	if staleIndex > executeIndex || reconcileIndex > executeIndex {
		t.Fatal("stale processing jobs must reconcile before executeSweepJob")
	}
}

func TestProcessSweepJobsHandlesBroadcastUncertainMarkFailuresAndReloadFallback(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	helper := extractMainSweepFunctionBody(t, string(sourceBytes), "markSweepBroadcastUncertainAndReconcile")
	for _, token := range []string{
		"MarkBroadcastUncertain(ctx, job.ID, err)",
		"if markErr != nil",
		"\"sweep_broadcast_uncertain_mark_failed\"",
		"if findErr != nil",
		"fallback.Status = models.SweepJobStatusDeadLetter",
	} {
		if !strings.Contains(helper, token) {
			t.Fatalf("broadcast uncertainty helper missing %q", token)
		}
	}
}

func TestSweepFailureBroadcastUncertainClassifiesPostBroadcastErrors(t *testing.T) {
	for _, errText := range []string{
		"ethereum tx broadcast failed: context deadline exceeded",
		"solana tx broadcast failed: rpc unavailable",
		"replacement transaction underpriced",
		"nonce too low",
		"transaction already known in mempool",
	} {
		if !sweepFailureBroadcastUncertain(errors.New(errText)) {
			t.Fatalf("expected broadcast-uncertain classification for %q", errText)
		}
	}
	for _, errText := range []string{
		"ledger reservation is required before outbound transfer",
		"wallet not found",
		"resource reservation failed: chain resource already reserved",
	} {
		if sweepFailureBroadcastUncertain(errors.New(errText)) {
			t.Fatalf("expected pre-broadcast classification for %q", errText)
		}
	}
}

func extractMainSweepFunctionBody(t *testing.T, source, functionName string) string {
	t.Helper()
	start := strings.Index(source, "func ")
	for start != -1 {
		remaining := source[start:]
		open := strings.Index(remaining, "{")
		if open == -1 {
			t.Fatalf("function %s has no opening brace", functionName)
		}
		signature := remaining[:open]
		if strings.Contains(signature, " "+functionName+"(") {
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
