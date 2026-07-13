package main

import (
	"os"
	"strings"
	"testing"
)

func TestMainPeriodicWorkersAreOwnedBySupervisor(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	for _, token := range []string{
		"buildGatewayWorkerSupervisor",
		"WorkerSupervisor.Start(mainCtx)",
		"WorkerSupervisor.Stop(workerCtx)",
		"startWebhookRetryWorker(ctx, notifier)",
		"supervisedWorker(\"deposit-facts\", startDepositFactWorker)",
		"supervisedWorker(\"sweep-jobs\", startSweepJobWorker)",
		"supervisedWorker(\"transfer-finalization\", startTransferFinalizationWorker)",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("main.go missing supervisor token %q", token)
		}
	}
	for _, token := range []string{
		"go startWebhookRetryWorker",
		"go startSessionExpiryWorker",
		"go startTransactionFinalityWorker",
		"go startDepositFactWorker",
		"go startOutboundTransactionWorker",
		"go startSweepJobWorker",
		"go startReconciliationWorker",
		"go startTransferFinalizationWorker",
		"go startProviderHealthWorker",
	} {
		if strings.Contains(source, token) {
			t.Fatalf("main.go still starts periodic worker directly with %q", token)
		}
	}
}
