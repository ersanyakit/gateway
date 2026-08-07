package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"core/constants"
	"core/models"
	webhooksvc "core/services/webhook"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestWebhookDeliveryClaimDueSourceContract(t *testing.T) {
	sourceBytes, err := os.ReadFile("webhook_delivery_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	for _, token := range []string{
		"func (r *WebhookDeliveryRepo) ClaimDue",
		"FOR UPDATE OF wd SKIP LOCKED",
		"ROW_NUMBER() OVER",
		"duplicate webhook delivery event_id suppressed before delivery",
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

func TestLatestByMerchantDomainsSourceContract(t *testing.T) {
	sourceBytes, err := os.ReadFile("webhook_delivery_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	body := source[strings.Index(source, "func (r *WebhookDeliveryRepo) LatestByMerchantDomains"):]
	body = body[:strings.Index(body, "\nfunc ")]
	for _, token := range []string{
		"merchant_id = ? AND domain_id IN ?",
		"Order(\"updated_at DESC, created_at DESC\")",
		"out[row.DomainID]",
		"merchantID == uuid.Nil",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("LatestByMerchantDomains source missing %q", token)
		}
	}
	if strings.Contains(body, "ListPage") || strings.Contains(body, "status = ?") {
		t.Fatal("LatestByMerchantDomains must not reuse unscoped admin list queries")
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

func TestWebhookDeliveryClaimTimeoutCannotUndercutLeaseSafeBatch(t *testing.T) {
	t.Setenv("WEBHOOK_DELIVERY_CLAIM_TIMEOUT", "5s")
	if got := webhookDeliveryClaimTimeout(); got != 2*time.Minute {
		t.Fatalf("claim timeout = %s, want 2m minimum", got)
	}
}

func TestWebhookDeliveryEnqueueLifecycleRequiresEntityMetadata(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}, &models.WebhookResourceSequence{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	domain := models.Domain{ID: uuid.New(), MerchantID: uuid.New(), WebhookURL: "https://example.test/webhook"}

	_, _, err := repo.EnqueueLifecycle(context.Background(), domain, webhooksvc.LifecyclePayload{
		EventID:      "manual:event",
		EventType:    "manual_test_deposit",
		EventVersion: "v1",
	})
	if !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("metadata-less lifecycle enqueue err = %v, want gorm.ErrInvalidData", err)
	}
}

func TestNewWebhookReplayDeliveryPreservesConsumerIdempotencyMetadata(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 30, 0, 0, time.UTC)
	paymentID := uuid.New()
	entityID := uuid.New()
	root := models.WebhookDelivery{
		ID:             uuid.New(),
		MerchantID:     uuid.New(),
		DomainID:       uuid.New(),
		PaymentID:      &paymentID,
		EventID:        "payment-id:payment_succeeded",
		EventType:      "payment_succeeded",
		EventVersion:   "v1",
		EntityType:     "payment",
		EntityID:       &entityID,
		ResourceType:   "payment",
		ResourceID:     entityID.String(),
		Sequence:       7,
		IdempotencyKey: "payment-id:payment_succeeded",
		PayloadJSON:    `{"event_id":"payment-id:payment_succeeded"}`,
		TargetURL:      "https://merchant.example/webhook",
		ReplayCount:    2,
	}

	replay := newWebhookReplayDelivery(root, "admin@example.com", now)
	if replay.EventID != root.EventID || replay.EventType != root.EventType || replay.EventVersion != root.EventVersion {
		t.Fatalf("replay changed consumer idempotency metadata: %#v", replay)
	}
	if replay.ResourceType != root.ResourceType || replay.ResourceID != root.ResourceID || replay.Sequence != root.Sequence || replay.IdempotencyKey != root.IdempotencyKey {
		t.Fatalf("replay changed resource ordering metadata: %#v", replay)
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

func TestWebhookDeliveryEnqueueLifecycleAssignsResourceSequenceMetadata(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}, &models.WebhookResourceSequence{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	domain := models.Domain{ID: uuid.New(), MerchantID: uuid.New(), WebhookURL: "https://example.test/webhook"}
	entityID := uuid.New().String()

	first, created, err := repo.EnqueueLifecycle(ctx, domain, webhooksvc.LifecyclePayload{
		EventID:      "payment:first",
		EventType:    "payment.succeeded.v1",
		EventVersion: "v1",
		EntityType:   "payment",
		EntityID:     entityID,
		Status:       "succeeded",
	})
	if err != nil || !created {
		t.Fatalf("enqueue first created=%v err=%v", created, err)
	}
	second, created, err := repo.EnqueueLifecycle(ctx, domain, webhooksvc.LifecyclePayload{
		EventID:      "payment:second",
		EventType:    "payment.failed.v1",
		EventVersion: "v1",
		EntityType:   "payment",
		EntityID:     entityID,
		Status:       "failed",
	})
	if err != nil || !created {
		t.Fatalf("enqueue second created=%v err=%v", created, err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d/%d, want 1/2", first.Sequence, second.Sequence)
	}
	if first.ResourceType != "payment" || first.ResourceID != entityID || first.IdempotencyKey != first.EventID {
		t.Fatalf("first metadata = %#v", first)
	}
	for _, row := range []*models.WebhookDelivery{first, second} {
		for _, token := range []string{`"sequence":`, `"idempotency_key":`, `"resource_type":"payment"`, `"resource_id":"` + entityID + `"`} {
			if !strings.Contains(row.PayloadJSON, token) {
				t.Fatalf("payload %s missing %s", row.PayloadJSON, token)
			}
		}
	}
}

func TestWebhookDeliveryEnqueueTransactionAndPaymentPersistImmutablePayloads(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}, &models.WebhookResourceSequence{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	domain := models.Domain{ID: domainID, MerchantID: merchantID, WebhookURL: "https://example.test/webhook"}

	txModel := models.Transaction{
		ID:          uuid.New(),
		ChainID:     constants.Ethereum,
		UniqueHash:  "immutable-tx-" + uuid.NewString(),
		Hash:        "0ximmutable",
		BlockNumber: "42",
		BlockHash:   "0xblock",
		Symbol:      "ETH",
		Decimals:    18,
		FromAddress: "0xfrom",
		ToAddress:   "0xto",
		Amount:      "100",
		Status:      models.TransactionStatusConfirmed,
		EventType:   constants.WebhookEventNativeTransfer,
		WalletID:    &walletID,
		MerchantID:  &merchantID,
		DomainID:    &domainID,
		CreatedAt:   time.Now().UTC(),
	}
	transactionDelivery, created, err := repo.EnqueueTransaction(ctx, domain, txModel)
	if err != nil || !created {
		t.Fatalf("enqueue transaction created=%v err=%v", created, err)
	}
	txModel.Amount = "999"
	txModel.Status = models.TransactionStatusReorged
	repeatedTransaction, created, err := repo.EnqueueTransaction(ctx, domain, txModel)
	if err != nil || created {
		t.Fatalf("repeat transaction created=%v err=%v", created, err)
	}
	if repeatedTransaction.ID != transactionDelivery.ID {
		t.Fatalf("repeat transaction delivery id = %s, want %s", repeatedTransaction.ID, transactionDelivery.ID)
	}
	var transactionPayload webhooksvc.Payload
	if err := json.Unmarshal([]byte(repeatedTransaction.PayloadJSON), &transactionPayload); err != nil {
		t.Fatal(err)
	}
	if transactionPayload.AmountRaw != "100" || transactionPayload.Status != models.TransactionStatusConfirmed {
		t.Fatalf("transaction snapshot mutated: %#v", transactionPayload)
	}
	if transactionPayload.DeliveryID != transactionDelivery.ID.String() || transactionPayload.Sequence != 1 || transactionPayload.ResourceID != txModel.ID.String() {
		t.Fatalf("transaction delivery metadata missing: %#v", transactionPayload)
	}

	session := models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "immutable-payment-" + uuid.NewString(),
		MerchantID:   merchantID,
		DomainID:     domainID,
		WalletID:     walletID,
		OrderID:      "order-immutable",
		Amount:       "25.00",
		Currency:     "USD",
		Status:       models.PaymentStatusPaid,
		WebhookEvent: constants.WebhookEventPaymentSucceeded,
		CreatedAt:    time.Now().UTC(),
	}
	paymentDelivery, created, err := repo.EnqueuePayment(ctx, domain, session)
	if err != nil || !created {
		t.Fatalf("enqueue payment created=%v err=%v", created, err)
	}
	session.Amount = "75.00"
	session.Status = models.PaymentStatusFailed
	repeatedPayment, created, err := repo.EnqueuePayment(ctx, domain, session)
	if err != nil || created {
		t.Fatalf("repeat payment created=%v err=%v", created, err)
	}
	var paymentPayload webhooksvc.PaymentPayload
	if err := json.Unmarshal([]byte(repeatedPayment.PayloadJSON), &paymentPayload); err != nil {
		t.Fatal(err)
	}
	if paymentPayload.Amount != "25.00" || paymentPayload.Status != models.PaymentStatusPaid {
		t.Fatalf("payment snapshot mutated: %#v", paymentPayload)
	}
	if paymentPayload.DeliveryID != paymentDelivery.ID.String() || paymentPayload.Sequence != 1 || paymentPayload.ResourceID != session.ID.String() {
		t.Fatalf("payment delivery metadata missing: %#v", paymentPayload)
	}
}

func TestWebhookDeliveryEnqueueSnapshotsNATSTargetAndSubject(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}, &models.WebhookResourceSequence{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	domain := models.Domain{
		ID: uuid.New(), MerchantID: uuid.New(), NotificationMode: models.DomainNotificationNATS,
		NATSURL: "nats://queued.example:4222", NATSSubject: "merchant.queued.events",
	}
	entityID := uuid.New()
	payload := webhooksvc.LifecyclePayload{
		EventID: "target-snapshot-" + entityID.String(), EventType: "payout.finalized.v1", EventVersion: "v1",
		EntityType: "payout", EntityID: entityID.String(), MerchantID: domain.MerchantID.String(), DomainID: domain.ID.String(),
	}
	delivery, created, err := NewWebhookDeliveryRepo(db).EnqueueLifecycle(context.Background(), domain, payload)
	if err != nil || !created {
		t.Fatalf("enqueue created=%v err=%v", created, err)
	}
	if delivery.NotificationMode != models.DomainNotificationNATS || delivery.TargetURL != domain.NATSURL || delivery.TargetSubject != domain.NATSSubject {
		t.Fatalf("target snapshot = %#v", delivery)
	}
}

func TestWebhookDeliveryEnqueueMoneyEventIsIdempotentAndPreservesRawSnapshot(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}, &models.WebhookResourceSequence{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	paymentID := uuid.New()
	domain := models.Domain{ID: uuid.New(), MerchantID: uuid.New(), WebhookURL: "https://example.test/webhook"}
	event := models.MoneyEventOutbox{
		EventID:        paymentID.String() + ":payment.succeeded.v1",
		EventType:      "payment.succeeded.v1",
		EventVersion:   constants.WebhookEventVersionV1,
		AggregateType:  "payment",
		AggregateID:    paymentID.String(),
		MerchantID:     domain.MerchantID,
		DomainID:       domain.ID,
		IdempotencyKey: "money-event-" + paymentID.String(),
		PayloadJSON:    `{"event_id":"` + paymentID.String() + `:payment.succeeded.v1","marker":"original"}`,
	}

	delivery, created, err := repo.EnqueueMoneyEvent(ctx, domain, event)
	if err != nil || !created {
		t.Fatalf("enqueue money event created=%v err=%v", created, err)
	}
	if delivery.PaymentID == nil || *delivery.PaymentID != paymentID || delivery.ResourceType != "payment" || delivery.ResourceID != paymentID.String() {
		t.Fatalf("money-event delivery linkage = %#v", delivery)
	}
	if delivery.EventID != event.EventID || delivery.EventType != event.EventType || delivery.EventVersion != event.EventVersion || delivery.IdempotencyKey != event.IdempotencyKey {
		t.Fatalf("money-event identity = %#v", delivery)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(delivery.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["marker"] != "original" || payload["delivery_id"] != delivery.ID.String() || payload["resource_id"] != paymentID.String() {
		t.Fatalf("money-event payload = %#v", payload)
	}

	repeated, created, err := repo.EnqueueMoneyEvent(ctx, domain, event)
	if err != nil || created {
		t.Fatalf("repeat money event created=%v err=%v", created, err)
	}
	if repeated.ID != delivery.ID {
		t.Fatalf("idempotent money-event snapshot changed: %#v", repeated)
	}
	event.PayloadJSON = `{"event_id":"` + event.EventID + `","marker":"mutated"}`
	if _, _, err := repo.EnqueueMoneyEvent(ctx, domain, event); !errors.Is(err, ErrWebhookDeliveryConflict) {
		t.Fatalf("conflicting money-event payload err = %v, want ErrWebhookDeliveryConflict", err)
	}

	transactionID := uuid.New()
	transactionEvent := event
	transactionEvent.EventID = transactionID.String() + ":transaction.reorged.v1"
	transactionEvent.EventType = "transaction.reorged.v1"
	transactionEvent.AggregateType = "transaction"
	transactionEvent.AggregateID = transactionID.String()
	transactionEvent.IdempotencyKey = transactionEvent.EventID
	transactionEvent.PayloadJSON = `{"event_id":"` + transactionEvent.EventID + `","marker":"transaction"}`
	transactionDelivery, created, err := repo.EnqueueMoneyEvent(ctx, domain, transactionEvent)
	if err != nil || !created {
		t.Fatalf("enqueue transaction money event created=%v err=%v", created, err)
	}
	if transactionDelivery.TransactionID == nil || *transactionDelivery.TransactionID != transactionID || transactionDelivery.PaymentID != nil {
		t.Fatalf("transaction money-event linkage = %#v", transactionDelivery)
	}
}

func TestWebhookDeliveryHasActivePaymentDeliveryOnlyIncludesRetryableWork(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	paymentID := uuid.New()

	active, err := repo.HasActivePaymentDelivery(ctx, paymentID)
	if err != nil || active {
		t.Fatalf("empty active lookup = %v, err=%v", active, err)
	}
	succeeded := webhookDeliveryTestRow(merchantID, domainID, "succeeded-"+uuid.NewString(), models.WebhookDeliveryStatusSucceeded, nil)
	succeeded.PaymentID = &paymentID
	if err := db.Create(&succeeded).Error; err != nil {
		t.Fatal(err)
	}
	active, err = repo.HasActivePaymentDelivery(ctx, paymentID)
	if err != nil || active {
		t.Fatalf("succeeded delivery active = %v, err=%v", active, err)
	}

	failed := webhookDeliveryTestRow(merchantID, domainID, "failed-"+uuid.NewString(), models.WebhookDeliveryStatusFailed, nil)
	failed.PaymentID = &paymentID
	if err := db.Create(&failed).Error; err != nil {
		t.Fatal(err)
	}
	active, err = repo.HasActivePaymentDelivery(ctx, paymentID)
	if err != nil || !active {
		t.Fatalf("failed delivery active = %v, err=%v", active, err)
	}
	if err := db.Model(&models.WebhookDelivery{}).Where("id = ?", failed.ID).Update("status", models.WebhookDeliveryStatusDeadLetter).Error; err != nil {
		t.Fatal(err)
	}
	active, err = repo.HasActivePaymentDelivery(ctx, paymentID)
	if err != nil || active {
		t.Fatalf("terminal deliveries active = %v, err=%v", active, err)
	}
}

func TestWebhookDeliveryHasPaymentDeliveryForEventHonorsAliasesAndTerminalRows(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatal(err)
	}
	repo := NewWebhookDeliveryRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()

	succeededPaymentID := uuid.New()
	succeeded := webhookDeliveryTestRow(merchantID, domainID, "canonical-success", models.WebhookDeliveryStatusSucceeded, nil)
	succeeded.PaymentID = &succeededPaymentID
	succeeded.EventType = "payment.succeeded.v1"
	if err := db.Create(&succeeded).Error; err != nil {
		t.Fatal(err)
	}
	found, delivered, err := repo.HasPaymentDeliveryForEvent(context.Background(), succeededPaymentID, constants.WebhookEventPaymentSucceeded)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !delivered {
		t.Fatalf("succeeded canonical delivery = found:%v delivered:%v", found, delivered)
	}

	deadLetterPaymentID := uuid.New()
	deadLetter := webhookDeliveryTestRow(merchantID, domainID, "canonical-dead-letter", models.WebhookDeliveryStatusDeadLetter, nil)
	deadLetter.PaymentID = &deadLetterPaymentID
	deadLetter.EventType = "payment.failed.v1"
	if err := db.Create(&deadLetter).Error; err != nil {
		t.Fatal(err)
	}
	found, delivered, err = repo.HasPaymentDeliveryForEvent(context.Background(), deadLetterPaymentID, constants.WebhookEventPaymentFailed)
	if err != nil {
		t.Fatal(err)
	}
	if !found || delivered {
		t.Fatalf("dead-letter canonical delivery = found:%v delivered:%v", found, delivered)
	}

	found, delivered, err = repo.HasPaymentDeliveryForEvent(context.Background(), deadLetterPaymentID, constants.WebhookEventPaymentSucceeded)
	if err != nil {
		t.Fatal(err)
	}
	if found || delivered {
		t.Fatalf("different payment lifecycle event = found:%v delivered:%v", found, delivered)
	}
}

func TestWebhookDeliveryClaimDuePreservesResourceOrder(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	resourceID := uuid.New().String()

	first := webhookDeliveryTestRow(merchantID, domainID, "evt-1", models.WebhookDeliveryStatusFailed, nil)
	first.ResourceType = "payment"
	first.ResourceID = resourceID
	first.Sequence = 1
	first.NextRetryAt = ptrTime(time.Now().UTC().Add(time.Hour))
	second := webhookDeliveryTestRow(merchantID, domainID, "evt-2", models.WebhookDeliveryStatusPending, nil)
	second.ResourceType = "payment"
	second.ResourceID = resourceID
	second.Sequence = 2
	if err := db.Create(&[]models.WebhookDelivery{first, second}).Error; err != nil {
		t.Fatalf("seed ordered deliveries: %v", err)
	}

	claimed, err := repo.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed later resource event while earlier retry was active: %#v", claimed)
	}

	if err := db.Model(&models.WebhookDelivery{}).Where("id = ?", first.ID).Updates(map[string]any{
		"status":        models.WebhookDeliveryStatusSucceeded,
		"next_retry_at": nil,
	}).Error; err != nil {
		t.Fatalf("mark first succeeded: %v", err)
	}
	claimed, err = repo.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != second.ID {
		t.Fatalf("claimed after first terminal = %#v, want second", claimed)
	}
}

func TestWebhookDeliveryDeadLetterBlocksLaterSequenceUntilReplaySucceeds(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	resourceID := uuid.NewString()

	first := webhookDeliveryTestRow(merchantID, domainID, "evt-dead-letter-first", models.WebhookDeliveryStatusDeadLetter, nil)
	first.ResourceType = "payment"
	first.ResourceID = resourceID
	first.Sequence = 1
	second := webhookDeliveryTestRow(merchantID, domainID, "evt-pending-second", models.WebhookDeliveryStatusPending, nil)
	second.ResourceType = "payment"
	second.ResourceID = resourceID
	second.Sequence = 2
	if err := db.Create(&[]models.WebhookDelivery{first, second}).Error; err != nil {
		t.Fatalf("seed ordered deliveries: %v", err)
	}

	claimed, err := repo.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("dead-letter predecessor did not block later sequence: %#v", claimed)
	}

	replay, created, err := repo.EnqueueReplay(ctx, WebhookReplayParams{
		DeliveryID: first.ID,
		ActorEmail: "operator@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created || replay.Sequence != first.Sequence || replay.OriginalDeliveryID == nil || *replay.OriginalDeliveryID != first.ID {
		t.Fatalf("replay did not preserve original ordering identity: %#v", replay)
	}

	claimed, err = repo.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != replay.ID || claimed[0].LeaseToken == nil {
		t.Fatalf("claim while predecessor replay pending = %#v, want only replay", claimed)
	}
	if err := repo.MarkAttempt(ctx, replay.ID, *claimed[0].LeaseToken, true, nil); err != nil {
		t.Fatalf("acknowledge replay: %v", err)
	}

	claimed, err = repo.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != second.ID {
		t.Fatalf("claim after replay resolved original = %#v, want second", claimed)
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

func TestWebhookDeliveryReplayDoesNotRaceActiveOriginal(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	root := webhookDeliveryTestRow(uuid.New(), uuid.New(), "evt-active-original", models.WebhookDeliveryStatusFailed, nil)
	root.ResourceType = "payment"
	root.ResourceID = uuid.NewString()
	root.Sequence = 4
	if err := db.Create(&root).Error; err != nil {
		t.Fatalf("seed active original: %v", err)
	}

	delivery, created, err := repo.EnqueueReplay(context.Background(), WebhookReplayParams{
		DeliveryID: root.ID,
		ActorEmail: "operator@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created || delivery == nil || delivery.ID != root.ID {
		t.Fatalf("active original replay = delivery:%#v created:%v, want original no-op", delivery, created)
	}
	var replayCount int64
	if err := db.Model(&models.WebhookDelivery{}).Where("original_delivery_id = ?", root.ID).Count(&replayCount).Error; err != nil {
		t.Fatal(err)
	}
	if replayCount != 0 {
		t.Fatalf("active original created %d replay rows, want 0", replayCount)
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
	staleProcessingWithoutLease := webhookDeliveryTestRow(merchantID, domainID, "evt-processing-null-lease", models.WebhookDeliveryStatusProcessing, nil)
	futureFailed := webhookDeliveryTestRow(merchantID, domainID, "evt-future", models.WebhookDeliveryStatusFailed, &future)
	succeeded := webhookDeliveryTestRow(merchantID, domainID, "evt-succeeded", models.WebhookDeliveryStatusSucceeded, nil)
	if err := db.Create(&[]models.WebhookDelivery{duePending, dueFailed, staleProcessingWithoutLease, futureFailed, succeeded}).Error; err != nil {
		t.Fatalf("seed deliveries: %v", err)
	}

	claimed, err := repo.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 3 {
		t.Fatalf("claimed %d rows, want 3: %#v", len(claimed), claimed)
	}
	claimedByEvent := map[string]bool{}
	claimTokens := map[uuid.UUID]bool{}
	for _, row := range claimed {
		claimedByEvent[row.EventID] = true
		if row.LeaseToken == nil || *row.LeaseToken == uuid.Nil || claimTokens[*row.LeaseToken] {
			t.Fatalf("claimed row has missing or duplicate lease token: %#v", row)
		}
		claimTokens[*row.LeaseToken] = true
	}
	for _, want := range []string{"evt-pending", "evt-failed", "evt-processing-null-lease"} {
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

func TestWebhookDeliveryExpiredLeaseStaleWorkerCannotOverwriteNewClaim(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	row := webhookDeliveryTestRow(uuid.New(), uuid.New(), "evt-overlapping-workers", models.WebhookDeliveryStatusPending, nil)
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	firstClaim, err := repo.ClaimDue(ctx, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstClaim) != 1 || firstClaim[0].LeaseToken == nil {
		t.Fatalf("first claim = %#v", firstClaim)
	}
	firstToken := *firstClaim[0].LeaseToken
	if err := db.Model(&models.WebhookDelivery{}).
		Where("id = ?", row.ID).
		Update("next_retry_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatalf("expire first lease: %v", err)
	}

	secondClaim, err := repo.ClaimDue(ctx, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondClaim) != 1 || secondClaim[0].LeaseToken == nil {
		t.Fatalf("second claim = %#v", secondClaim)
	}
	secondToken := *secondClaim[0].LeaseToken
	if secondToken == firstToken {
		t.Fatalf("reclaim reused lease token %s", secondToken)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		results <- repo.MarkAttempt(ctx, row.ID, firstToken, false, errors.New("stale worker timeout"))
	}()
	go func() {
		defer workers.Done()
		<-start
		results <- repo.MarkAttempt(ctx, row.ID, secondToken, true, nil)
	}()
	close(start)
	workers.Wait()
	close(results)

	leaseLost := 0
	succeeded := 0
	for result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, ErrWebhookDeliveryLeaseLost):
			leaseLost++
		default:
			t.Fatalf("overlapping mark result: %v", result)
		}
	}
	if succeeded != 1 || leaseLost != 1 {
		t.Fatalf("overlapping marks succeeded=%d lease_lost=%d, want 1/1", succeeded, leaseLost)
	}

	var got models.WebhookDelivery
	if err := db.First(&got, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.WebhookDeliveryStatusSucceeded || got.Attempts != 1 || got.LeaseToken != nil || got.DeliveredAt == nil {
		t.Fatalf("authoritative second claim was overwritten: %#v", got)
	}
}

func TestWebhookDeliveryClaimDueSuppressesDuplicateActiveEventIDs(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	eventID := "evt-duplicate"

	older := webhookDeliveryTestRow(merchantID, domainID, eventID, models.WebhookDeliveryStatusPending, nil)
	newer := webhookDeliveryTestRow(merchantID, domainID, eventID, models.WebhookDeliveryStatusPending, nil)
	older.UpdatedAt = time.Now().Add(-2 * time.Minute)
	newer.UpdatedAt = time.Now().Add(-time.Minute)
	if err := db.Create(&[]models.WebhookDelivery{older, newer}).Error; err != nil {
		t.Fatalf("seed duplicate deliveries: %v", err)
	}

	claimed, err := repo.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].EventID != eventID || claimed[0].ID != older.ID {
		t.Fatalf("claimed duplicate rows = %#v, want only oldest %s", claimed, older.ID)
	}

	var olderAfter, newerAfter models.WebhookDelivery
	if err := db.First(&olderAfter, "id = ?", older.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&newerAfter, "id = ?", newer.ID).Error; err != nil {
		t.Fatal(err)
	}
	if olderAfter.Status != models.WebhookDeliveryStatusProcessing || olderAfter.OperatorAction != "delivery_in_progress" {
		t.Fatalf("oldest row state = %s/%s, want processing/delivery_in_progress", olderAfter.Status, olderAfter.OperatorAction)
	}
	if newerAfter.Status != models.WebhookDeliveryStatusDeadLetter ||
		newerAfter.OperatorAction != "duplicate_suppressed" ||
		newerAfter.FailureCategory != "duplicate" ||
		newerAfter.NextRetryAt != nil {
		t.Fatalf("duplicate row state = %#v, want suppressed dead letter", newerAfter)
	}
}

func TestWebhookDeliveryClaimDueSuppressesDuplicatesOutsideClaimLimit(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	eventID := "evt-duplicate-limit"

	older := webhookDeliveryTestRow(merchantID, domainID, eventID, models.WebhookDeliveryStatusPending, nil)
	newer := webhookDeliveryTestRow(merchantID, domainID, eventID, models.WebhookDeliveryStatusPending, nil)
	older.UpdatedAt = time.Now().Add(-2 * time.Minute)
	newer.UpdatedAt = time.Now().Add(-time.Minute)
	if err := db.Create(&[]models.WebhookDelivery{older, newer}).Error; err != nil {
		t.Fatalf("seed duplicate deliveries: %v", err)
	}

	claimed, err := repo.ClaimDue(ctx, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != older.ID {
		t.Fatalf("claimed rows = %#v, want only oldest %s", claimed, older.ID)
	}

	var newerAfter models.WebhookDelivery
	if err := db.First(&newerAfter, "id = ?", newer.ID).Error; err != nil {
		t.Fatal(err)
	}
	if newerAfter.Status != models.WebhookDeliveryStatusDeadLetter ||
		newerAfter.OperatorAction != "duplicate_suppressed" ||
		newerAfter.FailureCategory != "duplicate" {
		t.Fatalf("duplicate outside limit state = %#v, want suppressed dead letter", newerAfter)
	}

	again, err := repo.ClaimDue(ctx, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("duplicate was claimed after first batch: %#v", again)
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

	claimed, err := repo.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != replay.ID || claimed[0].LeaseToken == nil {
		t.Fatalf("claimed replay = %#v", claimed)
	}
	if err := repo.MarkAttempt(ctx, replay.ID, *claimed[0].LeaseToken, true, nil); err != nil {
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

func TestWebhookDeliveryMarkAttemptRejectsWorkerAfterAcknowledgedSuccess(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WebhookDelivery{}); err != nil {
		t.Fatalf("automigrate webhook deliveries: %v", err)
	}
	repo := NewWebhookDeliveryRepo(db)
	row := webhookDeliveryTestRow(uuid.New(), uuid.New(), "evt-stale-failure", models.WebhookDeliveryStatusSucceeded, nil)
	deliveredAt := time.Now().UTC().Add(-time.Minute)
	row.DeliveredAt = &deliveredAt
	row.Attempts = 1
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed succeeded delivery: %v", err)
	}

	if err := repo.MarkAttempt(context.Background(), row.ID, uuid.New(), false, errors.New("stale worker timeout")); !errors.Is(err, ErrWebhookDeliveryLeaseLost) {
		t.Fatalf("stale attempt error = %v, want ErrWebhookDeliveryLeaseLost", err)
	}
	var got models.WebhookDelivery
	if err := db.First(&got, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.WebhookDeliveryStatusSucceeded || got.Attempts != row.Attempts || got.DeliveredAt == nil || !got.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("stale failure regressed acknowledged delivery: %#v", got)
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

func ptrTime(value time.Time) *time.Time {
	return &value
}
