package repositories

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrSweepJobStateConflict   = errors.New("sweep job state conflict")
	ErrSweepJobTxHashRequired  = errors.New("sweep transaction hash is required")
	ErrSweepRecoveryAction     = errors.New("invalid sweep recovery action")
	ErrSweepRecoveryNoteNeeded = errors.New("sweep recovery note is required")
)

type SweepJobRepo struct {
	db *gorm.DB
}

func NewSweepJobRepo(db *gorm.DB) *SweepJobRepo {
	return &SweepJobRepo{db: db}
}

func (r *SweepJobRepo) EnqueueForTransaction(ctx context.Context, txModel models.Transaction) (*models.SweepJob, bool, error) {
	if txModel.WalletID == nil || txModel.MerchantID == nil {
		return nil, false, nil
	}
	if txModel.UniqueHash == "" {
		return nil, false, errors.New("transaction unique hash is required")
	}
	now := time.Now()
	job := &models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: txModel.UniqueHash,
		TransactionHash:       txModel.Hash,
		WalletID:              *txModel.WalletID,
		MerchantID:            *txModel.MerchantID,
		ChainID:               txModel.ChainID,
		Token:                 txModel.Token,
		Status:                models.SweepJobStatusPending,
		MaxAttempts:           uintFromEnv("SWEEP_MAX_ATTEMPTS", 12),
		PrefundMaxAttempts:    uintFromEnv("SWEEP_PREFUND_MAX_ATTEMPTS", 3),
		NextRunAt:             &now,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(job)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return job, true, nil
	}
	var existing models.SweepJob
	if err := r.db.WithContext(ctx).First(&existing, "transaction_unique_hash = ?", txModel.UniqueHash).Error; err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

func (r *SweepJobRepo) ClaimDue(ctx context.Context, limit int, lockFor time.Duration) ([]models.SweepJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if lockFor <= 0 {
		lockFor = 2 * time.Minute
	}
	now := time.Now()
	lockUntil := now.Add(lockFor)
	var jobs []models.SweepJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(`
			WITH ranked_due AS (
				SELECT
					sj.id,
					sj.created_at,
					ROW_NUMBER() OVER (
						PARTITION BY sj.wallet_id, sj.chain_id
						ORDER BY sj.created_at ASC
					) AS wallet_rank
				FROM sweep_jobs sj
				WHERE sj.status IN ?
				  AND (sj.next_run_at IS NULL OR sj.next_run_at <= ?)
				  AND (sj.locked_until IS NULL OR sj.locked_until < ?)
				  AND NOT EXISTS (
					SELECT 1
					FROM sweep_jobs active
					WHERE active.wallet_id = sj.wallet_id
					  AND active.chain_id = sj.chain_id
					  AND active.status = ?
					  AND active.locked_until IS NOT NULL
					  AND active.locked_until >= ?
				  )
			),
			due AS (
				SELECT id
				FROM ranked_due
				WHERE wallet_rank = 1
				ORDER BY created_at ASC
				LIMIT ?
			)
			SELECT sj.*
			FROM sweep_jobs sj
			JOIN due ON due.id = sj.id
			ORDER BY sj.created_at ASC
			FOR UPDATE OF sj SKIP LOCKED
		`,
			[]string{models.SweepJobStatusPending, models.SweepJobStatusFailed, models.SweepJobStatusProcessing},
			now,
			now,
			models.SweepJobStatusProcessing,
			now,
			limit,
		).Scan(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(jobs))
		for _, job := range jobs {
			ids = append(ids, job.ID)
		}
		return tx.Model(&models.SweepJob{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":       models.SweepJobStatusProcessing,
				"locked_until": &lockUntil,
				"updated_at":   now,
			}).Error
	})
	return jobs, err
}

