package handlers

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPartnerAPIOpenAPIContractDocumentsEpic1Fields(t *testing.T) {
	swagger := readSwaggerJSON(t)

	createPayment := swaggerOperation(t, swagger, "/api/v1/payment/create", "post")
	for _, header := range []string{"X-API-Secret", "X-Gateway-Timestamp", "X-Gateway-Signature", "Idempotency-Key"} {
		requireSwaggerHeader(t, createPayment, header)
	}

	errorProps := swaggerDefinitionProperties(t, swagger, "types.V1ErrorResponse")
	requireSwaggerProperties(t, errorProps, "result", "message")
	if _, ok := errorProps["error"]; ok {
		t.Fatalf("V1ErrorResponse must not document legacy error field: %#v", errorProps)
	}

	staticProps := swaggerDefinitionProperties(t, swagger, "types.V1StaticAddressDetail")
	requireSwaggerProperties(t, staticProps, "wallet_id", "user_id", "product_id", "chain", "chain_id", "symbol", "token", "address", "label", "created_at")

	checkoutProps := swaggerDefinitionProperties(t, swagger, "types.PaymentStatusResponse")
	requireSwaggerProperties(t, checkoutProps, "success", "status", "paid", "payment_id", "tx_hash", "success_path", "cancel_path", "payable", "terminal")

	statusTableProps := swaggerDefinitionProperties(t, swagger, "types.V1PaymentStatusTableResponse")
	data, ok := statusTableProps["data"].(map[string]any)
	if !ok {
		t.Fatalf("status-table response data schema = %#v, want object ref", statusTableProps["data"])
	}
	if data["$ref"] != "#/definitions/types.V1PaymentStatusTableData" {
		t.Fatalf("status-table data ref = %#v, want V1PaymentStatusTableData", data["$ref"])
	}
	statusTableDataProps := swaggerDefinitionProperties(t, swagger, "types.V1PaymentStatusTableData")
	requireSwaggerProperties(t, statusTableDataProps, "statuses")
	statusItemProps := swaggerDefinitionProperties(t, swagger, "types.V1StatusTableItem")
	requireSwaggerProperties(t, statusItemProps, "status", "description", "is_final")
}

func TestIntegrationGuideContractEvidenceCoversEpic1PartnerFlow(t *testing.T) {
	body, err := os.ReadFile("../../docs/integration-guide.md")
	if err != nil {
		t.Fatalf("read integration guide: %v", err)
	}
	guide := string(body)

	requiredSnippets := []string{
		"V1 request signing binds method, original path/query target, timestamp, and raw body.",
		"message = METHOD + \"\\n\" + path_and_query + \"\\n\" + timestamp + \"\\n\" + body",
		"\"result\": \"ok\"",
		"\"data\": {",
		"\"track_id\": \"uuid\"",
		"\"expected_amount_raw\": \"25000000\"",
		"\"product_id\": \"static:1:USDT:token:0xdac17f958d2ee523a2206206994597c13d831ec7:product:checkout\"",
		"`underpaid`",
		"\"payable\": false",
		"\"terminal\": true",
		"\"result\": \"error\"",
		"\"message\": \"idempotency key was already used with a different request\"",
		"## Contract Evidence - Epic 1",
		"Known production limitations",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(guide, snippet) {
			t.Fatalf("integration guide missing required contract snippet %q", snippet)
		}
	}
}

func TestV1PaymentStatusTableUsesDocumentedContractShape(t *testing.T) {
	statuses := v1PaymentStatusTable()
	expectedFinal := map[string]bool{
		"pending":          false,
		"awaiting_payment": false,
		"paid":             true,
		"expired":          true,
		"canceled":         true,
		"failed":           true,
		"underpaid":        true,
		"overpaid":         true,
		"partial_paid":     true,
	}
	if len(statuses) != len(expectedFinal) {
		t.Fatalf("statuses = %d, want %d", len(statuses), len(expectedFinal))
	}
	for _, item := range statuses {
		wantFinal, ok := expectedFinal[item.Status]
		if !ok {
			t.Fatalf("unexpected status item: %#v", item)
		}
		if item.Description == "" {
			t.Fatalf("status %q missing description: %#v", item.Status, item)
		}
		if item.IsFinal != wantFinal {
			t.Fatalf("status %q is_final = %v, want %v", item.Status, item.IsFinal, wantFinal)
		}
	}
}

func readSwaggerJSON(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile("../../docs/swagger.json")
	if err != nil {
		t.Fatalf("read swagger: %v", err)
	}
	var swagger map[string]any
	if err := json.Unmarshal(body, &swagger); err != nil {
		t.Fatalf("decode swagger: %v", err)
	}
	return swagger
}

func swaggerOperation(t *testing.T, swagger map[string]any, path string, method string) map[string]any {
	t.Helper()
	paths, ok := swagger["paths"].(map[string]any)
	if !ok {
		t.Fatal("swagger paths missing")
	}
	pathItem, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("swagger path %s missing", path)
	}
	op, ok := pathItem[method].(map[string]any)
	if !ok {
		t.Fatalf("swagger operation %s %s missing", method, path)
	}
	return op
}

func requireSwaggerHeader(t *testing.T, operation map[string]any, name string) {
	t.Helper()
	parameters, ok := operation["parameters"].([]any)
	if !ok {
		t.Fatalf("operation parameters missing: %#v", operation)
	}
	for _, raw := range parameters {
		param, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if param["in"] == "header" && param["name"] == name {
			return
		}
	}
	t.Fatalf("operation missing header parameter %q", name)
}

func swaggerDefinitionProperties(t *testing.T, swagger map[string]any, name string) map[string]any {
	t.Helper()
	definitions, ok := swagger["definitions"].(map[string]any)
	if !ok {
		t.Fatal("swagger definitions missing")
	}
	definition, ok := definitions[name].(map[string]any)
	if !ok {
		t.Fatalf("swagger definition %q missing", name)
	}
	properties, ok := definition["properties"].(map[string]any)
	if !ok {
		t.Fatalf("swagger definition %q properties missing", name)
	}
	return properties
}

func requireSwaggerProperties(t *testing.T, properties map[string]any, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, ok := properties[name]; !ok {
			t.Fatalf("swagger properties missing %q in %#v", name, properties)
		}
	}
}
