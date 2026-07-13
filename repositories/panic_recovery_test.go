package repositories

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"core/types"

	"github.com/google/uuid"
)

func TestRepositoryCreateMissingDependenciesReturnLoggedErrors(t *testing.T) {
	var logs bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousLogOutput) })

	merchantID := uuid.NewString()
	domainID := uuid.NewString()
	name := "Merchant"
	email := "merchant@example.com"
	password := "secret-password"
	domainURL := "merchant.example.com"
	webhookURL := "https://merchant.example.com/webhook"
	webhookSecret := "webhook-secret"
	productID := "product"
	userID := "user"

	tests := []struct {
		name       string
		operation  string
		wantErrMsg string
		create     func() error
	}{
		{
			name:       "merchant",
			operation:  "merchant create",
			wantErrMsg: "merchant create: database is not configured",
			create: func() error {
				_, err := NewMerchantRepo(nil, nil).Create(types.MerchantParams{
					Context:        context.Background(),
					Name:           &name,
					Email:          &email,
					EmailRepeat:    &email,
					Password:       &password,
					PasswordRepeat: &password,
				})
				return err
			},
		},
		{
			name:       "domain",
			operation:  "domain create",
			wantErrMsg: "domain create: database is not configured",
			create: func() error {
				_, err := NewDomainRepo(NewMerchantRepo(nil, nil)).Create(types.DomainParams{
					Context:       context.Background(),
					MerchantID:    &merchantID,
					DomainURL:     &domainURL,
					WebhookURL:    &webhookURL,
					WebhookSecret: &webhookSecret,
				})
				return err
			},
		},
		{
			name:       "wallet",
			operation:  "wallet create",
			wantErrMsg: "wallet create: database is not configured",
			create: func() error {
				_, err := NewWalletRepo(nil).Create(types.WalletParams{
					Context:    context.Background(),
					MerchantId: &merchantID,
					DomainId:   &domainID,
					ProductId:  &productID,
					UserId:     &userID,
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logs.Reset()
			err := test.create()
			if err == nil {
				t.Fatal("missing repository dependency returned nil error")
			}
			if !strings.Contains(err.Error(), test.wantErrMsg) {
				t.Fatalf("error = %q, want %q", err, test.wantErrMsg)
			}
			if !strings.Contains(logs.String(), "repository operation="+test.operation+" error=") {
				t.Fatalf("log = %q, want operation error log", logs.String())
			}
		})
	}
}

func TestRecoverRepositoryTransactionPanicPreservesError(t *testing.T) {
	sentinel := errors.New("sentinel panic")
	err := recoverRepositoryTransactionPanic("test operation", nil, sentinel)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want wrapped sentinel", err)
	}
}