func (r *SweepJobRepo) AssignBatch(ctx context.Context, batchID uuid.UUID, batchKey string, policy string, jobIDs []uuid.UUID) error {
	if batchID == uuid.Nil {
		return errors.New("sweep batch id is required")
	}
	batchKey = strings.TrimSpace(batchKey)
	if batchKey == "" {
		return errors.New("sweep batch key is required")
	}
	if len(jobIDs) == 0 {
		return nil
	}
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for index, id := range jobIDs {
			result := tx.Model(&models.SweepJob{}).
				Where("id = ?", id).
				Where("status IN ?", []string{models.SweepJobStatusPending, models.SweepJobStatusFailed}).
				Updates(map[string]any{
					"batch_id":      &batchID,
					"batch_key":     batchKey,
					"batch_ordinal": uint(index + 1),
					"batch_size":    uint(len(jobIDs)),
					"batch_policy":  strings.TrimSpace(policy),
					"updated_at":    now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrSweepJobStateConflict
			}
		}
		return nil
	})
}

func (r *SweepJobRepo) ReservePrefundAttempt(ctx context.Context, id uuid.UUID, retryAfter time.Duration, maxAttempts uint) (bool, error) {
	if retryAfter <= 0 {
		retryAfter = 10 * time.Minute
	}
	if maxAttempts == 0 {
		maxAttempts = uintFromEnv("SWEEP_PREFUND_MAX_ATTEMPTS", 3)
	}
	now := time.Now()
	cutoff := now.Add(-retryAfter)
	result := r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Where("status IN ?", []string{models.SweepJobStatusPending, models.SweepJobStatusProcessing, models.SweepJobStatusFailed}).
		Where("(prefund_max_attempts = 0 AND prefund_attempts < ?) OR (prefund_max_attempts > 0 AND prefund_attempts < prefund_max_attempts)", maxAttempts).
		Where("prefunded_at IS NULL OR prefunded_at <= ?", cutoff).
		Where("prefund_last_attempt_at IS NULL OR prefund_last_attempt_at <= ?", cutoff).
		Updates(map[string]any{
			"prefund_max_attempts":    gorm.Expr("CASE WHEN prefund_max_attempts = 0 THEN ? ELSE prefund_max_attempts END", maxAttempts),
			"prefund_last_attempt_at": &now,
			"operator_action":         "",
			"updated_at":              now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (r *SweepJobRepo) MarkPrefunded(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"prefund_attempts":         gorm.Expr("prefund_attempts + 1"),
			"prefund_last_error":       "",
			"prefund_failure_category": "",
			"prefund_last_attempt_at":  &now,
			"prefunded_at":             &now,
			"operator_action":          "",
			"updated_at":               now,
		}).Error
}

func (r *SweepJobRepo) MarkPrefundFailed(ctx context.Context, id uuid.UUID, err error) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"prefund_attempts":         gorm.Expr("prefund_attempts + 1"),
			"prefund_last_error":       sweepJobErrorText(err, ""),
			"prefund_failure_category": sweepJobFailureCategory(err),
			"prefund_last_attempt_at":  &now,
			"operator_action":          gorm.Expr("CASE WHEN prefund_max_attempts > 0 AND prefund_attempts + 1 >= prefund_max_attempts THEN ? ELSE operator_action END", models.SweepOperatorActionReviewGasFunding),
			"updated_at":               now,
		}).Error
}

func (r *SweepJobRepo) MarkSucceeded(ctx context.Context, id uuid.UUID, txHash string) error {
	now := time.Now()
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrSweepJobTxHashRequired
	}
	result := r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Where("status = ?", models.SweepJobStatusProcessing).
		Where("locked_until IS NULL OR locked_until >= ?", now).
		Updates(map[string]any{
			"status":           models.SweepJobStatusSucceeded,
			"sweep_tx_hash":    txHash,
			"last_error":       "",
			"failure_category": "",
			"locked_until":     nil,
			"next_run_at":      nil,
			"updated_at":       now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSweepJobStateConflict
	}
	return nil
}

