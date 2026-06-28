package repositories

import (
	"context"
	"core/models"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WithdrawalRequestRepo struct {
	db *gorm.DB
}

var (
	ErrWithdrawalWalletBusy     = errors.New("withdrawal wallet has an active processing transfer")
	ErrTxHashRequired           = errors.New("tx hash is required")
	ErrWithdrawalTxHashRequired = ErrTxHashRequired
	ErrRefundTxHashRequired     = ErrTxHashRequired
)

func OutboundTransferFailureBroadcastUncertain(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, token := range []string{
		"broadcast",
		"sendtransaction",
		"already known",
		"nonce too low",
		"replacement transaction underpriced",
		"transaction underpriced",
		"mempool",
	} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}

func NewWithdrawalRequestRepo(db *gorm.DB) *WithdrawalRequestRepo {
	return &WithdrawalRequestRepo{db: db}
}

func (r *WithdrawalRequestRepo) DB() *gorm.DB { return r.db }

func (r *WithdrawalRequestRepo) Create(ctx context.Context, request *models.WithdrawalRequest) error {
	return r.db.WithContext(ctx).Create(request).Error
}

func (r *WithdrawalRequestRepo) CreateWithHold(ctx context.Context, request *models.WithdrawalRequest, ledger *LedgerRepo) error {
	if ledger == nil {
		return ErrLedgerReservationRequired
	}
	if request.ID == uuid.Nil {
		request.ID = uuid.New()
	}
	if strings.TrimSpace(request.Status) == "" {
		request.Status = models.WithdrawalStatusPending
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(request).Error; err != nil {
			return err
		}
		return NewLedgerRepo(tx).CreateWithdrawalHoldWithDB(ctx, tx, *request)
	})
}

func (r *WithdrawalRequestRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.WithdrawalRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var requests []models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Preload("Wallet").
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&requests).Error
	return requests, err
}

func (r *WithdrawalRequestRepo) ListByMerchantPage(ctx context.Context, merchantID uuid.UUID, page, limit int) ([]models.WithdrawalRequest, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&models.WithdrawalRequest{}).Where("merchant_id = ?", merchantID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var requests []models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&requests).Error
	return requests, total, err
}

