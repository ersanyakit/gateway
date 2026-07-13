package repositories

import (
	"context"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestPendingWebhookRecoveryIncludesNATSDomains(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Merchant{},
		&models.Domain{},
		&models.Wallet{},
		&models.PaymentSession{},
		&models.Transaction{},
	); err != nil {
		t.Fatalf("automigrate notification recovery models: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC()
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	merchant := models.Merchant{
		ID:        merchantID,
		Name:      "NATS Recovery Merchant",
		Email:     "nats-recovery-" + uuid.NewString() + "@example.test",
		Password:  "x",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domain := models.Domain{
		ID:               domainID,
		MerchantID:       merchantID,
		DomainURL:        "nats-recovery.example.test",
		APIKey:           "pk_" + uuid.NewString(),
		APISecret:        "secret",
		HDAccountID:      8801,
		NotificationMode: models.DomainNotificationNATS,
		NATSURL:          "nats://events.example.test:4222",
		NATSSubject:      "merchant.events",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	wallet := models.Wallet{
		ID:              walletID,
		HDAccountID:     domain.HDAccountID,
		HDAddressId:     1,
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       "nats-recovery-product",
		UserID:          "nats-recovery-user",
		EthereumAddress: "0x" + uuid.NewString(),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.WithContext(ctx).Create(&merchant).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.WithContext(ctx).Create(&domain).Error; err != nil {
		t.Fatalf("seed NATS domain: %v", err)
	}
	if err := db.WithContext(ctx).Create(&wallet).Error; err != nil {
		t.Fatalf("seed wallet: %v", err)
	}

	payment := models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "nats-recovery-" + uuid.NewString(),
		MerchantID:   merchantID,
		DomainID:     domainID,
		WalletID:     walletID,
		OrderID:      "order-" + uuid.NewString(),
		ProductID:    wallet.ProductID,
		UserID:       wallet.UserID,
		LinkType:     models.PaymentLinkTypeFixed,
		Amount:       "10.00",
		Currency:     "USD",
		Status:       models.PaymentStatusExpired,
		WebhookEvent: constants.WebhookEventPaymentExpired,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	transaction := models.Transaction{
		ID:                    uuid.New(),
		ChainID:               constants.Ethereum,
		UniqueHash:            "1-" + uuid.NewString() + "-0",
		Hash:                  "0x" + uuid.NewString(),
		BlockNumber:           "100",
		BlockHash:             "0xblock",
		Symbol:                "ETH",
		Decimals:              18,
		FromAddress:           "0xfrom",
		ToAddress:             wallet.EthereumAddress,
		Amount:                "1000000000000000000",
		Status:                models.TransactionStatusConfirmed,
		ConfirmationsRequired: 1,
		FinalizedAt:           &now,
		EventType:             constants.WebhookEventNativeTransfer,
		WalletID:              &walletID,
		MerchantID:            &merchantID,
		DomainID:              &domainID,
		ProductID:             wallet.ProductID,
		UserID:                wallet.UserID,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := db.WithContext(ctx).Create(&payment).Error; err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	if err := db.WithContext(ctx).Create(&transaction).Error; err != nil {
		t.Fatalf("seed transaction: %v", err)
	}

	pendingPayments, err := NewPaymentRepo(db).ListPendingWebhooks(ctx, 10)
	if err != nil {
		t.Fatalf("list pending NATS payment deliveries: %v", err)
	}
	if len(pendingPayments) != 1 || pendingPayments[0].ID != payment.ID {
		t.Fatalf("pending NATS payments = %#v, want %s", pendingPayments, payment.ID)
	}
	if pendingPayments[0].Domain.ID != domainID || !pendingPayments[0].Domain.UsesNATS() {
		t.Fatalf("pending payment domain was not preloaded as NATS: %#v", pendingPayments[0].Domain)
	}

	pendingTransactions, err := NewTransactionRepo(db).ListPendingWebhooks(ctx, 10)
	if err != nil {
		t.Fatalf("list pending NATS transaction deliveries: %v", err)
	}
	if len(pendingTransactions) != 1 || pendingTransactions[0].ID != transaction.ID {
		t.Fatalf("pending NATS transactions = %#v, want %s", pendingTransactions, transaction.ID)
	}
}
