package repositories

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"core/constants"
	"core/models"
	webhooksvc "core/services/webhook"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBuildMoneyEventOutboxFromCatalog(t *testing.T) {
	merchantID := uuid.New()
	domainID := uuid.New()

	event, err := BuildMoneyEventOutboxRecord(MoneyEventOutboxBuildInput{
		EventType:      "payment.succeeded.v1",
		EventID:        " payment_uuid:payment.succeeded.v1 ",
		AggregateType:  " payment ",
		AggregateID:    " payment_uuid ",
		MerchantID:     merchantID,
		DomainID:       domainID,
		IdempotencyKey: " payment_uuid:payment.succeeded.v1 ",
		Payload: map[string]any{
			"event_type": "payment.succeeded.v1",
			"event_id":   "payment_uuid:payment.succeeded.v1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.EventType != "payment.succeeded.v1" || event.EventVersion != constants.WebhookEventVersionV1 {
		t.Fatalf("event identity = %#v", event)
	}
	if event.EventID != "payment_uuid:payment.succeeded.v1" || event.AggregateType != "payment" || event.AggregateID != "payment_uuid" {
		t.Fatalf("event fields were not normalized: %#v", event)
	}
	if event.Status != models.MoneyEventOutboxStatusPending {
		t.Fatalf("status = %q", event.Status)
	}
	if event.PayloadJSON != `{"event_id":"payment_uuid:payment.succeeded.v1","event_type":"payment.succeeded.v1"}` {
		t.Fatalf("payload was not canonicalized: %s", event.PayloadJSON)
	}
}

func TestBuildMoneyEventOutboxRejectsUnknownCatalogEvent(t *testing.T) {
	_, err := BuildMoneyEventOutboxRecord(MoneyEventOutboxBuildInput{
		EventType:      "unknown.event.v1",
		EventID:        "evt",
		AggregateType:  "payment",
		AggregateID:    "payment_uuid",
		MerchantID:     uuid.New(),
		DomainID:       uuid.New(),
		IdempotencyKey: "idem",
		Payload:        map[string]any{"ok": true},
	})
	if err == nil {
		t.Fatal("unknown catalog event should fail")
	}
}

func TestMoneyEventOutboxRecordLifecycleWithDBIsIdempotentAndTransactional(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	entityID := uuid.New()
	payload := webhooksvc.LifecyclePayload{
		EventID:        entityID.String() + ":" + constants.WebhookEventPayoutRequestedV1,
		EventType:      constants.WebhookEventPayoutRequestedV1,
		EventVersion:   constants.WebhookEventVersionV1,
		OccurredAt:     "2026-07-18T00:00:00Z",
		EntityType:     webhooksvc.EntityTypePayout,
		EntityID:       entityID.String(),
		ResourceType:   webhooksvc.EntityTypePayout,
		ResourceID:     entityID.String(),
		ResourceStatus: models.WithdrawalStatusPending,
		IdempotencyKey: "business-request-key",
		MerchantID:     merchantID.String(),
		DomainID:       domainID.String(),
		Status:         models.WithdrawalStatusPending,
	}

	var first *models.MoneyEventOutbox
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var created bool
		var err error
		first, created, err = NewMoneyEventOutboxRepo(tx).RecordLifecycleWithDB(ctx, tx, payload)
		if err != nil {
			return err
		}
		if !created {
			return errors.New("first lifecycle event was not created")
		}
		second, created, err := NewMoneyEventOutboxRepo(tx).RecordLifecycleWithDB(ctx, tx, payload)
		if err != nil {
			return err
		}
		if created || second.ID != first.ID {
			return errors.New("duplicate lifecycle event was not idempotent")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.IdempotencyKey != payload.EventID || first.Sequence != 1 {
		t.Fatalf("lifecycle boundary = idempotency:%q sequence:%d", first.IdempotencyKey, first.Sequence)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", payload.EventID, 1)

	rollbackPayload := payload
	rollbackPayload.EventType = constants.WebhookEventPayoutBroadcastV1
	rollbackPayload.EventID = entityID.String() + ":" + rollbackPayload.EventType
	rollbackErr := errors.New("force caller rollback")
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, _, err := NewMoneyEventOutboxRepo(tx).RecordLifecycleWithDB(ctx, tx, rollbackPayload); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("rollback error = %v", err)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", rollbackPayload.EventID, 0)
}

func TestValidateMoneyEventOutboxValidationAndDefaults(t *testing.T) {
	base := testMoneyEventOutbox()
	base.Status = ""
	base.PayloadJSON = `{"b":2,"a":1}`

	if err := validateMoneyEventOutbox(&base); err != nil {
		t.Fatal(err)
	}
	if base.EventVersion != constants.WebhookEventVersionV1 {
		t.Fatalf("event version = %q", base.EventVersion)
	}
	if base.Status != models.MoneyEventOutboxStatusPending {
		t.Fatalf("status = %q", base.Status)
	}
	if base.PayloadJSON != `{"a":1,"b":2}` {
		t.Fatalf("payload = %s", base.PayloadJSON)
	}

	invalid := base
	invalid.EventID = ""
	if err := validateMoneyEventOutbox(&invalid); !errors.Is(err, ErrMoneyEventOutboxInvalid) {
		t.Fatalf("missing event id err = %v", err)
	}

	invalid = base
	invalid.PayloadJSON = `{`
	if err := validateMoneyEventOutbox(&invalid); err == nil {
		t.Fatalf("invalid payload err = %v", err)
	}

	invalid = base
	invalid.PayloadJSON = `"not-an-event-object"`
	if err := validateMoneyEventOutbox(&invalid); !errors.Is(err, ErrMoneyEventOutboxInvalid) {
		t.Fatalf("primitive payload err = %v", err)
	}

	invalid = base
	invalid.PayloadJSON = `["not", "an", "event"]`
	if err := validateMoneyEventOutbox(&invalid); !errors.Is(err, ErrMoneyEventOutboxInvalid) {
		t.Fatalf("array payload err = %v", err)
	}
}

func TestMoneyEventOutboxCompatibility(t *testing.T) {
	existing := testMoneyEventOutbox()
	if err := validateMoneyEventOutbox(&existing); err != nil {
		t.Fatal(err)
	}
	compatible := existing
	compatible.ID = uuid.New()
	compatible.PayloadJSON = `{ "resource_id": "payment_uuid", "event_type": "payment.succeeded.v1" }`
	if err := validateMoneyEventOutbox(&compatible); err != nil {
		t.Fatal(err)
	}
	if !moneyEventOutboxCompatible(&existing, &compatible) {
		t.Fatal("same identity and canonical payload should be compatible")
	}

	conflicting := compatible
	conflicting.EventID = "other:event"
	if moneyEventOutboxCompatible(&existing, &conflicting) {
		t.Fatal("different event id should be incompatible")
	}

	conflicting = compatible
	conflicting.PayloadJSON = `{"resource_id":"payment_uuid","event_type":"payment.failed.v1"}`
	if err := validateMoneyEventOutbox(&conflicting); err != nil {
		t.Fatal(err)
	}
	if moneyEventOutboxCompatible(&existing, &conflicting) {
		t.Fatal("different payload should be incompatible")
	}
}

func TestMoneyEventOutboxRepoPostgresTransactionSemantics(t *testing.T) {
	ctx := context.Background()
	db := openMoneyEventOutboxPostgresTestDB(t)
	prefix := "money-outbox-test-" + uuid.NewString()

	repo := NewMoneyEventOutboxRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()

	commitStateID := uuid.New()
	commitEvent := testMoneyEventOutboxWithIDs(prefix+":event:commit", prefix+":idem:commit", merchantID, domainID)
	commitEvent.AggregateID = commitStateID.String()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&moneyOutboxTestState{ID: commitStateID, Name: prefix + ":commit"}).Error; err != nil {
			return err
		}
		_, created, err := repo.RecordWithDB(ctx, tx, &commitEvent)
		if err != nil {
			return err
		}
		if !created {
			return errors.New("first outbox insert should create row")
		}
		return nil
	}); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	requirePostgresCount(t, db, &moneyOutboxTestState{}, "id = ?", commitStateID, 1)
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", commitEvent.EventID, 1)

	rollbackStateID := uuid.New()
	rollbackEvent := testMoneyEventOutboxWithIDs(prefix+":event:rollback", prefix+":idem:rollback", merchantID, domainID)
	rollbackEvent.AggregateID = rollbackStateID.String()
	errRollback := errors.New("force rollback")
	rollbackErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&moneyOutboxTestState{ID: rollbackStateID, Name: prefix + ":rollback"}).Error; err != nil {
			return err
		}
		if _, _, err := repo.RecordWithDB(ctx, tx, &rollbackEvent); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(rollbackErr, errRollback) {
		t.Fatalf("rollback err = %v", rollbackErr)
	}
	requirePostgresCount(t, db, &moneyOutboxTestState{}, "id = ?", rollbackStateID, 0)
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", rollbackEvent.EventID, 0)

	row, created, err := repo.Record(ctx, &commitEvent)
	if err != nil {
		t.Fatalf("duplicate record: %v", err)
	}
	if created || row.EventID != commitEvent.EventID {
		t.Fatalf("duplicate should no-op and return existing row: created=%v row=%#v", created, row)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "idempotency_key = ?", commitEvent.IdempotencyKey, 1)

	conflict := commitEvent
	conflict.EventID = prefix + ":event:conflict"
	conflict.PayloadJSON = `{"event_type":"payment.failed.v1","resource_id":"payment_uuid"}`
	if _, _, err := repo.Record(ctx, &conflict); !errors.Is(err, ErrMoneyEventOutboxConflict) {
		t.Fatalf("conflicting duplicate err = %v", err)
	}
}

