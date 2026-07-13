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

func TestMerchantParamsValidateAcceptsValidEmailPattern(t *testing.T) {
	name := "Merchant"
	email := "payments+test@example.co.uk"
	password := "correct horse battery staple"
	params := MerchantParams{
		Name:           &name,
		Email:          &email,
		EmailRepeat:    &email,
		Password:       &password,
		PasswordRepeat: &password,
	}

	if merchantEmailPatternErr != nil {
		t.Fatalf("merchant email pattern failed to compile: %v", merchantEmailPatternErr)
	}
	if err := params.Validate(); err != nil {
		t.Fatalf("valid merchant params rejected: %v", err)
	}
}

func TestMerchantParamsValidateMissingPrimaryFieldsDoesNotPanic(t *testing.T) {
	name := "Merchant"
	emailRepeat := "merchant@example.com"
	passwordRepeat := "correct horse battery staple"
	params := MerchantParams{
		Name:           &name,
		EmailRepeat:    &emailRepeat,
		PasswordRepeat: &passwordRepeat,
	}

	if err := params.Validate(); err == nil {
		t.Fatal("missing primary email and password should fail validation")
	}
}

func TestValidatePositiveDecimalRejectsMultipleDecimalPoints(t *testing.T) {
	if err := ValidatePositiveDecimal("1.2.3"); err == nil {
		t.Fatal("amount with multiple decimal points should fail")
	}
}

func TestPaymentCreateParamsValidateNormalizesSelectedAsset(t *testing.T) {
	orderID := " order-1 "
	amount := "10.50"
	currency := " usd "
	chainID := int64(1)
	symbol := " usdt "
	token := " 0xdAC17F958D2ee523a2206206994597C13D831ec7 "

	params := PaymentCreateParams{
		OrderID:  &orderID,
		Amount:   &amount,
		Currency: &currency,
		ChainID:  &chainID,
		Symbol:   &symbol,
		Token:    &token,
	}
	if err := params.Validate(); err != nil {
		t.Fatal(err)
	}
	if *params.OrderID != "order-1" {
		t.Fatalf("order id = %q, want trimmed", *params.OrderID)
	}
	if *params.Currency != "USD" {
		t.Fatalf("currency = %q, want USD", *params.Currency)
	}
	if params.ChainID == nil || *params.ChainID != 1 {
		t.Fatalf("chain id = %#v, want 1", params.ChainID)
	}
	if params.Symbol == nil || *params.Symbol != "USDT" {
		t.Fatalf("symbol = %#v, want USDT", params.Symbol)
	}
	if params.Token == nil || *params.Token != "0xdAC17F958D2ee523a2206206994597C13D831ec7" {
		t.Fatalf("token = %#v, want trimmed token", params.Token)
	}
}

func TestDomainParamsValidateSupportsNATSNotificationMode(t *testing.T) {
	merchantID := uuid.NewString()
	domainURL := "merchant.example.com"
	natsURL := "nats://nats.example.com:4222"
	params := DomainParams{
		MerchantID:       &merchantID,
		DomainURL:        &domainURL,
		NotificationMode: "nats",
		NATSURL:          &natsURL,
	}
	if err := params.Validate(); err != nil {
		t.Fatal(err)
	}
	if params.NotificationMode != "nats" {
		t.Fatalf("notification mode = %q, want nats", params.NotificationMode)
	}
}

func TestDomainParamsValidateRequiresActiveNotificationTarget(t *testing.T) {
	merchantID := uuid.NewString()
	domainURL := "merchant.example.com"
	params := DomainParams{MerchantID: &merchantID, DomainURL: &domainURL, NotificationMode: "nats"}
	if err := params.Validate(); err == nil {
		t.Fatal("nats mode without nats URL should fail")
	}
}

func TestPaymentCreateParamsValidateRejectsPartialAssetSelection(t *testing.T) {
	orderID := "order-1"
	amount := "10"
	currency := "USD"
	symbol := "USDT"

	params := PaymentCreateParams{
		OrderID:  &orderID,
		Amount:   &amount,
		Currency: &currency,
		Symbol:   &symbol,
	}
	if err := params.Validate(); err == nil {
		t.Fatal("partial asset selection should fail")
	}
}

func TestPaymentCreateParamsValidateNormalizesSettlementPolicyAndMemo(t *testing.T) {
	orderID := "order-1"
	amount := "10"
	currency := "USD"
	policy := " Aggregate "
	memo := " ORDER-42 "

	params := PaymentCreateParams{
		OrderID:          &orderID,
		Amount:           &amount,
		Currency:         &currency,
		SettlementPolicy: &policy,
		RequiredMemo:     &memo,
	}
	if err := params.Validate(); err != nil {
		t.Fatal(err)
	}
	if params.SettlementPolicy == nil || *params.SettlementPolicy != "aggregate" {
		t.Fatalf("settlement policy = %#v, want aggregate", params.SettlementPolicy)
	}
	if params.RequiredMemo == nil || *params.RequiredMemo != "ORDER-42" {
		t.Fatalf("required memo = %#v, want trimmed memo", params.RequiredMemo)
	}
}

func TestPaymentCreateParamsValidateRejectsUnknownSettlementPolicy(t *testing.T) {
	orderID := "order-1"
	amount := "10"
	currency := "USD"
	policy := "split"

	params := PaymentCreateParams{
		OrderID:          &orderID,
		Amount:           &amount,
		Currency:         &currency,
		SettlementPolicy: &policy,
	}
	if err := params.Validate(); err == nil {
		t.Fatal("unknown settlement policy should fail")
	}
}