func (r *WithdrawalRequestRepo) ListByDomainPage(ctx context.Context, merchantID, domainID uuid.UUID, page, limit int) ([]models.WithdrawalRequest, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	base := r.db.WithContext(ctx).Where("merchant_id = ? AND domain_id = ?", merchantID, domainID)
	var total int64
	if err := base.Model(&models.WithdrawalRequest{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var requests []models.WithdrawalRequest
	err := base.
		Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&requests).Error
	return requests, total, err
}

func (r *WithdrawalRequestRepo) List(ctx context.Context, status string, limit int) ([]models.WithdrawalRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := r.db.WithContext(ctx).Preload("Merchant").Preload("Wallet").Order("created_at DESC").Limit(limit)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var requests []models.WithdrawalRequest
	err := query.Find(&requests).Error
	return requests, err
}

func (r *WithdrawalRequestRepo) ListPage(ctx context.Context, page, limit int) ([]models.WithdrawalRequest, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&models.WithdrawalRequest{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var requests []models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Preload("Merchant").Preload("Wallet").
		Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&requests).Error
	return requests, total, err
}

func (r *WithdrawalRequestRepo) ListProcessingWithTxHash(ctx context.Context, limit int) ([]models.WithdrawalRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var requests []models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Preload("Merchant").Preload("Wallet").
		Where("status = ?", models.WithdrawalStatusProcessing).
		Where("tx_hash <> ''").
		Order("updated_at ASC").
		Limit(limit).
		Find(&requests).Error
	return requests, err
}

func (r *WithdrawalRequestRepo) Find(ctx context.Context, id uuid.UUID) (*models.WithdrawalRequest, error) {
	var request models.WithdrawalRequest
	err := r.db.WithContext(ctx).Preload("Merchant").Preload("Wallet").First(&request, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (r *WithdrawalRequestRepo) FindByDomain(ctx context.Context, merchantID, domainID, id uuid.UUID) (*models.WithdrawalRequest, error) {
	var request models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Preload("Merchant").
		Preload("Wallet").
		Where("merchant_id = ? AND domain_id = ? AND id = ?", merchantID, domainID, id).
		First(&request).Error
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (r *WithdrawalRequestRepo) FindByDomainIdempotencyKey(ctx context.Context, merchantID, domainID uuid.UUID, key string) (*models.WithdrawalRequest, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var request models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Preload("Merchant").
		Preload("Wallet").
		Where("merchant_id = ? AND domain_id = ? AND idempotency_key = ?", merchantID, domainID, key).
		Order("created_at DESC").
		First(&request).Error
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (r *WithdrawalRequestRepo) RecordBroadcast(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	result := r.db.WithContext(ctx).Model(&models.WithdrawalRequest{}).
		Where("id = ? AND status = ?", id, models.WithdrawalStatusProcessing).
		Updates(map[string]any{
			"reviewed_by":    reviewedBy,
			"reviewed_at":    &now,
			"tx_hash":        txHash,
			"broadcasted_at": &now,
			"error":          "",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *WithdrawalRequestRepo) FinalizeProcessingWithLedger(ctx context.Context, id uuid.UUID, ledger *LedgerRepo) error {
	if ledger == nil {
		return ErrLedgerReservationRequired
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request models.WithdrawalRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			First(&request, "id = ? AND status = ? AND tx_hash <> ''", id, models.WithdrawalStatusProcessing).Error; err != nil {
			return err
		}
		if err := NewLedgerRepo(tx).PostWithdrawalDebitWithDB(ctx, tx, request, request.TxHash); err != nil {
			return err
		}
		now := time.Now()
		result := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusProcessing).
			Updates(map[string]any{
				"status":       models.WithdrawalStatusFinalized,
				"reviewed_at":  &now,
				"finalized_at": &now,
				"error":        "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (r *WithdrawalRequestRepo) MarkApproved(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&models.WithdrawalRequest{}).
		Where("id = ? AND status IN ?", id, []string{models.WithdrawalStatusPending, models.WithdrawalStatusProcessing}).
		Updates(map[string]any{
			"status":         models.WithdrawalStatusFinalized,
			"reviewed_by":    reviewedBy,
			"reviewed_at":    &now,
			"tx_hash":        txHash,
			"broadcasted_at": &now,
			"finalized_at":   &now,
			"error":          "",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *WithdrawalRequestRepo) MarkRejected(ctx context.Context, id uuid.UUID, reviewedBy string, reason string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusPending).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusRejected,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       reason,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return NewLedgerRepo(tx).VoidWithdrawalHoldWithDB(ctx, tx, id)
	})
}

func (r *WithdrawalRequestRepo) MarkFailed(ctx context.Context, id uuid.UUID, reviewedBy string, errText string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request models.WithdrawalRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&request, "id = ? AND status IN ?", id, []string{models.WithdrawalStatusPending, models.WithdrawalStatusProcessing}).Error; err != nil {
			return err
		}
		result := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status IN ?", id, []string{models.WithdrawalStatusPending, models.WithdrawalStatusProcessing}).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusFailed,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       errText,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if strings.TrimSpace(request.TxHash) != "" {
			return nil
		}
		return NewLedgerRepo(tx).VoidWithdrawalHoldWithDB(ctx, tx, id)
	})
}

func (r *WithdrawalRequestRepo) MarkFailedFinalWithLedgerRelease(ctx context.Context, id uuid.UUID, reviewedBy string, errText string, ledger *LedgerRepo) error {
	if ledger == nil {
		return ErrLedgerReservationRequired
	}
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request models.WithdrawalRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			First(&request, "id = ? AND status = ? AND tx_hash <> ''", id, models.WithdrawalStatusProcessing).Error; err != nil {
			return err
		}
		result := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusProcessing).
			Updates(map[string]any{
				"status":       models.WithdrawalStatusFailed,
				"reviewed_by":  reviewedBy,
				"reviewed_at":  &now,
				"finalized_at": &now,
				"error":        errText,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return NewLedgerRepo(tx).VoidWithdrawalHoldWithDB(ctx, tx, id)
	})
}

func (r *WithdrawalRequestRepo) SetProcessingError(ctx context.Context, id uuid.UUID, errText string) error {
	result := r.db.WithContext(ctx).Model(&models.WithdrawalRequest{}).
		Where("id = ? AND status = ?", id, models.WithdrawalStatusProcessing).
		Update("error", errText)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *WithdrawalRequestRepo) LockPending(ctx context.Context, id uuid.UUID) (*models.WithdrawalRequest, error) {
	var request models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Preload("Merchant").
		Preload("Wallet").
		First(&request, "id = ? AND status = ?", id, models.WithdrawalStatusPending).Error
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (r *WithdrawalRequestRepo) ApproveWithTransfer(ctx context.Context, id uuid.UUID, reviewedBy string, ledger *LedgerRepo, transfer func(*models.WithdrawalRequest) (string, error)) (*models.WithdrawalRequest, error) {
	if transfer == nil {
		return nil, errors.New("transfer callback is required")
	}
	if ledger == nil {
		return nil, ErrLedgerReservationRequired
	}

	var request models.WithdrawalRequest
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Merchant").
			Preload("Wallet").
			First(&request, "id = ? AND status = ?", id, models.WithdrawalStatusPending).Error; err != nil {
			return err
		}

		lockKey := withdrawalWalletChainLockKey(request.WalletID, request.Chain)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockKey).Error; err != nil {
			return err
		}

		var activeCount int64
		if err := tx.Model(&models.WithdrawalRequest{}).
			Where("wallet_id = ? AND chain = ? AND status = ? AND id <> ?", request.WalletID, request.Chain, models.WithdrawalStatusProcessing, request.ID).
			Count(&activeCount).Error; err != nil {
			return err
		}
		if activeCount > 0 {
			return ErrWithdrawalWalletBusy
		}
		if err := NewLedgerRepo(tx).RequireWithdrawalHoldWithDB(ctx, tx, request.ID); err != nil {
			return err
		}

		now := time.Now()
		result := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusPending).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusProcessing,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		request.Status = models.WithdrawalStatusProcessing
		request.ReviewedBy = reviewedBy
		request.ReviewedAt = &now
		request.Error = ""
		return nil
	})
	if err != nil {
		return nil, err
	}

	txHash, transferErr := transfer(&request)
	if transferErr != nil {
		if OutboundTransferFailureBroadcastUncertain(transferErr) {
			errText := "broadcast outcome uncertain: " + transferErr.Error()
			_ = r.SetProcessingError(ctx, id, errText)
			request.Status = models.WithdrawalStatusProcessing
			request.Error = errText
			return &request, transferErr
		}
		_ = r.MarkFailed(ctx, id, reviewedBy, transferErr.Error())
		request.Status = models.WithdrawalStatusFailed
		request.Error = transferErr.Error()
		return &request, transferErr
	}
	if err := r.RecordBroadcast(ctx, id, reviewedBy, txHash); err != nil {
		errText := "broadcast sent but tx hash persist failed: " + err.Error()
		_ = r.SetProcessingError(ctx, id, errText)
		request.Status = models.WithdrawalStatusProcessing
		request.TxHash = strings.TrimSpace(txHash)
		request.Error = errText
		return &request, err
	}
	now := time.Now()
	request.Status = models.WithdrawalStatusProcessing
	request.ReviewedBy = reviewedBy
	request.ReviewedAt = &now
	request.BroadcastedAt = &now
	request.TxHash = strings.TrimSpace(txHash)
	request.Error = ""
	return &request, nil
}

func withdrawalWalletChainLockKey(walletID uuid.UUID, chain string) string {
	return fmt.Sprintf("withdrawal-wallet-chain:%s:%s", walletID, strings.ToLower(strings.TrimSpace(chain)))
}