func TestMoneyEventOutboxRepoClaimsBrokenTargetsAndRecoversExpiredLeases(t *testing.T) {
	ctx := context.Background()
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}); err != nil {
		t.Fatalf("automigrate notification target: %v", err)
	}

	merchantID := uuid.New()
	domainID := uuid.New()
	if err := db.Create(&models.Merchant{ID: merchantID, Name: "Outbox Merchant", Email: uuid.NewString() + "@example.test"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Domain{
		ID:               domainID,
		MerchantID:       merchantID,
		DomainURL:        "outbox.example.test",
		APIKey:           uuid.NewString(),
		APISecret:        "encrypted",
		HDAccountID:      uint32(time.Now().UnixNano()),
		NotificationMode: models.DomainNotificationNATS,
		NATSURL:          "nats://127.0.0.1:4222",
	}).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	pending := testMoneyEventOutboxWithIDs("evt-pending", "idem-pending", merchantID, domainID)
	processing := testMoneyEventOutboxWithIDs("evt-processing", "idem-processing", merchantID, domainID)
	processing.Status = models.MoneyEventOutboxStatusProcessing
	processing.LockedUntil = &expired
	notDue := testMoneyEventOutboxWithIDs("evt-future", "idem-future", merchantID, domainID)
	notDue.Status = models.MoneyEventOutboxStatusFailed
	notDue.LockedUntil = &future
	// An absent domain must still be claimed so the relay can persist explicit
	// failure/dead-letter evidence instead of silently leaving the event pending.
	orphaned := testMoneyEventOutboxWithIDs("evt-orphaned-domain", "idem-orphaned-domain", uuid.New(), uuid.New())
	if err := db.Create(&[]models.MoneyEventOutbox{pending, processing, notDue, orphaned}).Error; err != nil {
		t.Fatal(err)
	}

	repo := NewMoneyEventOutboxRepo(db)
	claimed, err := repo.ClaimDueForNotifications(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 3 {
		t.Fatalf("claimed %d rows, want pending, expired processing, and broken target: %#v", len(claimed), claimed)
	}
	claimedIDs := map[uuid.UUID]bool{}
	claimTokens := map[uuid.UUID]uuid.UUID{}
	seenTokens := map[uuid.UUID]bool{}
	for _, row := range claimed {
		claimedIDs[row.ID] = true
		if row.Status != models.MoneyEventOutboxStatusProcessing || row.LockedUntil == nil || !row.LockedUntil.After(now) || row.LeaseToken == nil || *row.LeaseToken == uuid.Nil {
			t.Fatalf("claimed row has no active lease: %#v", row)
		}
		if seenTokens[*row.LeaseToken] {
			t.Fatalf("duplicate claim lease token %s", *row.LeaseToken)
		}
		seenTokens[*row.LeaseToken] = true
		claimTokens[row.ID] = *row.LeaseToken
	}
	if !claimedIDs[pending.ID] || !claimedIDs[processing.ID] || !claimedIDs[orphaned.ID] || claimedIDs[notDue.ID] {
		t.Fatalf("claimed ids = %#v", claimedIDs)
	}
	claimedAgain, err := repo.ClaimDueForNotifications(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("active leases were claimed twice: %#v", claimedAgain)
	}

	if err := repo.MarkRelayAttempt(ctx, pending.ID, claimTokens[pending.ID], true, nil); err != nil {
		t.Fatal(err)
	}
	var delivered models.MoneyEventOutbox
	if err := db.First(&delivered, "id = ?", pending.ID).Error; err != nil {
		t.Fatal(err)
	}
	if delivered.Status != models.MoneyEventOutboxStatusDelivered || delivered.Attempts != 1 || delivered.LockedUntil != nil || delivered.LeaseToken != nil || delivered.LastError != "" {
		t.Fatalf("delivered outbox state = %#v", delivered)
	}

	t.Setenv("MONEY_EVENT_OUTBOX_RETRY_BACKOFF_BASE", "1h")
	if err := repo.MarkRelayAttempt(ctx, processing.ID, claimTokens[processing.ID], false, errors.New("temporary enqueue failure")); err != nil {
		t.Fatal(err)
	}
	var failed models.MoneyEventOutbox
	if err := db.First(&failed, "id = ?", processing.ID).Error; err != nil {
		t.Fatal(err)
	}
	if failed.Status != models.MoneyEventOutboxStatusFailed || failed.Attempts != 1 || failed.LockedUntil == nil || !failed.LockedUntil.After(now) || failed.LeaseToken != nil {
		t.Fatalf("failed outbox state = %#v", failed)
	}
	if err := db.Model(&models.MoneyEventOutbox{}).Where("id = ?", processing.ID).Update("status", models.MoneyEventOutboxStatusDeadLetter).Error; err != nil {
		t.Fatal(err)
	}
	requeued, err := repo.RequeueRelay(ctx, processing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !requeued {
		t.Fatal("dead-lettered relay was not requeued")
	}
	var pendingAgain models.MoneyEventOutbox
	if err := db.First(&pendingAgain, "id = ?", processing.ID).Error; err != nil {
		t.Fatal(err)
	}
	if pendingAgain.Status != models.MoneyEventOutboxStatusPending || pendingAgain.Attempts != 0 || pendingAgain.LockedUntil != nil || pendingAgain.LeaseToken != nil || pendingAgain.LastError != "" {
		t.Fatalf("requeued outbox state = %#v", pendingAgain)
	}
	if requeued, err := repo.RequeueRelay(ctx, pending.ID); err != nil || requeued {
		t.Fatalf("delivered relay requeued=%v err=%v", requeued, err)
	}
}

type moneyOutboxTestState struct {
	ID   uuid.UUID `gorm:"type:uuid;primaryKey"`
	Name string    `gorm:"size:128;index"`
}

func TestMoneyEventOutboxHasAggregateEventMatchesCanonicalAlias(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	event := testMoneyEventOutbox()
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}

	found, err := NewMoneyEventOutboxRepo(db).HasAggregateEvent(context.Background(), "payment", event.AggregateID, constants.WebhookEventPaymentSucceeded)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("canonical payment outbox event did not match its legacy delivery alias")
	}

	found, err = NewMoneyEventOutboxRepo(db).HasAggregateEvent(context.Background(), "payment", event.AggregateID, constants.WebhookEventPaymentFailed)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("different payment lifecycle event matched canonical outbox row")
	}
}

