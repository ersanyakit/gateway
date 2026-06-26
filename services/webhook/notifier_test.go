package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"core/constants"
	"core/helpers"
	"core/models"

	"github.com/google/uuid"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNotifierDeliverSignsAndPostsTransaction(t *testing.T) {
	t.Setenv("MASTER_KEY", "webhook-test-master-key")
	t.Setenv("APP_ENV", "test")
	encryptedSecret, err := helpers.EncryptSecret("plain-webhook-secret")
	if err != nil {
		t.Fatal(err)
	}

	var received Payload
	var signature string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		signature = r.Header.Get("X-Gateway-Signature")
		if !strings.HasPrefix(signature, "sha256=") {
			t.Fatalf("signature header = %q", signature)
		}
		if r.Header.Get("X-Gateway-Event") != "native_transfer" {
			t.Fatalf("event header = %q", r.Header.Get("X-Gateway-Event"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       io.NopCloser(bytes.NewBuffer(nil)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}

	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	tx := models.Transaction{
		ID:          uuid.New(),
		UniqueHash:  "1-0xabc-tx:1",
		EventType:   "native_transfer",
		MerchantID:  &merchantID,
		DomainID:    &domainID,
		WalletID:    &walletID,
		ProductID:   "product",
		UserID:      "user",
		ChainID:     constants.Ethereum,
		Hash:        "0xabc",
		BlockNumber: "123",
		BlockHash:   "0xblock",
		Symbol:      "ETH",
		Decimals:    18,
		FromAddress: "0xfrom",
		ToAddress:   "0xto",
		Amount:      "100",
		Status:      models.TransactionStatusConfirmed,
		CreatedAt:   time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC),
	}

	notifier := &Notifier{client: client}
	err = notifier.Deliver(context.Background(), models.Domain{
		ID:            domainID,
		WebhookURL:    "http://127.0.0.1/webhook",
		WebhookSecret: encryptedSecret,
	}, tx)
	if err != nil {
		t.Fatal(err)
	}
	if received.EventID != tx.UniqueHash {
		t.Fatalf("event id = %q", received.EventID)
	}
	if received.MerchantID != merchantID.String() || received.DomainID != domainID.String() || received.WalletID != walletID.String() {
		t.Fatalf("identity fields not populated: %#v", received)
	}
}

func TestNotifierDeliverRejectsMissingWebhookConfig(t *testing.T) {
	notifier := NewNotifier()
	domain := models.Domain{ID: uuid.New()}
	if err := notifier.Deliver(context.Background(), domain, models.Transaction{}); err == nil {
		t.Fatal("empty webhook url should fail")
	} else if !IsPermanent(err) {
		t.Fatalf("empty webhook url error should be permanent: %v", err)
	}
	domain.WebhookURL = "https://example.com/webhook"
	if err := notifier.Deliver(context.Background(), domain, models.Transaction{}); err == nil {
		t.Fatal("empty webhook secret should fail")
	} else if !IsPermanent(err) {
		t.Fatalf("empty webhook secret error should be permanent: %v", err)
	}
}
