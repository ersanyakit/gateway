package types

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestWalletParamsValidateTrimsAndAcceptsUUIDs(t *testing.T) {
	merchantID := uuid.NewString()
	domainID := uuid.NewString()
	productID := " product "
	userID := " user "

	params := WalletParams{
		Context:    context.Background(),
		MerchantId: &merchantID,
		DomainId:   &domainID,
		ProductId:  &productID,
		UserId:     &userID,
	}
	if err := params.Validate(); err != nil {
		t.Fatal(err)
	}
	if *params.ProductId != "product" {
		t.Fatalf("product id = %q, want trimmed", *params.ProductId)
	}
	if *params.UserId != "user" {
		t.Fatalf("user id = %q, want trimmed", *params.UserId)
	}
}

func TestWalletParamsValidateRejectsMissingFields(t *testing.T) {
	params := WalletParams{}
	if err := params.Validate(); err == nil {
		t.Fatal("missing wallet params should fail")
	}
}

func TestMerchantParamsValidateCollectsErrors(t *testing.T) {
	name := "ab"
	email := "bad-email"
	emailRepeat := "other@example.com"
	password := "123"
	passwordRepeat := "456"
	params := MerchantParams{
		Name:           &name,
		Email:          &email,
		EmailRepeat:    &emailRepeat,
		Password:       &password,
		PasswordRepeat: &passwordRepeat,
	}

	err := params.Validate()
	if err == nil {
		t.Fatal("invalid merchant params should fail")
	}
	validationErrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("error type = %T, want ValidationErrors", err)
	}
	if len(validationErrs) < 5 {
		t.Fatalf("validation errors = %d, want at least 5", len(validationErrs))
	}
}
