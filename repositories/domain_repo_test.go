package repositories

import (
	"context"
	"testing"
	"time"

	"core/models"
	"core/types"

	"github.com/google/uuid"
)

func TestDomainRepoCreateAllocatesHDAccountIDGlobally(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}); err != nil {
		t.Fatalf("automigrate domain tables: %v", err)
	}
	t.Setenv("MASTER_KEY", "unit-test-master-key")

	ctx := context.Background()
	merchantA := models.Merchant{ID: uuid.New(), Name: "Merchant A", Email: "a@example.com", Password: "x", IsActive: true}
	merchantB := models.Merchant{ID: uuid.New(), Name: "Merchant B", Email: "b@example.com", Password: "x", IsActive: true}
	if err := db.WithContext(ctx).Create(&merchantA).Error; err != nil {
		t.Fatalf("seed merchant A: %v", err)
	}
	if err := db.WithContext(ctx).Create(&merchantB).Error; err != nil {
		t.Fatalf("seed merchant B: %v", err)
	}

	repo := NewDomainRepo(NewMerchantRepo(db, nil))
	first := createDomainForMerchant(t, repo, ctx, merchantA.ID, "a.example.com")
	second := createDomainForMerchant(t, repo, ctx, merchantB.ID, "b.example.com")

	if first.HDAccountID != 1 {
		t.Fatalf("first hd account id = %d, want 1", first.HDAccountID)
	}
	if second.HDAccountID != 2 {
		t.Fatalf("second hd account id = %d, want 2", second.HDAccountID)
	}
	for _, domain := range []*models.Domain{first, second} {
		if domain.APIScopes != models.DefaultDomainAPIScopesCSV() {
			t.Fatalf("api scopes = %q, want default explicit scopes", domain.APIScopes)
		}
		if domain.APISecretRotationPolicy != models.DomainAPISecretRotationImmediateRevoke {
			t.Fatalf("rotation policy = %q, want immediate revoke", domain.APISecretRotationPolicy)
		}
	}
}

func TestDomainRepoRotateAPISecretRecordsImmediateRevocationPolicy(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}); err != nil {
		t.Fatalf("automigrate domain tables: %v", err)
	}
	t.Setenv("MASTER_KEY", "unit-test-master-key")

	ctx := context.Background()
	merchant := models.Merchant{ID: uuid.New(), Name: "Merchant", Email: "merchant@example.com", Password: "x", IsActive: true}
	if err := db.WithContext(ctx).Create(&merchant).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}

	repo := NewDomainRepo(NewMerchantRepo(db, nil))
	domain := createDomainForMerchant(t, repo, ctx, merchant.ID, "merchant.example.com")
	secret, err := repo.RotateAPISecret(ctx, domain.ID, merchant.ID)
	if err != nil {
		t.Fatalf("rotate api secret: %v", err)
	}
	if secret == "" {
		t.Fatal("rotated secret should be returned once")
	}
	rotated, err := repo.FindByID(types.DomainParams{Context: ctx, DomainID: stringPtr(domain.ID.String())})
	if err != nil {
		t.Fatalf("find rotated domain: %v", err)
	}
	if rotated.APISecretLastRotatedAt == nil {
		t.Fatal("APISecretLastRotatedAt should be recorded")
	}
	if rotated.APISecretRotationPolicy != models.DomainAPISecretRotationImmediateRevoke {
		t.Fatalf("rotation policy = %q, want immediate revoke", rotated.APISecretRotationPolicy)
	}
}

func TestDomainRepoAcceptSignedRequestReplayIsDurableAndTTLBound(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.APISignedRequestReplay{}); err != nil {
		t.Fatalf("automigrate signed replay table: %v", err)
	}
	ctx := context.Background()
	repo := NewDomainRepo(NewMerchantRepo(db, nil))
	domainID := uuid.New()
	expiresAt := time.Now().Add(time.Minute)

	accepted, err := repo.AcceptSignedRequestReplay(ctx, "replay-key", domainID, expiresAt)
	if err != nil {
		t.Fatalf("first accept replay: %v", err)
	}
	if !accepted {
		t.Fatal("first replay key should be accepted")
	}
	accepted, err = repo.AcceptSignedRequestReplay(ctx, "replay-key", domainID, expiresAt)
	if err != nil {
		t.Fatalf("duplicate accept replay: %v", err)
	}
	if accepted {
		t.Fatal("duplicate replay key should be rejected")
	}

	expiredKey := "expired-replay-key"
	if err := db.WithContext(ctx).Create(&models.APISignedRequestReplay{
		ID:        uuid.New(),
		ReplayKey: expiredKey,
		DomainID:  domainID,
		ExpiresAt: time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed expired replay: %v", err)
	}
	accepted, err = repo.AcceptSignedRequestReplay(ctx, expiredKey, domainID, expiresAt)
	if err != nil {
		t.Fatalf("expired accept replay: %v", err)
	}
	if !accepted {
		t.Fatal("expired replay key should be accepted after cleanup")
	}
}

