package helpers

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGenerateAPIKeyAndExtractKeyID(t *testing.T) {
	keyID, apiKey, err := GenerateAPIKey("test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(apiKey, TestPrefix+"_") {
		t.Fatalf("test api key prefix = %q", apiKey)
	}
	extracted, err := ExtractKeyID(apiKey)
	if err != nil {
		t.Fatal(err)
	}
	if extracted != keyID {
		t.Fatalf("ExtractKeyID = %q, want %q", extracted, keyID)
	}

	_, liveKey, err := GenerateAPIKey("live")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(liveKey, LivePrefix+"_") {
		t.Fatalf("live api key prefix = %q", liveKey)
	}
}

func TestSecretLengthsStayBcryptSafe(t *testing.T) {
	secret, err := GenerateBcryptSafeSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) > 72 {
		t.Fatalf("bcrypt-safe secret length = %d, want <= 72", len(secret))
	}
	if !strings.HasPrefix(secret, SecretPref+"_") {
		t.Fatalf("secret prefix = %q", secret)
	}
}

func TestEncryptDecryptSecretAndHMAC(t *testing.T) {
	t.Setenv("MASTER_KEY", "unit-test-master-key")

	encrypted, err := EncryptSecret("super-secret")
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := DecryptSecret(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != "super-secret" {
		t.Fatalf("decrypted secret = %q", decrypted)
	}

	h1, err := HMACSecret("super-secret")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HMACSecret("super-secret")
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 || h1 == "" {
		t.Fatalf("HMACSecret should be deterministic and non-empty: %q %q", h1, h2)
	}
}

func TestSignatureAndTimestampValidation(t *testing.T) {
	body := []byte(`{"amount":"1"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	signature := GenerateSignature("secret", ts, body)
	if !VerifySignature("secret", ts, body, signature) {
		t.Fatal("signature should verify")
	}
	if VerifySignature("secret", ts, []byte(`{}`), signature) {
		t.Fatal("signature should reject modified body")
	}
	if err := ValidateTimestamp(ts); err != nil {
		t.Fatalf("current timestamp rejected: %v", err)
	}
	old := strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10)
	if err := ValidateTimestamp(old); err == nil {
		t.Fatal("expired timestamp should fail")
	}
	if err := ValidateTimestamp("not-a-unix-timestamp"); err == nil {
		t.Fatal("malformed timestamp should fail")
	}
	allowedFuture := strconv.FormatInt(time.Now().Add(time.Duration(TimeSkewSec-1)*time.Second).Unix(), 10)
	if err := ValidateTimestamp(allowedFuture); err != nil {
		t.Fatalf("timestamp inside future skew rejected: %v", err)
	}
	tooFarFuture := strconv.FormatInt(time.Now().Add(time.Duration(TimeSkewSec+2)*time.Second).Unix(), 10)
	if err := ValidateTimestamp(tooFarFuture); err == nil {
		t.Fatal("future timestamp outside skew should fail")
	}
}

func TestRequestSignatureBindsMethodPathTimestampAndBody(t *testing.T) {
	body := []byte(`{"amount":"1"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	signature := GenerateRequestSignature("secret", "POST", "/api/v1/payment/create", ts, body)

	if !VerifyRequestSignature("secret", "POST", "/api/v1/payment/create", ts, body, signature) {
		t.Fatal("request signature should verify")
	}
	if VerifyRequestSignature("secret", "GET", "/api/v1/payment/create", ts, body, signature) {
		t.Fatal("request signature should reject modified method")
	}
	if VerifyRequestSignature("secret", "POST", "/api/v1/payment/info", ts, body, signature) {
		t.Fatal("request signature should reject modified path")
	}
	if VerifyRequestSignature("secret", "POST", "/api/v1/payment/create", ts, []byte(`{"amount":"2"}`), signature) {
		t.Fatal("request signature should reject modified body")
	}
}

func TestValidateWebhookURLRejectsPrivateByDefault(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("ALLOW_PRIVATE_WEBHOOK_URLS", "")

	err := ValidateWebhookURL("http://127.0.0.1:3000/webhook")
	if err == nil {
		t.Fatal("private webhook url should fail by default")
	}
	if !strings.Contains(err.Error(), "private or loopback") {
		t.Fatalf("error = %q, want private or loopback rejection", err.Error())
	}
}

func TestValidateWebhookURLAllowsPrivateInDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("ALLOW_PRIVATE_WEBHOOK_URLS", "")

	if err := ValidateWebhookURL("http://127.0.0.1:3000/webhook"); err != nil {
		t.Fatalf("development private webhook url should pass: %v", err)
	}
}

func TestValidateWebhookURLAllowsPrivateWithExplicitFlag(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("ALLOW_PRIVATE_WEBHOOK_URLS", "true")

	if err := ValidateWebhookURL("http://192.168.1.20/webhook"); err != nil {
		t.Fatalf("explicitly allowed private webhook url should pass: %v", err)
	}
}

func TestValidateWebhookURLRejectsPrivateInProductionEvenWithFlag(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("ALLOW_PRIVATE_WEBHOOK_URLS", "true")

	err := ValidateWebhookURL("http://127.0.0.1:3000/webhook")
	if err == nil {
		t.Fatal("production private webhook url should fail")
	}
}
