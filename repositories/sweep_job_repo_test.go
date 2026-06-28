package repositories

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
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
	wallets := []models.Wallet{
		{ID: userWalletID, MerchantID: merchantID, DomainID: domainID, ProductID: "wallet:user", UserID: "user", HDAccountID: 1, HDAddressId: 7, CreatedAt: now, UpdatedAt: now},
		{ID: reserveWalletID, MerchantID: merchantID, DomainID: domainID, ProductID: "reserve", UserID: "reserve", HDAccountID: 1, HDAddressId: 0, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.WithContext(ctx).Create(&wallets).Error; err != nil {
		t.Fatalf("seed wallets: %v", err)
	}
	finalized := now
	txs := []models.Transaction{
		sweepJobSchedulerTx("scheduler-user", userWalletID, merchantID, domainID, &finalized),
		sweepJobSchedulerTx("scheduler-reserve", reserveWalletID, merchantID, domainID, &finalized),
		sweepJobSchedulerTx("scheduler-pending", userWalletID, merchantID, domainID, nil),
	}
	if err := db.WithContext(ctx).Create(&txs).Error; err != nil {
		t.Fatalf("seed transactions: %v", err)
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
		Amount:                "100",
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
	return models.Transaction{
		UniqueHash: uniqueHash,
		Hash:       strings.TrimPrefix(uniqueHash, "1-"),
		WalletID:   &walletID,
		MerchantID: &merchantID,
		ChainID:    chainID,
		Symbol:     "ETH",
		Decimals:   18,
		Amount:     "100",
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
