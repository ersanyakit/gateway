package repositories

import (
	"context"
	"errors"
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

func TestWebhookReplaySourceContract(t *testing.T) {
	sourceBytes, err := os.ReadFile("webhook_delivery_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	for _, token := range []string{
		"func (r *WebhookDeliveryRepo) EnqueueReplay",
		"ErrWebhookReplayScopeDenied",
		"original_delivery_id",
		"webhookReplayActiveStatuses",
		"WebhookDeliveryStatusFailed",
		"replay_count",
		"replay_requested_by",
		"operator_action",
		"replay_queued",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("replay source missing %q", token)
		}
	}
}

func TestWebhookDeliveryFailureState(t *testing.T) {
	t.Setenv("WEBHOOK_MAX_ATTEMPTS", "3")

	status, action := webhookDeliveryFailureState(0, errors.New("timeout"))
	if status != models.WebhookDeliveryStatusFailed || action != "waiting_retry" {
		t.Fatalf("transient state = %s/%s, want failed/waiting_retry", status, action)
	}

	status, action = webhookDeliveryFailureState(2, errors.New("timeout"))
	if status != models.WebhookDeliveryStatusDeadLetter || action != "replay_or_investigate" {
		t.Fatalf("exhausted state = %s/%s, want dead_letter/replay_or_investigate", status, action)
	}

	status, action = webhookDeliveryFailureState(0, testPermanentError{err: errors.New("invalid callback")})
	if status != models.WebhookDeliveryStatusDeadLetter || action != "replay_or_investigate" {
		t.Fatalf("permanent state = %s/%s, want dead_letter/replay_or_investigate", status, action)
	}
}

func TestNewWebhookReplayDeliveryPreservesConsumerIdempotencyMetadata(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 30, 0, 0, time.UTC)
	paymentID := uuid.New()
	entityID := uuid.New()
	root := models.WebhookDelivery{
		ID:           uuid.New(),
		MerchantID:   uuid.New(),
		DomainID:     uuid.New(),
		PaymentID:    &paymentID,
		EventID:      "payment-id:payment_succeeded",
		EventType:    "payment_succeeded",
		EventVersion: "v1",
		EntityType:   "payment",
		EntityID:     &entityID,
		PayloadJSON:  `{"event_id":"payment-id:payment_succeeded"}`,
		TargetURL:    "https://merchant.example/webhook",
		ReplayCount:  2,
	}

	replay := newWebhookReplayDelivery(root, "admin@example.com", now)
	if replay.EventID != root.EventID || replay.EventType != root.EventType || replay.EventVersion != root.EventVersion {
		t.Fatalf("replay changed consumer idempotency metadata: %#v", replay)
	}
	if replay.OriginalDeliveryID == nil || *replay.OriginalDeliveryID != root.ID {
		t.Fatalf("original delivery id = %#v, want %s", replay.OriginalDeliveryID, root.ID)
	}
	if replay.Status != models.WebhookDeliveryStatusPending || replay.Attempts != 0 {
		t.Fatalf("replay status/attempts = %s/%d, want pending/0", replay.Status, replay.Attempts)
	}
	if replay.ReplayCount != 3 || replay.ReplayRequestedBy != "admin@example.com" || replay.ReplayRequestedAt == nil || !replay.ReplayRequestedAt.Equal(now) {
		t.Fatalf("replay metadata = %#v", replay)
	}
}

func TestWebhookReplayActiveStatusesIncludesRetryableFailures(t *testing.T) {
	statuses := webhookReplayActiveStatuses()
	for _, want := range []string{models.WebhookDeliveryStatusPending, models.WebhookDeliveryStatusProcessing, models.WebhookDeliveryStatusFailed} {
		if !containsWebhookReplayStatus(statuses, want) {
			t.Fatalf("active statuses %v missing %s", statuses, want)
		}
	}
	for _, forbidden := range []string{models.WebhookDeliveryStatusSucceeded, models.WebhookDeliveryStatusDeadLetter} {
		if containsWebhookReplayStatus(statuses, forbidden) {
			t.Fatalf("active statuses %v must not include terminal %s", statuses, forbidden)
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

func TestWebhookDeliveryReplayPostgresCreatesDuplicateSafeReplay(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	root := webhookDeliveryTestRow(merchantID, domainID, "evt-replay", models.WebhookDeliveryStatusDeadLetter, nil)
	root.LastError = "timeout"
	root.FailureCategory = "timeout"
	root.OperatorAction = "replay_or_investigate"
	if err := db.Create(&root).Error; err != nil {
		t.Fatalf("seed root delivery: %v", err)
	}

	replay, created, err := repo.EnqueueReplay(ctx, WebhookReplayParams{DeliveryID: root.ID, ActorEmail: "admin@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first replay should create a row")
	}
	if replay.EventID != root.EventID || replay.EventType != root.EventType || replay.EventVersion != root.EventVersion {
		t.Fatalf("replay changed event metadata: %#v", replay)
	}
	if replay.OriginalDeliveryID == nil || *replay.OriginalDeliveryID != root.ID {
		t.Fatalf("replay original id = %#v, want %s", replay.OriginalDeliveryID, root.ID)
	}

	again, created, err := repo.EnqueueReplay(ctx, WebhookReplayParams{DeliveryID: root.ID, ActorEmail: "admin@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if created || again.ID != replay.ID {
		t.Fatalf("duplicate active replay created=%v id=%s want existing %s", created, again.ID, replay.ID)
	}

	if err := repo.MarkAttempt(ctx, replay.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	next, created, err := repo.EnqueueReplay(ctx, WebhookReplayParams{DeliveryID: root.ID, ActorEmail: "admin@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !created || next.ID == replay.ID {
		t.Fatalf("new replay after terminal prior created=%v next=%s prior=%s", created, next.ID, replay.ID)
	}

	wrongMerchant := uuid.New()
	if _, _, err := repo.EnqueueReplay(ctx, WebhookReplayParams{DeliveryID: root.ID, ActorEmail: "admin@example.com", MerchantID: &wrongMerchant}); !errors.Is(err, ErrWebhookReplayScopeDenied) {
		t.Fatalf("scope denied err = %v, want ErrWebhookReplayScopeDenied", err)
	}
}

func containsWebhookReplayStatus(statuses []string, want string) bool {
	for _, status := range statuses {
		if status == want {
			return true
		}
	}
	return false
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
