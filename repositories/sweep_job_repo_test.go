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
	"unicode/utf8"

	"core/constants"
	"core/models"
	webhooksvc "core/services/webhook"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestSweepJobRepoEnqueueForTransactionIsIdempotent(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	txModel := sweepJobTestTransaction("1-0xidem-log:0", uuid.New(), uuid.New(), constants.Ethereum)

	first, created, err := repo.EnqueueForTransaction(ctx, txModel)
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if !created {
		t.Fatal("first enqueue should create a job")
	}

	again, created, err := repo.EnqueueForTransaction(ctx, txModel)
	if err != nil {
		t.Fatalf("duplicate enqueue: %v", err)
	}
	if created || again.ID != first.ID {
		t.Fatalf("duplicate enqueue should return existing job, created=%v first=%s again=%s", created, first.ID, again.ID)
	}

	var count int64
	if err := db.WithContext(ctx).Model(&models.SweepJob{}).Where("transaction_unique_hash = ?", txModel.UniqueHash).Count(&count).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("job count = %d, want 1", count)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", first.ID.String()+":"+constants.WebhookEventSweepRequestedV1, 1)
}

func TestSweepJobRepoRevivesReorgDeadLetterOnCanonicalTransactionReappearance(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Transaction{}, &models.SweepJob{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	txModel := sweepJobTestTransaction("1-0xreappeared-log:0", uuid.New(), uuid.New(), constants.Ethereum)
	txModel.Status = models.TransactionStatusConfirmed
	txModel.FinalizedAt = &now
	if err := db.Create(&txModel).Error; err != nil {
		t.Fatal(err)
	}
	job, created, err := NewSweepJobRepo(db).EnqueueForTransaction(context.Background(), txModel)
	if err != nil || !created {
		t.Fatalf("initial enqueue created=%v err=%v", created, err)
	}
	if err := db.Model(&models.SweepJob{}).Where("id = ?", job.ID).Updates(map[string]any{
		"status": models.SweepJobStatusDeadLetter, "last_error": "source transaction was reorged", "attempts": 4,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Transaction{}).Where("id = ?", txModel.ID).Updates(map[string]any{
		"status": models.TransactionStatusReorged, "reorged_at": &now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	stale, queued, err := NewSweepJobRepo(db).EnqueueForTransaction(context.Background(), txModel)
	if err != nil {
		t.Fatal(err)
	}
	if queued || stale.Status != models.SweepJobStatusDeadLetter {
		t.Fatalf("stale confirmed snapshot revived reorged source: queued=%v job=%#v", queued, stale)
	}
	if err := db.Model(&models.Transaction{}).Where("id = ?", txModel.ID).Updates(map[string]any{
		"status": models.TransactionStatusConfirmed, "finalized_at": &now, "reorged_at": nil,
	}).Error; err != nil {
		t.Fatal(err)
	}
	revived, queued, err := NewSweepJobRepo(db).EnqueueForTransaction(context.Background(), txModel)
	if err != nil {
		t.Fatal(err)
	}
	if !queued || revived.Status != models.SweepJobStatusPending || revived.Attempts != 0 || revived.LastError != "" || revived.NextRunAt == nil {
		t.Fatalf("revived sweep = queued:%v job:%#v", queued, revived)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", job.ID.String()+":"+constants.WebhookEventSweepRequestedV1, 1)
}

func TestSweepJobRepoRollsBackJobWhenRequestedOutboxCannotPersist(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&models.MoneyEventOutbox{}); err != nil {
		t.Fatal(err)
	}
	txModel := sweepJobTestTransaction("1-0xoutbox-down-log:0", uuid.New(), uuid.New(), constants.Ethereum)
	if _, _, err := NewSweepJobRepo(db).EnqueueForTransaction(context.Background(), txModel); err == nil {
		t.Fatal("enqueue succeeded without lifecycle outbox table")
	}
	requirePostgresCount(t, db, &models.SweepJob{}, "transaction_unique_hash = ?", txModel.UniqueHash, 0)
}

func TestSweepJobRepoLifecycleTransitionsPersistCanonicalOutboxSnapshots(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		db := openMoneyEventOutboxPostgresTestDB(t)
		repo, _, job := seedSweepLifecycleJob(t, db, "lifecycle-success")
		markSweepJobProcessing(t, db, job.ID)
		if err := repo.MarkSucceeded(context.Background(), job.ID, "0xsweep-success"); err != nil {
			t.Fatal(err)
		}
		assertSweepLifecycleEvents(t, db, job.ID,
			constants.WebhookEventSweepRequestedV1,
			constants.WebhookEventSweepSucceededV1,
		)
		assertSweepLifecyclePayload(t, db, job.ID, constants.WebhookEventSweepSucceededV1, models.SweepJobStatusSucceeded, "0xsweep-success")
	})

	t.Run("retry then dead letter", func(t *testing.T) {
		db := openMoneyEventOutboxPostgresTestDB(t)
		repo, _, job := seedSweepLifecycleJob(t, db, "lifecycle-failed")
		job.MaxAttempts = 2
		if err := db.Model(&models.SweepJob{}).Where("id = ?", job.ID).Update("max_attempts", 2).Error; err != nil {
			t.Fatal(err)
		}
		markSweepJobProcessing(t, db, job.ID)
		if err := repo.MarkFailed(context.Background(), job.ID, errors.New("rpc unavailable")); err != nil {
			t.Fatal(err)
		}
		if err := repo.MarkFailed(context.Background(), job.ID, context.DeadlineExceeded); err != nil {
			t.Fatal(err)
		}
		assertSweepLifecycleEvents(t, db, job.ID,
			constants.WebhookEventSweepRequestedV1,
			constants.WebhookEventSweepFailedV1,
			constants.WebhookEventSweepDeadLetteredV1,
		)
		assertSweepLifecyclePayload(t, db, job.ID, constants.WebhookEventSweepDeadLetteredV1, models.SweepJobStatusDeadLetter, "")
	})
}

func TestSweepJobRepoLifecycleTransitionsRollbackWithOutbox(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		db := openMoneyEventOutboxPostgresTestDB(t)
		repo, _, job := seedSweepLifecycleJob(t, db, "rollback-success")
		markSweepJobProcessing(t, db, job.ID)
		if err := db.Migrator().DropTable(&models.MoneyEventOutbox{}); err != nil {
			t.Fatal(err)
		}
		if err := repo.MarkSucceeded(context.Background(), job.ID, "0xmust-rollback"); err == nil {
			t.Fatal("success transition committed without lifecycle outbox")
		}
		reloaded, err := repo.Find(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Status != models.SweepJobStatusProcessing || reloaded.SweepTxHash != "" {
			t.Fatalf("success state escaped rolled-back transaction: %#v", reloaded)
		}
	})

	t.Run("dead letter", func(t *testing.T) {
		db := openMoneyEventOutboxPostgresTestDB(t)
		repo, _, job := seedSweepLifecycleJob(t, db, "rollback-dead-letter")
		if err := db.Model(&models.SweepJob{}).Where("id = ?", job.ID).Update("max_attempts", 1).Error; err != nil {
			t.Fatal(err)
		}
		markSweepJobProcessing(t, db, job.ID)
		if err := db.Migrator().DropTable(&models.MoneyEventOutbox{}); err != nil {
			t.Fatal(err)
		}
		if err := repo.MarkFailed(context.Background(), job.ID, errors.New("wallet not found")); err == nil {
			t.Fatal("dead-letter transition committed without lifecycle outbox")
		}
		reloaded, err := repo.Find(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Status != models.SweepJobStatusProcessing || reloaded.Attempts != 0 || reloaded.LastError != "" {
			t.Fatalf("dead-letter state escaped rolled-back transaction: %#v", reloaded)
		}
	})

	t.Run("operator recovery", func(t *testing.T) {
		db := openMoneyEventOutboxPostgresTestDB(t)
		repo, _, job := seedSweepLifecycleJob(t, db, "rollback-operator-recovery")
		markSweepJobProcessing(t, db, job.ID)
		if err := repo.MarkNeedsOperatorAction(context.Background(), job.ID, models.SweepOperatorActionReconcileBroadcast, errors.New("broadcast uncertain")); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrator().DropTable(&models.MoneyEventOutbox{}); err != nil {
			t.Fatal(err)
		}
		if err := repo.RecordOperatorRecovery(context.Background(), job.ID, models.SweepRecoveryActionMarkSuccess, "replacement confirmed", "0xreplacement", nil); err == nil {
			t.Fatal("operator recovery committed without lifecycle outbox")
		}
		reloaded, err := repo.Find(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Status != models.SweepJobStatusDeadLetter || reloaded.RecoveredAt != nil || reloaded.SweepTxHash != "" {
			t.Fatalf("operator recovery escaped rolled-back transaction: %#v", reloaded)
		}
	})
}

func TestSweepJobSourceReorgFenceCoversEveryRunnableStateAndEmitsLifecycle(t *testing.T) {
	for _, tc := range []struct {
		status         string
		revivable      bool
		reason         string
		operatorAction string
	}{
		{status: models.SweepJobStatusPending, revivable: true, reason: sweepSourceReorgRevivalReason},
		{status: models.SweepJobStatusFailed, revivable: true, reason: sweepSourceReorgRevivalReason},
		{status: models.SweepJobStatusProcessing, reason: sweepSourceReorgProcessingReason, operatorAction: models.SweepOperatorActionReconcileBroadcast},
		{status: models.SweepJobStatusSucceeded, reason: sweepSourceReorgSucceededReason, operatorAction: models.SweepOperatorActionReconcileBroadcast},
	} {
		t.Run(tc.status, func(t *testing.T) {
			db := openMoneyEventOutboxPostgresTestDB(t)
			repo, txModel, job := seedSweepLifecycleJob(t, db, "source-reorg-fence-"+tc.status)
			now := time.Now().UTC()
			updates := map[string]any{
				"status":      tc.status,
				"next_run_at": &now,
			}
			if tc.status == models.SweepJobStatusProcessing {
				lockedUntil := now.Add(time.Minute)
				updates["locked_until"] = &lockedUntil
			}
			if tc.status == models.SweepJobStatusFailed {
				updates["failure_category"] = models.SweepFailureCategoryTransient
			}
			if tc.status == models.SweepJobStatusSucceeded {
				updates["sweep_tx_hash"] = "0xalready-broadcast"
			}
			if err := db.Model(&models.SweepJob{}).Where("id = ?", job.ID).Updates(updates).Error; err != nil {
				t.Fatal(err)
			}

			if err := db.Transaction(func(tx *gorm.DB) error {
				return fenceSweepJobsForSourceReorgWithDB(context.Background(), tx, txModel)
			}); err != nil {
				t.Fatalf("fence %s sweep: %v", tc.status, err)
			}
			fenced, err := repo.Find(context.Background(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fenced.Status != models.SweepJobStatusDeadLetter || fenced.LastError != tc.reason || fenced.OperatorAction != tc.operatorAction || fenced.NextRunAt != nil || fenced.LockedUntil != nil {
				t.Fatalf("fenced %s sweep = %#v", tc.status, fenced)
			}
			if tc.operatorAction != "" && fenced.FailureCategory != models.SweepFailureCategoryBroadcastUncertain {
				t.Fatalf("fenced %s category = %q", tc.status, fenced.FailureCategory)
			}
			if tc.status == models.SweepJobStatusSucceeded && fenced.SweepTxHash != "0xalready-broadcast" {
				t.Fatalf("succeeded fence lost broadcast hash: %#v", fenced)
			}
			assertSweepLifecycleEvents(t, db, job.ID,
				constants.WebhookEventSweepRequestedV1,
				constants.WebhookEventSweepDeadLetteredV1,
			)

			afterEnqueue, queued, err := repo.EnqueueForTransaction(context.Background(), txModel)
			if err != nil {
				t.Fatal(err)
			}
			if queued != tc.revivable {
				t.Fatalf("%s revival queued = %v, want %v", tc.status, queued, tc.revivable)
			}
			if tc.revivable {
				if afterEnqueue.Status != models.SweepJobStatusPending {
					t.Fatalf("revived %s sweep = %#v", tc.status, afterEnqueue)
				}
				assertSweepLifecycleEvents(t, db, job.ID,
					constants.WebhookEventSweepRequestedV1,
					constants.WebhookEventSweepDeadLetteredV1,
					constants.WebhookEventSweepRequestedV1,
				)
			} else if afterEnqueue.Status != models.SweepJobStatusDeadLetter {
				t.Fatalf("unsafe %s sweep was revived: %#v", tc.status, afterEnqueue)
			}
		})
	}
}

func TestSweepJobSourceReorgFenceRollsBackWithoutLifecycleOutbox(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	repo, txModel, job := seedSweepLifecycleJob(t, db, "source-reorg-fence-rollback")
	if err := db.Migrator().DropTable(&models.MoneyEventOutbox{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return fenceSweepJobsForSourceReorgWithDB(context.Background(), tx, txModel)
	}); err == nil {
		t.Fatal("source reorg fence committed without lifecycle outbox")
	}
	reloaded, err := repo.Find(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != models.SweepJobStatusPending || reloaded.LastError != "" {
		t.Fatalf("source reorg fence escaped rollback: %#v", reloaded)
	}
}

func TestSweepJobRepoOperatorRecoveryIsAtomicConcurrentAndIdempotent(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	repo, _, job := seedSweepLifecycleJob(t, db, "operator-recovery-concurrent")
	markSweepJobProcessing(t, db, job.ID)
	if err := repo.MarkNeedsOperatorAction(context.Background(), job.ID, models.SweepOperatorActionReconcileBroadcast, errors.New("broadcast uncertain")); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(8)

	const workers = 8
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- repo.RecordOperatorRecovery(context.Background(), job.ID, models.SweepRecoveryActionMarkSuccess, "replacement confirmed", "0xreplacement", nil)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("idempotent concurrent recovery: %v", err)
		}
	}
	reloaded, err := repo.Find(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != models.SweepJobStatusSucceeded || reloaded.SweepTxHash != "0xreplacement" || reloaded.RecoveryAction != models.SweepRecoveryActionMarkSuccess {
		t.Fatalf("recovered job = %#v", reloaded)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", job.ID.String()+":"+constants.WebhookEventSweepSucceededV1, 1)

	_, _, competingJob := seedSweepLifecycleJob(t, db, "operator-recovery-competing")
	markSweepJobProcessing(t, db, competingJob.ID)
	if err := repo.MarkNeedsOperatorAction(context.Background(), competingJob.ID, models.SweepOperatorActionReconcileBroadcast, errors.New("manual reconciliation required")); err != nil {
		t.Fatal(err)
	}
	retryAt := time.Now().UTC().Add(time.Minute)
	start = make(chan struct{})
	results = make(chan error, 2)
	for _, call := range []func() error{
		func() error {
			return repo.RecordOperatorRecovery(context.Background(), competingJob.ID, models.SweepRecoveryActionRetry, "operator chose retry", "", &retryAt)
		},
		func() error {
			return repo.RecordOperatorRecovery(context.Background(), competingJob.ID, models.SweepRecoveryActionMarkSuccess, "operator found replacement", "0xcompeting", nil)
		},
	} {
		wg.Add(1)
		go func(call func() error) {
			defer wg.Done()
			<-start
			results <- call()
		}(call)
	}
	close(start)
	wg.Wait()
	close(results)
	succeededCalls, conflictedCalls := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeededCalls++
		case errors.Is(err, ErrSweepJobStateConflict):
			conflictedCalls++
		default:
			t.Fatalf("competing recovery: %v", err)
		}
	}
	if succeededCalls != 1 || conflictedCalls != 1 {
		t.Fatalf("competing recoveries = success:%d conflict:%d, want 1/1", succeededCalls, conflictedCalls)
	}
	var recoveryEvents int64
	if err := db.Model(&models.MoneyEventOutbox{}).
		Where("aggregate_type = ? AND aggregate_id = ?", webhooksvc.EntityTypeSweep, competingJob.ID.String()).
		Where("event_type IN ?", []string{constants.WebhookEventSweepFailedV1, constants.WebhookEventSweepSucceededV1}).
		Count(&recoveryEvents).Error; err != nil {
		t.Fatal(err)
	}
	if recoveryEvents != 1 {
		t.Fatalf("competing recovery outbox events = %d, want exactly one", recoveryEvents)
	}
}

func TestSweepJobRepoDefersForNetworkStateWithoutConsumingAttempt(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC()
	due := now.Add(-time.Second)
	job := sweepJobTestJob("maintenance-defer", uuid.New(), uuid.New(), constants.Ethereum, models.SweepJobStatusPending, now, &due, nil)
	if err := db.WithContext(ctx).Create(&job).Error; err != nil {
		t.Fatalf("seed sweep job: %v", err)
	}
	claimed, err := repo.ClaimDue(ctx, 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim due = %#v, err=%v", claimed, err)
	}
	if err := repo.DeferForNetworkState(ctx, job.ID, "node upgrade", time.Minute); err != nil {
		t.Fatalf("defer for network state: %v", err)
	}

	var reloaded models.SweepJob
	if err := db.WithContext(ctx).First(&reloaded, "id = ?", job.ID).Error; err != nil {
		t.Fatalf("reload sweep job: %v", err)
	}
	if reloaded.Status != models.SweepJobStatusPending || reloaded.Attempts != 0 || reloaded.LockedUntil != nil || reloaded.NextRunAt == nil || !reloaded.NextRunAt.After(time.Now()) {
		t.Fatalf("deferred sweep state = %#v", reloaded)
	}
	if reloaded.FailureCategory != models.SweepFailureCategoryNetworkMaintenance || reloaded.LastError != "node upgrade" {
		t.Fatalf("deferred sweep metadata = %q/%q", reloaded.FailureCategory, reloaded.LastError)
	}
}

func TestSweepJobRepoClaimDueRecoversStaleProcessingAndSerializesWalletChain(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	walletID := uuid.New()
	merchantID := uuid.New()
	staleLock := now.Add(-time.Minute)
	due := now.Add(-time.Second)
	jobs := []models.SweepJob{
		sweepJobTestJob("stale-processing", walletID, merchantID, constants.Ethereum, models.SweepJobStatusProcessing, now.Add(-3*time.Minute), &due, &staleLock),
		sweepJobTestJob("same-wallet-pending", walletID, merchantID, constants.Ethereum, models.SweepJobStatusPending, now.Add(-2*time.Minute), &due, nil),
		sweepJobTestJob("other-chain-pending", walletID, merchantID, constants.TRON, models.SweepJobStatusPending, now.Add(-time.Minute), &due, nil),
	}
	if err := db.WithContext(ctx).Create(&jobs).Error; err != nil {
		t.Fatalf("seed sweep jobs: %v", err)
	}

	claimed, err := repo.ClaimDue(ctx, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed = %d, want stale processing plus other chain", len(claimed))
	}
	claimedByHash := map[string]bool{}
	for _, job := range claimed {
		claimedByHash[job.TransactionUniqueHash] = true
	}
	if !claimedByHash["stale-processing"] || !claimedByHash["other-chain-pending"] || claimedByHash["same-wallet-pending"] {
		t.Fatalf("claimed hashes = %#v", claimedByHash)
	}

	var sameWalletPending models.SweepJob
	if err := db.WithContext(ctx).First(&sameWalletPending, "transaction_unique_hash = ?", "same-wallet-pending").Error; err != nil {
		t.Fatalf("reload same wallet pending: %v", err)
	}
	if sameWalletPending.Status != models.SweepJobStatusPending {
		t.Fatalf("same wallet pending status = %q, want pending", sameWalletPending.Status)
	}

	for hash := range claimedByHash {
		var reloaded models.SweepJob
		if err := db.WithContext(ctx).First(&reloaded, "transaction_unique_hash = ?", hash).Error; err != nil {
			t.Fatalf("reload claimed %s: %v", hash, err)
		}
		if reloaded.Status != models.SweepJobStatusProcessing || reloaded.LockedUntil == nil || !reloaded.LockedUntil.After(time.Now()) {
			t.Fatalf("claimed job state = %#v", reloaded)
		}
	}
}

func TestSweepJobRepoClaimDueRecoversStaleProcessingWithNilNextRun(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	staleLock := now.Add(-time.Minute)
	job := sweepJobTestJob("stale-processing-nil-next", uuid.New(), uuid.New(), constants.Ethereum, models.SweepJobStatusProcessing, now.Add(-time.Minute), nil, &staleLock)
	if err := db.WithContext(ctx).Create(&job).Error; err != nil {
		t.Fatalf("seed sweep job: %v", err)
	}

	claimed, err := repo.ClaimDue(ctx, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if len(claimed) != 1 || claimed[0].TransactionUniqueHash != job.TransactionUniqueHash {
		t.Fatalf("claimed = %#v, want stale processing with nil next_run_at", claimed)
	}
}

func TestSweepJobRepoActiveProcessingSuppressesSameWalletChain(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	walletID := uuid.New()
	merchantID := uuid.New()
	due := now.Add(-time.Second)
	activeLock := now.Add(5 * time.Minute)
	jobs := []models.SweepJob{
		sweepJobTestJob("active-processing", walletID, merchantID, constants.Ethereum, models.SweepJobStatusProcessing, now.Add(-3*time.Minute), &due, &activeLock),
		sweepJobTestJob("blocked-pending", walletID, merchantID, constants.Ethereum, models.SweepJobStatusPending, now.Add(-2*time.Minute), &due, nil),
		sweepJobTestJob("other-wallet-pending", uuid.New(), merchantID, constants.Ethereum, models.SweepJobStatusPending, now.Add(-time.Minute), &due, nil),
	}
	if err := db.WithContext(ctx).Create(&jobs).Error; err != nil {
		t.Fatalf("seed sweep jobs: %v", err)
	}

	claimed, err := repo.ClaimDue(ctx, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if len(claimed) != 1 || claimed[0].TransactionUniqueHash != "other-wallet-pending" {
		t.Fatalf("claimed = %#v, want only other wallet pending", claimed)
	}
}

func TestSweepJobRepoRetryDeadLetterAndFailureCategory(t *testing.T) {
	t.Setenv("SWEEP_RETRY_BACKOFF_BASE", "1s")
	t.Setenv("SWEEP_RETRY_BACKOFF_MAX", "1m")
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	job := sweepJobTestJob("retry-classified", uuid.New(), uuid.New(), constants.Ethereum, models.SweepJobStatusProcessing, now, nil, nil)
	job.MaxAttempts = 2
	if err := db.WithContext(ctx).Create(&job).Error; err != nil {
		t.Fatalf("seed sweep job: %v", err)
	}

	if err := repo.MarkFailed(ctx, job.ID, errors.New("rpc unavailable: "+strings.Repeat("x", 700))); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	first, err := repo.Find(ctx, job.ID)
	if err != nil {
		t.Fatalf("find failed job: %v", err)
	}
	if first.Status != models.SweepJobStatusFailed || first.Attempts != 1 || first.NextRunAt == nil || first.LockedUntil != nil {
		t.Fatalf("first failure state = %#v", first)
	}
	if first.FailureCategory != models.SweepFailureCategoryTransient {
		t.Fatalf("failure category = %q, want transient", first.FailureCategory)
	}
	if len(first.LastError) > models.SweepJobErrorMaxLength {
		t.Fatalf("last error length = %d, want bounded", len(first.LastError))
	}

	if err := repo.MarkFailed(ctx, job.ID, contextDeadlineExceeded()); err != nil {
		t.Fatalf("mark dead-letter: %v", err)
	}
	dead, err := repo.Find(ctx, job.ID)
	if err != nil {
		t.Fatalf("find dead-letter job: %v", err)
	}
	if dead.Status != models.SweepJobStatusDeadLetter || dead.Attempts != 2 || dead.NextRunAt != nil || dead.LockedUntil != nil {
		t.Fatalf("dead-letter state = %#v", dead)
	}
	if dead.FailureCategory != models.SweepFailureCategoryTimeout {
		t.Fatalf("dead-letter category = %q, want timeout", dead.FailureCategory)
	}
}

func TestSweepJobRepoPolicyFailureDeadLettersImmediately(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	lockUntil := now.Add(time.Minute)
	job := sweepJobTestJob("policy-terminal", uuid.New(), uuid.New(), constants.Ethereum, models.SweepJobStatusProcessing, now, nil, &lockUntil)
	job.MaxAttempts = 12
	if err := db.WithContext(ctx).Create(&job).Error; err != nil {
		t.Fatalf("seed sweep job: %v", err)
	}

	if err := repo.MarkFailed(ctx, job.ID, errors.New("wallet not found")); err != nil {
		t.Fatalf("mark policy failed: %v", err)
	}
	dead, err := repo.Find(ctx, job.ID)
	if err != nil {
		t.Fatalf("find dead-letter job: %v", err)
	}
	if dead.Status != models.SweepJobStatusDeadLetter || dead.Attempts != 1 || dead.NextRunAt != nil || dead.LockedUntil != nil {
		t.Fatalf("policy failure state = %#v", dead)
	}
	if dead.FailureCategory != models.SweepFailureCategoryPolicy {
		t.Fatalf("policy category = %q", dead.FailureCategory)
	}
}

func TestSweepJobRepoBroadcastUncertainIsTerminalAndClassified(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	next := now.Add(time.Minute)
	locked := now.Add(time.Minute)
	job := sweepJobTestJob("broadcast-uncertain", uuid.New(), uuid.New(), constants.Ethereum, models.SweepJobStatusProcessing, now, &next, &locked)
	if err := db.WithContext(ctx).Create(&job).Error; err != nil {
		t.Fatalf("seed sweep job: %v", err)
	}

	if err := repo.MarkBroadcastUncertain(ctx, job.ID, errors.New("sendTransaction timeout")); err != nil {
		t.Fatalf("mark broadcast uncertain: %v", err)
	}
	updated, err := repo.Find(ctx, job.ID)
	if err != nil {
		t.Fatalf("find broadcast uncertain: %v", err)
	}
	if updated.Status != models.SweepJobStatusDeadLetter || updated.Attempts != 1 || updated.NextRunAt != nil || updated.LockedUntil != nil {
		t.Fatalf("broadcast uncertain state = %#v", updated)
	}
	if updated.FailureCategory != models.SweepFailureCategoryBroadcastUncertain {
		t.Fatalf("failure category = %q, want broadcast_uncertain", updated.FailureCategory)
	}
	if updated.OperatorAction != models.SweepOperatorActionReconcileBroadcast {
		t.Fatalf("operator action = %q, want reconcile broadcast", updated.OperatorAction)
	}
}

func TestSweepJobRepoSuccessAndPrefundPersistence(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	job := sweepJobTestJob("success-prefund", uuid.New(), uuid.New(), constants.Ethereum, models.SweepJobStatusProcessing, now, nil, nil)
	if err := db.WithContext(ctx).Create(&job).Error; err != nil {
		t.Fatalf("seed sweep job: %v", err)
	}

	if err := repo.MarkPrefundFailed(ctx, job.ID, errors.New("gas balance below threshold")); err != nil {
		t.Fatalf("mark prefund failed: %v", err)
	}
	afterPrefundFail, err := repo.Find(ctx, job.ID)
	if err != nil {
		t.Fatalf("find prefund failed: %v", err)
	}
	if afterPrefundFail.PrefundAttempts != 1 || afterPrefundFail.PrefundLastError == "" || afterPrefundFail.PrefundFailureCategory == "" || afterPrefundFail.PrefundLastAttemptAt == nil {
		t.Fatalf("prefund failure state = %#v", afterPrefundFail)
	}
	if err := repo.MarkPrefunded(ctx, job.ID); err != nil {
		t.Fatalf("mark prefunded: %v", err)
	}
	afterPrefunded, err := repo.Find(ctx, job.ID)
	if err != nil {
		t.Fatalf("find prefunded: %v", err)
	}
	if afterPrefunded.PrefundAttempts != 2 || afterPrefunded.PrefundLastError != "" || afterPrefunded.PrefundFailureCategory != "" || afterPrefunded.PrefundedAt == nil || afterPrefunded.PrefundLastAttemptAt == nil {
		t.Fatalf("prefunded state = %#v", afterPrefunded)
	}

	if err := repo.MarkSucceeded(ctx, job.ID, " 0xsweep "); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}
	succeeded, err := repo.Find(ctx, job.ID)
	if err != nil {
		t.Fatalf("find succeeded: %v", err)
	}
	if succeeded.Status != models.SweepJobStatusSucceeded || succeeded.SweepTxHash != "0xsweep" || succeeded.NextRunAt != nil || succeeded.LockedUntil != nil || succeeded.LastError != "" || succeeded.FailureCategory != "" {
		t.Fatalf("succeeded state = %#v", succeeded)
	}
}

func TestSweepJobRepoPrefundReservationIsIdempotentAndLimitAware(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	job := sweepJobTestJob("prefund-reserve", uuid.New(), uuid.New(), constants.Ethereum, models.SweepJobStatusProcessing, now, nil, nil)
	job.PrefundMaxAttempts = 1
	if err := db.WithContext(ctx).Create(&job).Error; err != nil {
		t.Fatalf("seed sweep job: %v", err)
	}

	ok, err := repo.ReservePrefundAttempt(ctx, job.ID, time.Hour, 1)
	if err != nil {
		t.Fatalf("reserve prefund: %v", err)
	}
	if !ok {
		t.Fatal("first prefund reservation should be allowed")
	}
	again, err := repo.ReservePrefundAttempt(ctx, job.ID, time.Hour, 1)
	if err != nil {
		t.Fatalf("reserve prefund again: %v", err)
	}
	if again {
		t.Fatal("second prefund reservation in retry window should be suppressed")
	}
	if err := repo.MarkPrefundFailed(ctx, job.ID, errors.New("gas balance below threshold")); err != nil {
		t.Fatalf("mark prefund failed: %v", err)
	}
	afterFailure, err := repo.Find(ctx, job.ID)
	if err != nil {
		t.Fatalf("find after prefund failure: %v", err)
	}
	if afterFailure.PrefundAttempts != 1 || afterFailure.OperatorAction != models.SweepOperatorActionReviewGasFunding {
		t.Fatalf("prefund failure limit state = %#v", afterFailure)
	}

	oldAttempt := now.Add(-2 * time.Hour)
	if err := db.WithContext(ctx).Model(&models.SweepJob{}).Where("id = ?", job.ID).Update("prefund_last_attempt_at", &oldAttempt).Error; err != nil {
		t.Fatalf("age prefund attempt: %v", err)
	}
	afterLimit, err := repo.ReservePrefundAttempt(ctx, job.ID, time.Hour, 1)
	if err != nil {
		t.Fatalf("reserve after max attempts: %v", err)
	}
	if afterLimit {
		t.Fatal("prefund reservation should stop at max attempts")
	}
}

func TestSweepJobRepoAssignBatchPersistsPlanMetadata(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := sweepJobTestJob("batch-first", uuid.New(), uuid.New(), constants.Bitcoin, models.SweepJobStatusPending, now, nil, nil)
	second := sweepJobTestJob("batch-second", uuid.New(), first.MerchantID, constants.Bitcoin, models.SweepJobStatusFailed, now.Add(time.Second), nil, nil)
	if err := db.WithContext(ctx).Create(&[]models.SweepJob{first, second}).Error; err != nil {
		t.Fatalf("seed sweep jobs: %v", err)
	}
	batchID := uuid.New()
	if err := repo.AssignBatch(ctx, batchID, "merchant:bitcoin:native", "treasury", []uuid.UUID{first.ID, second.ID}); err != nil {
		t.Fatalf("assign batch: %v", err)
	}

	var rows []models.SweepJob
	if err := db.WithContext(ctx).Order("batch_ordinal ASC").Find(&rows, "batch_id = ?", batchID).Error; err != nil {
		t.Fatalf("load batch rows: %v", err)
	}
	if len(rows) != 2 || rows[0].BatchOrdinal != 1 || rows[1].BatchOrdinal != 2 || rows[0].BatchSize != 2 || rows[1].BatchPolicy != "treasury" {
		t.Fatalf("batch rows = %#v", rows)
	}
}

func TestSweepJobRepoOperatorRecoveryActions(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	ctx := context.Background()
	repo, _, job := seedSweepLifecycleJob(t, db, "operator-recovery")
	markSweepJobProcessing(t, db, job.ID)
	if err := repo.MarkNeedsOperatorAction(ctx, job.ID, models.SweepOperatorActionReconcileBroadcast, errors.New("broadcast uncertain")); err != nil {
		t.Fatalf("mark needs operator action: %v", err)
	}
	retryAt := time.Now().UTC().Add(time.Minute)
	if err := repo.RecordOperatorRecovery(ctx, job.ID, models.SweepRecoveryActionRetry, "chain lookup found no broadcast; retry permitted", "", &retryAt); err != nil {
		t.Fatalf("record retry recovery: %v", err)
	}
	if err := repo.RecordOperatorRecovery(ctx, job.ID, models.SweepRecoveryActionRetry, "chain lookup found no broadcast; retry permitted", "", &retryAt); err != nil {
		t.Fatalf("repeat retry recovery should be idempotent: %v", err)
	}
	retry, err := repo.Find(ctx, job.ID)
	if err != nil {
		t.Fatalf("find retry recovery: %v", err)
	}
	if retry.Status != models.SweepJobStatusFailed || retry.NextRunAt == nil || retry.OperatorAction != "" || retry.RecoveredAt == nil || retry.RecoveryAction != models.SweepRecoveryActionRetry {
		t.Fatalf("retry recovery state = %#v", retry)
	}

	if err := repo.MarkNeedsOperatorAction(ctx, job.ID, models.SweepOperatorActionReconcileBroadcast, errors.New("replacement landed")); err != nil {
		t.Fatalf("mark second operator action: %v", err)
	}
	if err := repo.RecordOperatorRecovery(ctx, job.ID, models.SweepRecoveryActionMarkSuccess, "chain confirmed replacement", " 0xrecovered ", nil); err != nil {
		t.Fatalf("record success recovery: %v", err)
	}
	succeeded, err := repo.Find(ctx, job.ID)
	if err != nil {
		t.Fatalf("find success recovery: %v", err)
	}
	if succeeded.Status != models.SweepJobStatusSucceeded || succeeded.SweepTxHash != "0xrecovered" || succeeded.RecoveryAction != models.SweepRecoveryActionMarkSuccess {
		t.Fatalf("success recovery state = %#v", succeeded)
	}
	assertSweepLifecycleEvents(t, db, job.ID,
		constants.WebhookEventSweepRequestedV1,
		constants.WebhookEventSweepDeadLetteredV1,
		constants.WebhookEventSweepFailedV1,
		constants.WebhookEventSweepDeadLetteredV1,
		constants.WebhookEventSweepSucceededV1,
	)
}

func TestSweepJobRepoTerminalUpdatesAreFencedAndRequireTxHash(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiredLock := now.Add(-time.Minute)
	job := sweepJobTestJob("expired-fence", uuid.New(), uuid.New(), constants.Ethereum, models.SweepJobStatusProcessing, now, nil, &expiredLock)
	if err := db.WithContext(ctx).Create(&job).Error; err != nil {
		t.Fatalf("seed sweep job: %v", err)
	}

	if err := repo.MarkSucceeded(ctx, job.ID, " "); !errors.Is(err, ErrSweepJobTxHashRequired) {
		t.Fatalf("blank success error = %v, want ErrSweepJobTxHashRequired", err)
	}
	if err := repo.MarkSucceeded(ctx, job.ID, "0xsweep"); !errors.Is(err, ErrSweepJobStateConflict) {
		t.Fatalf("expired success error = %v, want ErrSweepJobStateConflict", err)
	}
	if err := repo.MarkFailed(ctx, job.ID, errors.New("rpc unavailable")); !errors.Is(err, ErrSweepJobStateConflict) {
		t.Fatalf("expired failed error = %v, want ErrSweepJobStateConflict", err)
	}
	if err := repo.MarkBroadcastUncertain(ctx, job.ID, errors.New("broadcast timeout")); !errors.Is(err, ErrSweepJobStateConflict) {
		t.Fatalf("expired uncertain error = %v, want ErrSweepJobStateConflict", err)
	}
}

func TestSweepJobRepoErrorTextRedactsAndPreservesUTF8(t *testing.T) {
	raw := "raw_tx=0x" + strings.Repeat("ab", 120) + " " + strings.Repeat("ç", models.SweepJobErrorMaxLength)
	text := sweepJobErrorText(errors.New(raw), "")
	if !strings.Contains(text, "<redacted") {
		t.Fatalf("error text was not redacted: %q", text[:min(len(text), 80)])
	}
	if !utf8.ValidString(text) {
		t.Fatalf("error text is invalid UTF-8: %q", text)
	}
	if len([]rune(text)) > models.SweepJobErrorMaxLength {
		t.Fatalf("error text rune length = %d, want bounded", len([]rune(text)))
	}
}

func TestSweepJobRepoEnqueueMissingFinalizedTransactions(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.SweepJob{}, &models.Transaction{}, &models.Wallet{}); err != nil {
		t.Fatalf("automigrate sweep scheduler models: %v", err)
	}
	ctx := context.Background()
	repo := NewSweepJobRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	userWalletID := uuid.New()
	reserveWalletID := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.WithContext(ctx).Create(&models.Merchant{
		ID: merchantID, Name: "Sweep Scheduler Merchant", Email: "sweep-" + merchantID.String() + "@example.test", IsActive: true,
	}).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.WithContext(ctx).Create(&models.Domain{
		ID: domainID, MerchantID: merchantID, DomainURL: "sweep-" + domainID.String() + ".example.test", APIKey: "pk_" + domainID.String(), APISecret: "secret", HDAccountID: 1,
	}).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	wallets := []models.Wallet{
		{ID: userWalletID, MerchantID: merchantID, DomainID: domainID, ProductID: "wallet:user", UserID: "user", HDAccountID: 1, HDAddressId: 7, CreatedAt: now, UpdatedAt: now},
		{ID: reserveWalletID, MerchantID: merchantID, DomainID: domainID, ProductID: "reserve", UserID: "reserve", HDAccountID: 1, HDAddressId: 0, CreatedAt: now, UpdatedAt: now},
	}
	for i := range wallets {
		suffix := strings.ReplaceAll(wallets[i].ID.String(), "-", "")
		wallets[i].BitcoinAddress = "btc-" + suffix
		wallets[i].EthereumAddress = "0x" + suffix
		wallets[i].AvalancheAddress = "avax-" + suffix
		wallets[i].BinanceAddress = "bnb-" + suffix
		wallets[i].BaseAddress = "base-" + suffix
		wallets[i].ArbitrumAddress = "arb-" + suffix
		wallets[i].UnichainAddress = "uni-" + suffix
		wallets[i].TronAddress = "tron-" + suffix
		wallets[i].SolanaAddress = "sol-" + suffix
		wallets[i].ChilizAddress = "chz-" + suffix
		wallets[i].ChilizSpicyAddress = "spicy-" + suffix
	}
	if err := db.WithContext(ctx).Create(&wallets).Error; err != nil {
		t.Fatalf("seed wallets: %v", err)
	}
	finalized := now
	txs := []models.Transaction{
		sweepJobSchedulerTx("scheduler-user", userWalletID, merchantID, domainID, &finalized),
		sweepJobSchedulerTx("scheduler-reserve", reserveWalletID, merchantID, domainID, &finalized),
		sweepJobSchedulerTx("scheduler-pending", userWalletID, merchantID, domainID, nil),
		sweepJobSchedulerTxWithAmount("scheduler-zero", userWalletID, merchantID, domainID, &finalized, "0"),
	}
	if err := db.WithContext(ctx).Create(&txs).Error; err != nil {
		t.Fatalf("seed transactions: %v", err)
	}

	eligible, err := repo.CountMissingFinalizedTransactions(ctx)
	if err != nil {
		t.Fatalf("count missing finalized: %v", err)
	}
	if eligible != 1 {
		t.Fatalf("eligible = %d, want one finalized positive user-wallet transaction", eligible)
	}

	created, err := repo.EnqueueMissingFinalizedTransactions(ctx, 100)
	if err != nil {
		t.Fatalf("enqueue missing finalized: %v", err)
	}
	if len(created) != 1 || created[0].TransactionUniqueHash != "scheduler-user" {
		t.Fatalf("created = %#v, want only finalized user-wallet transaction", created)
	}
	createdAgain, err := repo.EnqueueMissingFinalizedTransactions(ctx, 100)
	if err != nil {
		t.Fatalf("enqueue missing finalized again: %v", err)
	}
	if len(createdAgain) != 0 {
		t.Fatalf("created again = %#v, want idempotent no-op", createdAgain)
	}
}

func TestSweepJobClaimDueUsesPostgresSkipLockedAndProcessingRecovery(t *testing.T) {
	source, err := os.ReadFile("sweep_job_repo.go")
	if err != nil {
		t.Fatalf("read sweep_job_repo.go: %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"FOR UPDATE OF sj SKIP LOCKED",
		"models.SweepJobStatusProcessing",
		"sj.status IN ?",
		"active.locked_until >= ?",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("ClaimDue source missing %q", want)
		}
	}
}

func sweepJobSchedulerTx(uniqueHash string, walletID, merchantID, domainID uuid.UUID, finalizedAt *time.Time) models.Transaction {
	return sweepJobSchedulerTxWithAmount(uniqueHash, walletID, merchantID, domainID, finalizedAt, "100")
}

func sweepJobSchedulerTxWithAmount(uniqueHash string, walletID, merchantID, domainID uuid.UUID, finalizedAt *time.Time, amount string) models.Transaction {
	status := models.TransactionStatusPendingConfirmation
	if finalizedAt != nil {
		status = models.TransactionStatusConfirmed
	}
	return models.Transaction{
		ID:                    uuid.New(),
		ChainID:               constants.Ethereum,
		UniqueHash:            uniqueHash,
		Hash:                  "0x" + strings.ReplaceAll(uniqueHash, "-", ""),
		BlockNumber:           "123",
		Symbol:                "ETH",
		Decimals:              18,
		FromAddress:           "0xfrom",
		ToAddress:             "0xto",
		Amount:                amount,
		Status:                status,
		Confirmations:         12,
		ConfirmationsRequired: 12,
		FinalizedAt:           finalizedAt,
		WalletID:              &walletID,
		MerchantID:            &merchantID,
		DomainID:              &domainID,
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
}

func sweepJobTestTransaction(uniqueHash string, walletID, merchantID uuid.UUID, chainID constants.ChainID) models.Transaction {
	domainID := uuid.New()
	return models.Transaction{
		UniqueHash: uniqueHash,
		Hash:       strings.TrimPrefix(uniqueHash, "1-"),
		WalletID:   &walletID,
		MerchantID: &merchantID,
		DomainID:   &domainID,
		ChainID:    chainID,
		Symbol:     "ETH",
		Decimals:   18,
		Amount:     "100",
	}
}

func seedSweepLifecycleJob(t *testing.T, db *gorm.DB, uniqueHash string) (*SweepJobRepo, models.Transaction, *models.SweepJob) {
	t.Helper()
	if err := db.AutoMigrate(&models.Transaction{}, &models.SweepJob{}); err != nil {
		t.Fatalf("automigrate sweep lifecycle models: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	txModel := sweepJobTestTransaction(uniqueHash+"-"+uuid.NewString(), uuid.New(), uuid.New(), constants.Ethereum)
	txModel.Status = models.TransactionStatusConfirmed
	txModel.FinalizedAt = &now
	txModel.CreatedAt = now
	txModel.UpdatedAt = now
	if err := db.Create(&txModel).Error; err != nil {
		t.Fatalf("seed sweep source transaction: %v", err)
	}
	repo := NewSweepJobRepo(db)
	job, created, err := repo.EnqueueForTransaction(context.Background(), txModel)
	if err != nil {
		t.Fatalf("enqueue sweep lifecycle job: %v", err)
	}
	if !created || job == nil {
		t.Fatalf("sweep lifecycle job was not created: created=%v job=%#v", created, job)
	}
	return repo, txModel, job
}

func markSweepJobProcessing(t *testing.T, db *gorm.DB, id uuid.UUID) {
	t.Helper()
	lockedUntil := time.Now().UTC().Add(time.Minute)
	if err := db.Model(&models.SweepJob{}).Where("id = ?", id).Updates(map[string]any{
		"status": models.SweepJobStatusProcessing, "locked_until": &lockedUntil, "next_run_at": nil,
	}).Error; err != nil {
		t.Fatalf("mark sweep job processing: %v", err)
	}
}

func assertSweepLifecycleEvents(t *testing.T, db *gorm.DB, jobID uuid.UUID, eventTypes ...string) {
	t.Helper()
	var rows []models.MoneyEventOutbox
	if err := db.Where("aggregate_type = ? AND aggregate_id = ?", webhooksvc.EntityTypeSweep, jobID.String()).
		Order("sequence ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(eventTypes) {
		t.Fatalf("sweep lifecycle events = %#v, want %v", rows, eventTypes)
	}
	for i, eventType := range eventTypes {
		if rows[i].EventType != eventType || rows[i].Sequence != int64(i+1) {
			t.Fatalf("sweep lifecycle event[%d] = type:%q sequence:%d, want type:%q sequence:%d", i, rows[i].EventType, rows[i].Sequence, eventType, i+1)
		}
	}
}

func assertSweepLifecyclePayload(t *testing.T, db *gorm.DB, jobID uuid.UUID, eventType, status, sweepTxHash string) {
	t.Helper()
	var event models.MoneyEventOutbox
	if err := db.First(&event, "event_id = ?", jobID.String()+":"+eventType).Error; err != nil {
		t.Fatal(err)
	}
	var payload webhooksvc.LifecyclePayload
	if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
		t.Fatalf("decode lifecycle payload: %v", err)
	}
	if payload.Status != status || payload.ResourceStatus != status || payload.SweepTxHash != sweepTxHash {
		t.Fatalf("lifecycle payload = %#v, want status=%q sweep_tx_hash=%q", payload, status, sweepTxHash)
	}
}

func sweepJobTestJob(uniqueHash string, walletID, merchantID uuid.UUID, chainID constants.ChainID, status string, createdAt time.Time, nextRunAt, lockedUntil *time.Time) models.SweepJob {
	return models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: uniqueHash,
		TransactionHash:       "0x" + strings.ReplaceAll(uniqueHash, "-", ""),
		WalletID:              walletID,
		MerchantID:            merchantID,
		ChainID:               chainID,
		Status:                status,
		MaxAttempts:           3,
		NextRunAt:             nextRunAt,
		LockedUntil:           lockedUntil,
		CreatedAt:             createdAt,
		UpdatedAt:             createdAt,
	}
}

func contextDeadlineExceeded() error {
	return context.DeadlineExceeded
}
