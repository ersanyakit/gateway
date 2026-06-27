package webhook

import (
	"os"
	"strings"
	"testing"
)

func TestSourceMoneyFlowsDoNotInlineDeliverBoundaryWebhooks(t *testing.T) {
	tests := []struct {
		file      string
		functions []string
		forbidden []string
	}{
		{
			file:      "../../main.go",
			functions: []string{"handleDepositWebhook", "handlePaymentDeposit", "retryPendingWebhooks"},
			forbidden: []string{"notifier.Deliver(", "notifier.DeliverPayment("},
		},
		{
			file:      "../../api/handlers/payment.go",
			functions: []string{"deliverPaymentWebhook"},
			forbidden: []string{"Notifier.DeliverPayment("},
		},
		{
			file:      "../../api/handlers/dealer.go",
			functions: []string{"deliverAdminTransactionWebhook", "deliverAdminPaymentWebhookIfMatched"},
			forbidden: []string{"Notifier.Deliver(", "Notifier.DeliverPayment("},
		},
	}

	for _, tt := range tests {
		sourceBytes, err := os.ReadFile(tt.file)
		if err != nil {
			t.Fatalf("read %s: %v", tt.file, err)
		}
		source := string(sourceBytes)
		for _, functionName := range tt.functions {
			body := extractGoFunctionBody(t, source, functionName)
			for _, forbidden := range tt.forbidden {
				if strings.Contains(body, forbidden) {
					t.Fatalf("%s in %s still contains inline webhook delivery %q", functionName, tt.file, forbidden)
				}
			}
		}
	}
}

func extractGoFunctionBody(t *testing.T, source, functionName string) string {
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
