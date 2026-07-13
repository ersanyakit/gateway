package signer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPAdapterSignTransactionUsesFirstPartyProtocol(t *testing.T) {
	const secret = "test-hmac-secret"
	const bearer = "test-bearer"

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != defaultHTTPSignTransactionPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, defaultHTTPSignTransactionPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+bearer {
			t.Fatalf("authorization = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		timestamp := r.Header.Get("X-Gateway-Signer-Timestamp")
		signature := strings.TrimPrefix(r.Header.Get("X-Gateway-Signer-Signature"), "sha256=")
		expected := httpAdapterSignature(secret, timestamp, r.Method, r.URL.RequestURI(), body)
		if timestamp == "" || signature != expected {
			t.Fatalf("invalid signer HMAC timestamp=%q signature=%q expected=%q", timestamp, signature, expected)
		}

		var req SignTransactionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Chain != "ethereum" || req.Intent != "transfer.native" || string(req.UnsignedPayload) != "unsigned" {
			t.Fatalf("unexpected request: %#v", req)
		}

		return jsonResponse(http.StatusOK, `{"signed_payload":"0xdeadbeef","tx_hash":"0xtx","key_reference":"key-ref","audit_id":"audit-1"}`), nil
	})}

	adapter, err := NewHTTPAdapter(HTTPAdapterConfig{
		BaseURL:     "https://signer.internal.test",
		Mode:        "first-party",
		Provider:    "own-custody",
		BearerToken: bearer,
		HMACSecret:  secret,
		Client:      client,
	})
	if err != nil {
		t.Fatalf("NewHTTPAdapter: %v", err)
	}
	response, err := adapter.SignTransaction(context.Background(), SignTransactionRequest{
		Chain:           "ethereum",
		ChainID:         1,
		KeyReference:    "key-ref",
		Intent:          "transfer.native",
		UnsignedPayload: []byte("unsigned"),
	})
	if err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}
	if string(response.SignedPayload) != "0xdeadbeef" || response.TxHash != "0xtx" || response.AuditID != "audit-1" {
		t.Fatalf("response = %#v", response)
	}
}

func TestConfiguredHTTPAdapterRequiresHMACInProduction(t *testing.T) {
	resetCustodyAdapterForTest(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "first-party")
	t.Setenv("CUSTODY_SIGNER_URL", "https://signer.internal.example")

	if _, _, err := ProductionReadiness(); err == nil {
		t.Fatal("ProductionReadiness err=nil, want missing HMAC secret failure")
	}
}

func TestProductionReadinessAllowsConfiguredFirstPartySigner(t *testing.T) {
	resetCustodyAdapterForTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != defaultHTTPHealthPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, defaultHTTPHealthPath)
		}
		if r.Header.Get("X-Gateway-Signer-Signature") == "" {
			t.Fatal("missing signer HMAC signature")
		}
		return jsonResponse(http.StatusOK, `{"ready":true,"mode":"first-party","provider":"own-custody","details":"ready"}`), nil
	})}
	adapter, err := NewHTTPAdapter(HTTPAdapterConfig{
		BaseURL:    "https://signer.internal.test",
		Mode:       "first-party",
		Provider:   "own-custody",
		HMACSecret: "test-secret",
		Client:     client,
	})
	if err != nil {
		t.Fatalf("NewHTTPAdapter: %v", err)
	}
	restore := RegisterCustodyAdapter(adapter)
	t.Cleanup(restore)

	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "first-party")

	ok, details, err := ProductionReadiness()
	if err != nil || !ok {
		t.Fatalf("ProductionReadiness ok=%v details=%q err=%v, want ready", ok, details, err)
	}
	if !strings.Contains(details, "SIGNER_MODE=first-party") || !strings.Contains(details, "own-custody") {
		t.Fatalf("details = %q", details)
	}
}

func resetCustodyAdapterForTest(t *testing.T) {
	t.Helper()
	restore := RegisterCustodyAdapter(nil)
	t.Cleanup(restore)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