func TestDomainRepoNATSUniquenessUsesActiveURLAndSubject(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}); err != nil {
		t.Fatalf("automigrate domain tables: %v", err)
	}
	t.Setenv("MASTER_KEY", "unit-test-master-key")

	ctx := context.Background()
	merchant := models.Merchant{
		ID:       uuid.New(),
		Name:     "NATS Merchant",
		Email:    "nats-" + uuid.NewString() + "@example.com",
		Password: "x",
		IsActive: true,
	}
	if err := db.WithContext(ctx).Create(&merchant).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}

	repo := NewDomainRepo(NewMerchantRepo(db, nil))
	domainURL := "merchant-nats.example.com"
	first := createNATSDomainForMerchant(t, repo, ctx, merchant.ID, domainURL, "nats://one.example.com:4222", "merchant.payments")
	second := createNATSDomainForMerchant(t, repo, ctx, merchant.ID, domainURL, "nats://one.example.com:4222", "merchant.refunds")
	third := createNATSDomainForMerchant(t, repo, ctx, merchant.ID, domainURL, "nats://two.example.com:4222", "merchant.payments")
	if first.ID == second.ID || first.ID == third.ID || second.ID == third.ID {
		t.Fatalf("distinct NATS targets should create distinct domains: %s %s %s", first.ID, second.ID, third.ID)
	}

	merchantID := merchant.ID.String()
	natsURL := "nats://one.example.com:4222"
	natsSubject := "merchant.payments"
	_, err := repo.Create(types.DomainParams{
		Context:          ctx,
		MerchantID:       &merchantID,
		DomainURL:        &domainURL,
		NotificationMode: models.DomainNotificationNATS,
		NATSURL:          &natsURL,
		NATSSubject:      &natsSubject,
	})
	if err == nil {
		t.Fatal("duplicate NATS URL and subject should be rejected")
	}

	if err := repo.UpdateConfiguration(
		ctx,
		second.ID,
		merchant.ID,
		domainURL,
		models.DomainNotificationNATS,
		"",
		nil,
		first.NATSURL,
		first.NATSSubject,
	); err == nil {
		t.Fatal("updating a domain to another domain's NATS target should be rejected")
	}
	if err := repo.UpdateConfiguration(
		ctx,
		second.ID,
		merchant.ID,
		domainURL,
		models.DomainNotificationNATS,
		"",
		nil,
		second.NATSURL,
		second.NATSSubject,
	); err != nil {
		t.Fatalf("a domain should not conflict with its own NATS target: %v", err)
	}
}

func createDomainForMerchant(t *testing.T, repo *DomainRepo, ctx context.Context, merchantID uuid.UUID, domainURL string) *models.Domain {
	t.Helper()

	merchantIDValue := merchantID.String()
	webhookURL := "https://" + domainURL + "/webhook"
	webhookSecret := "webhook-secret"
	domain, err := repo.Create(types.DomainParams{
		Context:       ctx,
		MerchantID:    &merchantIDValue,
		DomainURL:     &domainURL,
		WebhookURL:    &webhookURL,
		WebhookSecret: &webhookSecret,
	})
	if err != nil {
		t.Fatalf("create domain %s: %v", domainURL, err)
	}
	return domain
}

func createNATSDomainForMerchant(t *testing.T, repo *DomainRepo, ctx context.Context, merchantID uuid.UUID, domainURL, natsURL, natsSubject string) *models.Domain {
	t.Helper()

	merchantIDValue := merchantID.String()
	domain, err := repo.Create(types.DomainParams{
		Context:          ctx,
		MerchantID:       &merchantIDValue,
		DomainURL:        &domainURL,
		NotificationMode: models.DomainNotificationNATS,
		NATSURL:          &natsURL,
		NATSSubject:      &natsSubject,
	})
	if err != nil {
		t.Fatalf("create NATS domain %s (%s %s): %v", domainURL, natsURL, natsSubject, err)
	}
	return domain
}

func stringPtr(value string) *string {
	return &value
}
