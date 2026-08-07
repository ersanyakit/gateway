package repositories

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestMoneyEventInboxProcessWithDBSuppressesDuplicateSuccess(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.MoneyEventInbox{}); err != nil {
		t.Fatalf("automigrate money event inbox: %v", err)
	}
	repo := NewMoneyEventInboxRepo(db)
	ctx := context.Background()
	params := MoneyEventInboxConsumeParams{
		EventID:          "chain-fact-1",
		ConsumerName:     "deposit-fact-processor",
		IdempotencyScope: "deposit-fact-processor:chain-fact-1",
		EventType:        "deposit.observed",
		ResourceType:     "chain_fact",
		ResourceID:       "fact-1",
		LockFor:          time.Minute,
		Evidence:         map[string]any{"tx_hash": "0xabc"},
	}

	calls := 0
	row, processed, err := repo.ProcessWithDB(ctx, params, func(_ *gorm.DB) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("process inbox first attempt: %v", err)
	}
	if !processed || calls != 1 {
		t.Fatalf("first attempt processed=%v calls=%d, want processed once", processed, calls)
	}
	if row == nil || row.Status != models.MoneyEventInboxStatusSucceeded {
		t.Fatalf("first attempt status = %#v, want succeeded row", row)
	}

	row, processed, err = repo.ProcessWithDB(ctx, params, func(_ *gorm.DB) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("process inbox duplicate: %v", err)
	}
	if processed || calls != 1 {
		t.Fatalf("duplicate processed=%v calls=%d, want suppressed duplicate", processed, calls)
	}
	if row == nil || row.Status != models.MoneyEventInboxStatusSucceeded {
		t.Fatalf("duplicate row status = %#v, want succeeded", row)
	}
}

func TestMoneyEventInboxFailureCommitsDeadLetterAndRedacts(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.MoneyEventInbox{}); err != nil {
		t.Fatalf("automigrate money event inbox: %v", err)
	}
	repo := NewMoneyEventInboxRepo(db)
	ctx := context.Background()

	row, processed, err := repo.ProcessWithDB(ctx, MoneyEventInboxConsumeParams{
		EventID:          "chain-fact-dead-letter",
		ConsumerName:     "deposit-fact-processor",
		IdempotencyScope: "deposit-fact-processor:chain-fact-dead-letter",
		EventType:        "deposit.observed",
		ResourceType:     "chain_fact",
		ResourceID:       "fact-dead-letter",
		MaxAttempts:      1,
		LockFor:          time.Minute,
		Evidence:         map[string]any{"address": "0xabc"},
	}, func(_ *gorm.DB) error {
		return errors.New("validation failed secret token should-not-leak")
	})
	if err == nil {
		t.Fatal("process inbox failure err = nil, want original processing error")
	}
	if processed {
		t.Fatal("failed attempt must not be reported as successfully processed")
	}
	if row == nil {
		t.Fatal("failed attempt returned nil row")
	}

	var persisted models.MoneyEventInbox
	if err := db.WithContext(ctx).First(&persisted, "event_id = ?", "chain-fact-dead-letter").Error; err != nil {
		t.Fatalf("reload inbox row: %v", err)
	}
	if persisted.Status != models.MoneyEventInboxStatusDeadLetter {
		t.Fatalf("status = %q, want dead_letter", persisted.Status)
	}
	if !strings.Contains(persisted.LastError, "[redacted]") || strings.Contains(persisted.LastError, "should-not-leak") {
		t.Fatalf("last_error was not redacted: %q", persisted.LastError)
	}
	if persisted.FailureCategory != "validation" {
		t.Fatalf("failure_category = %q, want validation", persisted.FailureCategory)
	}

	attempts, err := repo.AttemptCountByStatus(ctx, models.MoneyEventInboxStatusDeadLetter)
	if err != nil {
		t.Fatalf("attempt count: %v", err)
	}
	if attempts[models.MoneyEventInboxStatusDeadLetter] != 1 {
		t.Fatalf("dead-letter attempts = %d, want 1", attempts[models.MoneyEventInboxStatusDeadLetter])
	}
}

func TestCallMoneyEventInboxHandlerContainsPanicWithoutPayloadLeak(t *testing.T) {
	err := callMoneyEventInboxHandler(nil, func(*gorm.DB) error {
		panic("sensitive panic payload")
	})
	if !errors.Is(err, ErrMoneyEventInboxHandlerPanic) {
		t.Fatalf("error = %v, want ErrMoneyEventInboxHandlerPanic", err)
	}
	if strings.Contains(err.Error(), "sensitive panic payload") {
		t.Fatalf("panic payload leaked through error: %v", err)
	}
}

func TestMoneyEventInboxFailureRollsBackPartialBusinessWrites(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.MoneyEventInbox{}); err != nil {
		t.Fatalf("automigrate money event inbox: %v", err)
	}
	repo := NewMoneyEventInboxRepo(db)
	ctx := context.Background()
	stateID := uuid.New()

	row, processed, err := repo.ProcessWithDB(ctx, MoneyEventInboxConsumeParams{
		EventID:      "chain-fact-rollback",
		ConsumerName: "deposit-fact-processor",
		EventType:    "deposit.observed",
		ResourceType: "chain_fact",
		ResourceID:   "fact-rollback",
		MaxAttempts:  1,
	}, func(tx *gorm.DB) error {
		if err := tx.Create(&moneyOutboxTestState{ID: stateID, Name: "must-roll-back"}).Error; err != nil {
			return err
		}
		return errors.New("business write failed")
	})
	if err == nil || processed {
		t.Fatalf("err=%v processed=%v, want failed attempt", err, processed)
	}
	if row == nil || row.Status != models.MoneyEventInboxStatusDeadLetter {
		t.Fatalf("inbox row = %#v, want committed dead letter", row)
	}
	requirePostgresCount(t, db, &moneyOutboxTestState{}, "id = ?", stateID, 0)
	requirePostgresCount(t, db, &models.MoneyEventInbox{}, "event_id = ? AND status = 'dead_letter'", "chain-fact-rollback", 1)
}

func TestMoneyEventInboxContainsHandlerPanicAndRollsBack(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.MoneyEventInbox{}); err != nil {
		t.Fatalf("automigrate money event inbox: %v", err)
	}
	repo := NewMoneyEventInboxRepo(db)
	ctx := context.Background()
	stateID := uuid.New()

	row, processed, err := repo.ProcessWithDB(ctx, MoneyEventInboxConsumeParams{
		EventID:      "chain-fact-panic",
		ConsumerName: "deposit-fact-processor",
		EventType:    "deposit.observed",
		ResourceType: "chain_fact",
		ResourceID:   "fact-panic",
		MaxAttempts:  1,
	}, func(tx *gorm.DB) error {
		if err := tx.Create(&moneyOutboxTestState{ID: stateID, Name: "panic-roll-back"}).Error; err != nil {
			return err
		}
		panic("sensitive panic payload")
	})
	if !errors.Is(err, ErrMoneyEventInboxHandlerPanic) || processed {
		t.Fatalf("err=%v processed=%v, want contained handler panic", err, processed)
	}
	if strings.Contains(err.Error(), "sensitive panic payload") {
		t.Fatalf("panic payload leaked through error: %v", err)
	}
	if row == nil || row.Status != models.MoneyEventInboxStatusDeadLetter {
		t.Fatalf("inbox row = %#v, want committed dead letter", row)
	}
	requirePostgresCount(t, db, &moneyOutboxTestState{}, "id = ?", stateID, 0)
}
