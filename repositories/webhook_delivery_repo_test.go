package repositories

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"core/models"

	"github.com/google/uuid"
)

func TestWebhookDeliveryClaimDueSourceContract(t *testing.T) {
	sourceBytes, err := os.ReadFile("webhook_delivery_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	for _, token := range []string{
		"func (r *WebhookDeliveryRepo) ClaimDue",
		`clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}`,
		"WebhookDeliveryStatusPending",
		"WebhookDeliveryStatusFailed",
		"next_retry_at IS NULL OR next_retry_at <= ?",
		"WebhookDeliveryStatusProcessing",
		"next_retry_at",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("ClaimDue source missing %q", token)
		}
	}
	claimBody := source[strings.Index(source, "func (r *WebhookDeliveryRepo) ClaimDue"):]
	claimBody = claimBody[:strings.Index(claimBody, "\nfunc ")]
	for _, terminalStatus := range []string{models.WebhookDeliveryStatusSucceeded, models.WebhookDeliveryStatusDeadLetter} {
		if strings.Contains(claimBody, terminalStatus) {
			t.Fatalf("ClaimDue must not claim terminal status %q", terminalStatus)
		}
	}
}

func TestWebhookDeliveryClaimDuePostgresFiltersAndLocksRows(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	merchantID := uuid.New()
	domainID := uuid.New()

	duePending := webhookDeliveryTestRow(merchantID, domainID, "evt-pending", models.WebhookDeliveryStatusPending, nil)
	dueFailed := webhookDeliveryTestRow(merchantID, domainID, "evt-failed", models.WebhookDeliveryStatusFailed, nil)
	futureFailed := webhookDeliveryTestRow(merchantID, domainID, "evt-future", models.WebhookDeliveryStatusFailed, &future)
	succeeded := webhookDeliveryTestRow(merchantID, domainID, "evt-succeeded", models.WebhookDeliveryStatusSucceeded, nil)
	if err := db.Create(&[]models.WebhookDelivery{duePending, dueFailed, futureFailed, succeeded}).Error; err != nil {
		t.Fatalf("seed deliveries: %v", err)
	}

	claimed, err := repo.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d rows, want 2: %#v", len(claimed), claimed)
	}
	claimedByEvent := map[string]bool{}
	for _, row := range claimed {
		claimedByEvent[row.EventID] = true
	}
	for _, want := range []string{"evt-pending", "evt-failed"} {
		if !claimedByEvent[want] {
			t.Fatalf("expected due row %s to be claimed; got %#v", want, claimedByEvent)
		}
	}
	for _, forbidden := range []string{"evt-future", "evt-succeeded"} {
		if claimedByEvent[forbidden] {
			t.Fatalf("terminal/not-due row %s was claimed", forbidden)
		}
	}

	claimedAgain, err := repo.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("claimed locked rows again: %#v", claimedAgain)
	}
}

func webhookDeliveryTestRow(merchantID, domainID uuid.UUID, eventID, status string, nextRetryAt *time.Time) models.WebhookDelivery {
	return models.WebhookDelivery{
		ID:           uuid.New(),
		MerchantID:   merchantID,
		DomainID:     domainID,
		EventID:      eventID,
		EventType:    "payment_succeeded",
		EventVersion: "v1",
		EntityType:   "payment",
		TargetURL:    "https://example.test/webhook",
		Status:       status,
		NextRetryAt:  nextRetryAt,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
}
