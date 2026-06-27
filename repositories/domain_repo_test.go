package repositories

import (
	"context"
	"testing"

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
