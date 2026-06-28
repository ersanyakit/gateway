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
	ErrSweepJobStateConflict  = errors.New("sweep job state conflict")
	ErrSweepJobTxHashRequired = errors.New("sweep transaction hash is required")
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