func (r *SweepJobRepo) MarkFailed(ctx context.Context, id uuid.UUID, err error) error {
	var job models.SweepJob
	if readErr := r.db.WithContext(ctx).First(&job, "id = ?", id).Error; readErr != nil {
		return readErr
	}
	if job.Status == models.SweepJobStatusSucceeded || job.Status == models.SweepJobStatusDeadLetter {
		return ErrSweepJobStateConflict
	}
	attempts := job.Attempts + 1
	maxAttempts := job.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = uintFromEnv("SWEEP_MAX_ATTEMPTS", 12)
	}
	category := sweepJobFailureCategory(err)
	status := models.SweepJobStatusFailed
	var nextRunAt *time.Time
	if category == models.SweepFailureCategoryPolicy || attempts >= maxAttempts {
		status = models.SweepJobStatusDeadLetter
	} else {
		next := time.Now().Add(sweepRetryBackoff(attempts))
		nextRunAt = &next
	}
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Where("status IN ?", []string{models.SweepJobStatusPending, models.SweepJobStatusProcessing, models.SweepJobStatusFailed}).
		Where("locked_until IS NULL OR locked_until >= ?", now).
		Updates(map[string]any{
			"status":           status,
			"attempts":         attempts,
			"last_error":       sweepJobErrorText(err, ""),
			"failure_category": category,
			"locked_until":     nil,
			"next_run_at":      nextRunAt,
			"updated_at":       now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSweepJobStateConflict
	}
	return nil
}

