package middleware

import "testing"

func TestHashRateLimitSecretDoesNotExposeRawSecret(t *testing.T) {
	raw := "Bearer gw_secret_test"
	hashed := hashRateLimitSecret(raw)

	if hashed == raw {
		t.Fatal("rate-limit key should not contain the raw secret")
	}
	if len(hashed) != 64 {
		t.Fatalf("hash length = %d, want 64", len(hashed))
	}
	if hashed != hashRateLimitSecret(raw) {
		t.Fatal("hash should be deterministic")
	}
}
