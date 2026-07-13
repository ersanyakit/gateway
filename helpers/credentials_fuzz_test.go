package helpers

import (
	"encoding/hex"
	"testing"
)

func FuzzRequestSignatureRoundTrip(f *testing.F) {
	f.Add("secret", "POST", "/api/v1/payment/create", "1700000000", []byte(`{"amount_raw":"100"}`))
	f.Add("", "get", "/probe?currency=EUR", "bad-ts", []byte{})
	f.Add("another-secret", "DELETE", "/api/v1/refund/1", "0", []byte("body"))

	f.Fuzz(func(t *testing.T, secret, method, path, timestamp string, body []byte) {
		body = limitFuzzBytes(body, 2048)
		secret = limitFuzzText(secret, 256)
		method = limitFuzzText(method, 32)
		path = limitFuzzText(path, 512)
		timestamp = limitFuzzText(timestamp, 64)

		signature := GenerateRequestSignature(secret, method, path, timestamp, body)
		if len(signature) != 64 {
			t.Fatalf("signature length = %d, want 64", len(signature))
		}
		if _, err := hex.DecodeString(signature); err != nil {
			t.Fatalf("signature is not hex: %v", err)
		}
		if !VerifyRequestSignature(secret, method, path, timestamp, body, signature) {
			t.Fatal("signature did not verify round trip")
		}
		if len(body) > 0 {
			modified := append([]byte(nil), body...)
			modified[0] ^= 0xff
			if VerifyRequestSignature(secret, method, path, timestamp, modified, signature) {
				t.Fatal("signature verified modified body")
			}
		}
	})
}

func limitFuzzText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func limitFuzzBytes(value []byte, max int) []byte {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
