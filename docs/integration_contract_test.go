package docs

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestIntegrationGuideMatchesEpic1PartnerContract(t *testing.T) {
	guideBytes, err := os.ReadFile("integration-guide.md")
	if err != nil {
		t.Fatalf("read integration guide: %v", err)
	}
	guide := string(guideBytes)

	requireContains(t, guide, "method + path/query + timestamp + raw body")
	requireContains(t, guide, "POST\\n/api/v1/payment/create")
	requireNotContains(t, guide, "message = timestamp + body")

	paymentSection := markdownSection(guide, "## Create Hosted Payment", "## Create Static Deposit Address")
	requireContains(t, paymentSection, `"result": "ok"`)
	requireContains(t, paymentSection, `"data": {`)
	requireContains(t, paymentSection, `"track_id"`)
	requireContains(t, paymentSection, `"expected_amount_raw"`)
	requireContains(t, paymentSection, `"deposit_address"`)
	requireContains(t, paymentSection, "Idempotency-Key")
	requireContains(t, paymentSection, "409")
	requireNotContains(t, paymentSection, `"success": true`)

	staticSection := markdownSection(guide, "## Create Static Deposit Address", "## Create Wallet Provider Wallet")
	for _, token := range []string{`"product_id"`, `"chain_id"`, `"token"`, `"created_at"`, "domain/product/user/asset"} {
		requireContains(t, staticSection, token)
	}

	statusSection := markdownSection(guide, "## Query Payment", "## Create Payout Request")
	for _, token := range []string{"`active`", "`pending`", "`confirming`", "`paid`", "`expired`", "`canceled`", "`failed`", "`underpaid`", "`payable`", "`terminal`"} {
		requireContains(t, statusSection, token)
	}

	errorSection := markdownSection(guide, "## Error Handling", "## Security Checklist")
	requireContains(t, errorSection, `"result": "error"`)
	requireContains(t, errorSection, `"message"`)
	requireNotContains(t, errorSection, `"error":`)

	for _, forbidden := range []string{"gw_secret_", "api_secret_value", "webhook_secret_value", "private_key", "mnemonic", "panic:", "goroutine "} {
		requireNotContains(t, guide, forbidden)
	}
}

func TestEpic1IntegrationEvidenceDocumentsCoveredContract(t *testing.T) {
	evidenceBytes, err := os.ReadFile("epic-1-integration-evidence.md")
	if err != nil {
		t.Fatalf("read Epic 1 integration evidence: %v", err)
	}
	evidence := string(evidenceBytes)
	for _, token := range []string{
		"POST /api/v1/payment/create",
		"POST /api/v1/payment/static-address",
		"GET /checkout/{token}/status.json",
		"X-API-Key",
		"Authorization: Bearer",
		"Idempotency-Key",
		"domain/product/user/asset",
		"terminal",
		"payable",
		"Known Production Limitations",
		"go test -count=1 ./...",
		"go vet ./...",
	} {
		requireContains(t, evidence, token)
	}
}

func TestMoneyEventCatalogDocumentsVersionedEvents(t *testing.T) {
	catalogBytes, err := os.ReadFile("money-event-catalog.md")
	if err != nil {
		t.Fatalf("read money event catalog: %v", err)
	}
	catalog := string(catalogBytes)
	for _, token := range []string{
		"deposit.detected.v1",
		"deposit.finalized.v1",
		"payment.succeeded.v1",
		"payment.failed.v1",
		"payment.expired.v1",
		"withdrawal.requested.v1",
		"withdrawal.broadcast.v1",
		"withdrawal.finalized.v1",
		"withdrawal.failed.v1",
		"refund.succeeded.v1",
		"sweep.succeeded.v1",
		"transaction.reorged.v1",
		"native_transfer",
		"payment_succeeded",
		"payout.requested.v1",
		"event_id",
		"event_type",
		"event_version",
		"occurred_at",
		"idempotency_key",
		"correlation_id",
		"non-destructive",
		"timestamp + raw_body",
	} {
		requireContains(t, catalog, token)
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "raw_signature", "stack_trace"} {
		requireNotContains(t, catalog, forbidden)
	}

	guideBytes, err := os.ReadFile("integration-guide.md")
	if err != nil {
		t.Fatalf("read integration guide: %v", err)
	}
	requireContains(t, string(guideBytes), "docs/money-event-catalog.md")
}

