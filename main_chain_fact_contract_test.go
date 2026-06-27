package main

import (
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
