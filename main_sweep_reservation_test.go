package main

import (
	"os"
	"strings"
	"testing"
)

func TestExecuteAutoSweepDepositWithJobUsesReservedAmountTransfers(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "executeAutoSweepDepositWithJob")
	for _, token := range []string{
		"chain.WithdrawToken(ctx, *userDetails, *txModel.Token, txModel.Amount, reserveAddr)",
		"chain.Withdraw(ctx, *userDetails, txModel.Amount, reserveAddr)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("executeAutoSweepDepositWithJob missing reserved amount transfer %q", token)
		}
	}
	for _, token := range []string{
		"chain.SweepERC20To(",
		"chain.SweepTo(",
	} {
		if strings.Contains(body, token) {
			t.Fatalf("executeAutoSweepDepositWithJob must not call full-wallet sweep API %q", token)
		}
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