func TestSwaggerContainsEpic1PartnerContract(t *testing.T) {
	swaggerBytes, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatalf("read swagger: %v", err)
	}
	var swagger map[string]any
	if err := json.Unmarshal(swaggerBytes, &swagger); err != nil {
		t.Fatalf("parse swagger: %v", err)
	}

	paths := asMap(t, swagger["paths"], "paths")
	defs := asMap(t, swagger["definitions"], "definitions")
	securityDefs := asMap(t, swagger["securityDefinitions"], "securityDefinitions")

	if _, ok := securityDefs["ApiKeyAuth"]; !ok {
		t.Fatalf("Swagger securityDefinitions missing ApiKeyAuth")
	}
	if _, ok := securityDefs["BearerAuth"]; !ok {
		t.Fatalf("Swagger securityDefinitions missing BearerAuth")
	}

	paymentCreate := operation(t, paths, "/api/v1/payment/create", "post")
	requireOperationParam(t, paymentCreate, "X-Gateway-Signature")
	requireOperationResponse(t, paymentCreate, "201", "#/definitions/types.V1PaymentCreateResponse")
	requireOperationResponse(t, paymentCreate, "409", "#/definitions/types.V1ErrorResponse")

	staticCreate := operation(t, paths, "/api/v1/payment/static-address", "post")
	requireOperationParam(t, staticCreate, "X-Gateway-Signature")
	requireOperationResponse(t, staticCreate, "200", "#/definitions/types.V1StaticAddressResponse")

	checkoutStatus := operation(t, paths, "/checkout/{token}/status.json", "get")
	requireOperationResponse(t, checkoutStatus, "200", "#/definitions/types.PaymentStatusResponse")

	paymentData := definitionProperties(t, defs, "types.V1PaymentCreatedData")
	for _, field := range []string{"payment_id", "track_id", "session_token", "checkout_url", "status", "expires_at", "order_id", "amount", "currency", "chain_id", "symbol", "token", "decimals", "expected_amount_raw", "deposit_address"} {
		if _, ok := paymentData[field]; !ok {
			t.Fatalf("types.V1PaymentCreatedData missing property %q", field)
		}
	}

	staticDetail := definitionProperties(t, defs, "types.V1StaticAddressDetail")
	for _, field := range []string{"wallet_id", "user_id", "product_id", "chain", "chain_id", "symbol", "token", "address", "label", "created_at"} {
		if _, ok := staticDetail[field]; !ok {
			t.Fatalf("types.V1StaticAddressDetail missing property %q", field)
		}
	}

	checkoutPayload := definitionProperties(t, defs, "types.PaymentStatusResponse")
	for _, field := range []string{"success", "status", "paid", "payment_id", "tx_hash", "success_path", "cancel_path", "payable", "terminal"} {
		if _, ok := checkoutPayload[field]; !ok {
			t.Fatalf("types.PaymentStatusResponse missing property %q", field)
		}
	}
}

func markdownSection(doc, start, end string) string {
	startIndex := strings.Index(doc, start)
	if startIndex < 0 {
		return ""
	}
	section := doc[startIndex:]
	if end == "" {
		return section
	}
	endIndex := strings.Index(section, end)
	if endIndex < 0 {
		return section
	}
	return section[:endIndex]
}

func requireContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected content to contain %q", needle)
	}
}

func requireNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected content not to contain %q", needle)
	}
}

func asMap(t *testing.T, value any, name string) map[string]any {
	t.Helper()
	m, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object", name)
	}
	return m
}

func operation(t *testing.T, paths map[string]any, path, method string) map[string]any {
	t.Helper()
	pathObj := asMap(t, paths[path], path)
	return asMap(t, pathObj[method], path+" "+method)
}

func requireOperationParam(t *testing.T, operation map[string]any, name string) {
	t.Helper()
	params, ok := operation["parameters"].([]any)
	if !ok {
		t.Fatalf("operation has no parameters")
	}
	for _, param := range params {
		paramObj := asMap(t, param, "parameter")
		if paramObj["name"] == name {
			return
		}
	}
	t.Fatalf("operation missing parameter %q", name)
}

func requireOperationResponse(t *testing.T, operation map[string]any, status, ref string) {
	t.Helper()
	responses := asMap(t, operation["responses"], "responses")
	response := asMap(t, responses[status], "response "+status)
	schema := asMap(t, response["schema"], "response "+status+" schema")
	if schema["$ref"] != ref {
		t.Fatalf("response %s ref = %v, want %s", status, schema["$ref"], ref)
	}
}

func definitionProperties(t *testing.T, definitions map[string]any, name string) map[string]any {
	t.Helper()
	definition := asMap(t, definitions[name], name)
	return asMap(t, definition["properties"], name+" properties")
}