func TestMoneyEventOutboxHasAggregateIgnoresHistoricalEventLabel(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	event := testMoneyEventOutbox()
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}

	found, err := NewMoneyEventOutboxRepo(db).HasAggregate(context.Background(), event.AggregateType, event.AggregateID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("canonical aggregate ownership was not detected")
	}
	found, err = NewMoneyEventOutboxRepo(db).HasAggregate(context.Background(), event.AggregateType, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("unrelated aggregate matched canonical outbox row")
	}
}

func TestMoneyEventOutboxAggregateSequenceBlocksSuccessorUntilPredecessorRelayed(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	repo := NewMoneyEventOutboxRepo(db)
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	first := testMoneyEventOutboxWithIDs("evt-sequence-1", "idem-sequence-1", merchantID, domainID)
	second := testMoneyEventOutboxWithIDs("evt-sequence-2", "idem-sequence-2", merchantID, domainID)
	first.AggregateID = "aggregate-sequence"
	second.AggregateID = first.AggregateID
	if _, created, err := repo.Record(ctx, &first); err != nil || !created {
		t.Fatalf("record first created=%v err=%v", created, err)
	}
	if _, created, err := repo.Record(ctx, &second); err != nil || !created {
		t.Fatalf("record second created=%v err=%v", created, err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("aggregate sequences = %d/%d, want 1/2", first.Sequence, second.Sequence)
	}
	claimed, err := repo.ClaimDueForNotifications(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != first.ID {
		t.Fatalf("claimed before predecessor delivery = %#v, want first only", claimed)
	}
	if claimed[0].LeaseToken == nil {
		t.Fatal("first claim has no lease token")
	}
	if err := repo.MarkRelayAttempt(ctx, first.ID, *claimed[0].LeaseToken, true, nil); err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.ClaimDueForNotifications(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != second.ID {
		t.Fatalf("claimed after predecessor delivery = %#v, want second", claimed)
	}
}

func TestMoneyEventOutboxExpiredLeaseStaleWorkerCannotOverwriteNewClaim(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	repo := NewMoneyEventOutboxRepo(db)
	ctx := context.Background()
	row := testMoneyEventOutboxWithIDs("evt-overlapping-relays", "idem-overlapping-relays", uuid.New(), uuid.New())
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	firstClaim, err := repo.ClaimDueForNotifications(ctx, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstClaim) != 1 || firstClaim[0].LeaseToken == nil {
		t.Fatalf("first claim = %#v", firstClaim)
	}
	firstToken := *firstClaim[0].LeaseToken
	if err := db.Model(&models.MoneyEventOutbox{}).
		Where("id = ?", row.ID).
		Update("locked_until", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}

	secondClaim, err := repo.ClaimDueForNotifications(ctx, 1, time.Minute)
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
		results <- repo.MarkRelayAttempt(ctx, row.ID, firstToken, false, errors.New("stale relay timeout"))
	}()
	go func() {
		defer workers.Done()
		<-start
		results <- repo.MarkRelayAttempt(ctx, row.ID, secondToken, true, nil)
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
		case errors.Is(result, ErrMoneyEventOutboxLeaseLost):
			leaseLost++
		default:
			t.Fatalf("overlapping relay mark result: %v", result)
		}
	}
	if succeeded != 1 || leaseLost != 1 {
		t.Fatalf("overlapping marks succeeded=%d lease_lost=%d, want 1/1", succeeded, leaseLost)
	}

	var got models.MoneyEventOutbox
	if err := db.First(&got, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.MoneyEventOutboxStatusDelivered || got.Attempts != 1 || got.LeaseToken != nil || got.LockedUntil != nil {
		t.Fatalf("authoritative relay claim was overwritten: %#v", got)
	}
}

func TestMoneyEventOutboxRejectsCallerAssignedSequenceForNewEvent(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	event := testMoneyEventOutbox()
	event.Sequence = 77
	if _, _, err := NewMoneyEventOutboxRepo(db).Record(context.Background(), &event); err == nil || !errors.Is(err, ErrMoneyEventOutboxInvalid) {
		t.Fatalf("caller-assigned sequence error = %v, want ErrMoneyEventOutboxInvalid", err)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", event.EventID, 0)
}

func testMoneyEventOutbox() models.MoneyEventOutbox {
	merchantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	domainID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	return testMoneyEventOutboxWithIDs("payment_uuid:payment.succeeded.v1", "payment_uuid:payment.succeeded.v1", merchantID, domainID)
}

func testMoneyEventOutboxWithIDs(eventID, idempotencyKey string, merchantID, domainID uuid.UUID) models.MoneyEventOutbox {
	return models.MoneyEventOutbox{
		ID:             uuid.New(),
		EventID:        eventID,
		EventType:      "payment.succeeded.v1",
		EventVersion:   constants.WebhookEventVersionV1,
		AggregateType:  "payment",
		AggregateID:    "payment_uuid",
		MerchantID:     merchantID,
		DomainID:       domainID,
		IdempotencyKey: idempotencyKey,
		PayloadJSON:    `{"event_type":"payment.succeeded.v1","resource_id":"payment_uuid"}`,
		Status:         models.MoneyEventOutboxStatusPending,
	}
}

func requirePostgresCount(t *testing.T, db *gorm.DB, model any, query string, args any, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(model).Where(query, args).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("count %s = %d, want %d", query, count, want)
	}
}

func openMoneyEventOutboxPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("OUTBOX_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("MONEY_OUTBOX_TEST_DATABASE_URL")
	}
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set OUTBOX_TEST_DATABASE_URL to run Postgres outbox transaction tests")
	}

	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect test postgres: %v", err)
	}
	if err := adminDB.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error; err != nil {
		t.Fatalf("enable uuid extension: %v", err)
	}
	schemaName := "money_outbox_test_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	quotedSchema := quotePostgresIdentifier(schemaName)
	if err := adminDB.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	if err := adminDB.Exec(
		"CREATE FUNCTION " + quotedSchema + ".uuid_generate_v4() RETURNS uuid LANGUAGE SQL VOLATILE PARALLEL SAFE AS 'SELECT public.uuid_generate_v4()'",
	).Error; err != nil {
		t.Fatalf("create schema-local uuid function: %v", err)
	}

	db, err := gorm.Open(postgres.Open(postgresDSNWithSearchPath(dsn, schemaName)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect schema-scoped test postgres: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get test sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(&models.MoneyEventOutbox{}, &moneyOutboxTestState{}); err != nil {
		t.Fatalf("automigrate test schema: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
		_ = adminDB.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error
		if adminSQL, err := adminDB.DB(); err == nil {
			_ = adminSQL.Close()
		}
	})
	return db
}

func postgresDSNWithSearchPath(dsn, schemaName string) string {
	searchPath := schemaName
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err == nil {
			query := parsed.Query()
			query.Set("search_path", searchPath)
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}
	return strings.TrimSpace(dsn) + " search_path=" + searchPath
}

func quotePostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