// DeferForNetworkState returns a claimed sweep to the pending queue without
// consuming an attempt. Listener ingestion remains live while outbound chain
// activity is paused, so these jobs can resume after an admin re-enables the
// network.
func (r *SweepJobRepo) DeferForNetworkState(ctx context.Context, id uuid.UUID, detail string, retryAfter time.Duration) error {
	if retryAfter <= 0 {
		retryAfter = 30 * time.Second
	}
	now := time.Now()
	next := now.Add(retryAfter)
	result := r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Where("status IN ?", []string{models.SweepJobStatusPending, models.SweepJobStatusProcessing, models.SweepJobStatusFailed}).
		Updates(map[string]any{
			"status":           models.SweepJobStatusPending,
			"last_error":       sweepJobErrorText(nil, detail),
			"failure_category": models.SweepFailureCategoryNetworkMaintenance,
			"locked_until":     nil,
			"next_run_at":      &next,
			"updated_at":       now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSweepJobStateConflict
	}
	return nil
}

func (r *SweepJobRepo) MarkBroadcastUncertain(ctx context.Context, id uuid.UUID, err error) error {
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Where("status IN ?", []string{models.SweepJobStatusPending, models.SweepJobStatusProcessing, models.SweepJobStatusFailed}).
		Where("locked_until IS NULL OR locked_until >= ?", now).
		Updates(map[string]any{
			"status":           models.SweepJobStatusDeadLetter,
			"attempts":         gorm.Expr("attempts + 1"),
			"last_error":       sweepJobErrorText(err, "broadcast outcome uncertain"),
			"failure_category": models.SweepFailureCategoryBroadcastUncertain,
			"operator_action":  models.SweepOperatorActionReconcileBroadcast,
			"locked_until":     nil,
			"next_run_at":      nil,
			"updated_at":       now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSweepJobStateConflict
	}
	return nil
}

func (r *SweepJobRepo) MarkNeedsOperatorAction(ctx context.Context, id uuid.UUID, action string, err error) error {
	action = strings.TrimSpace(action)
	if action == "" {
		action = models.SweepOperatorActionReviewPolicy
	}
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Where("status IN ?", []string{models.SweepJobStatusPending, models.SweepJobStatusProcessing, models.SweepJobStatusFailed, models.SweepJobStatusDeadLetter}).
		Updates(map[string]any{
			"status":           models.SweepJobStatusDeadLetter,
			"last_error":       sweepJobErrorText(err, "operator action required"),
			"failure_category": sweepJobFailureCategory(err),
			"operator_action":  action,
			"locked_until":     nil,
			"next_run_at":      nil,
			"updated_at":       now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSweepJobStateConflict
	}
	return nil
}

func (r *SweepJobRepo) RecordOperatorRecovery(ctx context.Context, id uuid.UUID, action string, note string, txHash string, retryAt *time.Time) error {
	action = strings.TrimSpace(action)
	note = strings.TrimSpace(note)
	txHash = strings.TrimSpace(txHash)
	if note == "" {
		return ErrSweepRecoveryNoteNeeded
	}
	now := time.Now()
	updates := map[string]any{
		"operator_note":   sweepJobErrorText(nil, note),
		"recovery_action": action,
		"recovered_at":    &now,
		"locked_until":    nil,
		"updated_at":      now,
	}
	switch action {
	case models.SweepRecoveryActionRetry:
		if retryAt == nil {
			retry := now
			retryAt = &retry
		}
		updates["status"] = models.SweepJobStatusFailed
		updates["next_run_at"] = retryAt
		updates["operator_action"] = ""
	case models.SweepRecoveryActionMarkSuccess:
		if txHash == "" {
			return ErrSweepJobTxHashRequired
		}
		updates["status"] = models.SweepJobStatusSucceeded
		updates["sweep_tx_hash"] = txHash
		updates["next_run_at"] = nil
		updates["operator_action"] = ""
		updates["last_error"] = ""
		updates["failure_category"] = ""
	case models.SweepRecoveryActionPreserveHold, models.SweepRecoveryActionReleaseHold:
		updates["status"] = models.SweepJobStatusDeadLetter
		updates["next_run_at"] = nil
	default:
		return ErrSweepRecoveryAction
	}
	result := r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Where("status IN ?", []string{models.SweepJobStatusFailed, models.SweepJobStatusDeadLetter}).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSweepJobStateConflict
	}
	return nil
}

func (r *SweepJobRepo) Find(ctx context.Context, id uuid.UUID) (*models.SweepJob, error) {
	var job models.SweepJob
	if err := r.db.WithContext(ctx).First(&job, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *SweepJobRepo) ListDeadLetters(ctx context.Context, limit int) ([]models.SweepJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var jobs []models.SweepJob
	err := r.db.WithContext(ctx).
		Where("status = ?", models.SweepJobStatusDeadLetter).
		Order("updated_at DESC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

func (r *SweepJobRepo) CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
	out := make(map[string]int64, len(statuses))
	for _, status := range statuses {
		out[status] = 0
	}
	if len(statuses) == 0 {
		return out, nil
	}
	var rows []struct {
		Status string
		Count  int64
	}
	err := r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Select("status, COUNT(*) AS count").
		Where("status IN ?", statuses).
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.Status] = row.Count
	}
	return out, nil
}

func (r *SweepJobRepo) CountMissingFinalizedTransactions(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).
		Model(&models.Transaction{}).
		Joins("JOIN wallets ON wallets.id = transactions.wallet_id").
		Joins("LEFT JOIN sweep_jobs ON sweep_jobs.transaction_unique_hash = transactions.unique_hash").
		Where("transactions.wallet_id IS NOT NULL").
		Where("transactions.merchant_id IS NOT NULL").
		Where("transactions.finalized_at IS NOT NULL").
		Where("transactions.status = ?", models.TransactionStatusConfirmed).
		Where("transactions.amount ~ '^[0-9]+$'").
		Where("transactions.amount::numeric > 0").
		Where("wallets.hd_address_id <> 0").
		Where("sweep_jobs.id IS NULL").
		Count(&total).Error
	return total, err
}

func (r *SweepJobRepo) ListPage(ctx context.Context, page, limit int, status string) ([]models.SweepJob, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	status = strings.TrimSpace(status)
	q := r.db.WithContext(ctx).Model(&models.SweepJob{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []models.SweepJob
	err := q.Order("created_at DESC").
		Limit(limit).
		Offset((page - 1) * limit).
		Find(&rows).Error
	return rows, total, err
}

func (r *SweepJobRepo) EnqueueMissingFinalizedTransactions(ctx context.Context, limit int) ([]models.SweepJob, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	var txs []models.Transaction
	if err := r.db.WithContext(ctx).
		Model(&models.Transaction{}).
		Joins("JOIN wallets ON wallets.id = transactions.wallet_id").
		Joins("LEFT JOIN sweep_jobs ON sweep_jobs.transaction_unique_hash = transactions.unique_hash").
		Where("transactions.wallet_id IS NOT NULL").
		Where("transactions.merchant_id IS NOT NULL").
		Where("transactions.finalized_at IS NOT NULL").
		Where("transactions.status = ?", models.TransactionStatusConfirmed).
		Where("transactions.amount ~ '^[0-9]+$'").
		Where("transactions.amount::numeric > 0").
		Where("wallets.hd_address_id <> 0").
		Where("sweep_jobs.id IS NULL").
		Order("transactions.finalized_at ASC, transactions.created_at ASC").
		Limit(limit).
		Find(&txs).Error; err != nil {
		return nil, err
	}
	created := make([]models.SweepJob, 0, len(txs))
	for _, txModel := range txs {
		job, inserted, err := r.EnqueueForTransaction(ctx, txModel)
		if err != nil {
			return created, err
		}
		if inserted && job != nil {
			created = append(created, *job)
		}
	}
	return created, nil
}

func sweepJobErrorText(err error, fallback string) string {
	text := strings.TrimSpace(fallback)
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		text = strings.TrimSpace(err.Error())
	}
	text = redactSweepJobErrorText(text)
	runes := []rune(text)
	if len(runes) > models.SweepJobErrorMaxLength {
		return string(runes[:models.SweepJobErrorMaxLength])
	}
	return text
}

func redactSweepJobErrorText(text string) string {
	for _, prefix := range []string{"raw_tx=0x", "signed_tx=0x", "signature=0x"} {
		text = redactHexDiagnostic(text, prefix)
	}
	return text
}

func redactHexDiagnostic(text, prefix string) string {
	for {
		idx := strings.Index(strings.ToLower(text), prefix)
		if idx == -1 {
			return text
		}
		start := idx + len(prefix)
		end := start
		for end < len(text) && isASCIIHex(text[end]) {
			end++
		}
		if end-start < 16 {
			return text
		}
		replacement := prefix + "<redacted:" + strconv.Itoa(end-start) + "hex>"
		text = text[:idx] + replacement + text[end:]
	}
}

func isASCIIHex(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func sweepJobFailureCategory(err error) string {
	if err == nil {
		return models.SweepFailureCategoryUnknown
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return models.SweepFailureCategoryTimeout
	}
	if errors.Is(err, ErrLedgerReservationRequired) || errors.Is(err, ErrInsufficientAvailableBalance) {
		return models.SweepFailureCategoryPolicy
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	for _, token := range []string{
		"policy",
		"not found",
		"no reserve wallet",
		"has no address",
		"re-derive",
		"reservation",
		"amount must be positive",
		"mismatch",
		"gas balance below threshold",
		"fee cap",
		"fee/resource",
	} {
		if strings.Contains(msg, token) {
			return models.SweepFailureCategoryPolicy
		}
	}
	for _, token := range []string{
		"timeout",
		"timed out",
		"deadline exceeded",
	} {
		if strings.Contains(msg, token) {
			return models.SweepFailureCategoryTimeout
		}
	}
	for _, token := range []string{
		"connection refused",
		"connection reset",
		"network",
		"temporary",
		"unavailable",
		"rpc",
	} {
		if strings.Contains(msg, token) {
			return models.SweepFailureCategoryTransient
		}
	}
	return models.SweepFailureCategoryTransient
}
