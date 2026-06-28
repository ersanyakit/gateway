package repositories

import (
	"context"
	"core/constants"
	"core/models"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RefundRepo struct {
	db *gorm.DB
}

func NewRefundRepo(db *gorm.DB) *RefundRepo {
	return &RefundRepo{db: db}
}

func (r *RefundRepo) Create(ctx context.Context, refund *models.Refund) error {
	if refund.ID == uuid.Nil {
		refund.ID = uuid.New()
	}
	if refund.Status == "" {
		refund.Status = models.RefundStatusPending
	}
	return r.db.WithContext(ctx).Create(refund).Error
}

func (r *RefundRepo) CreateWithHold(ctx context.Context, refund *models.Refund, session models.PaymentSession, ledger *LedgerRepo) error {
	if ledger == nil {
		return ErrLedgerReservationRequired
	}
	if refund.ID == uuid.Nil {
		refund.ID = uuid.New()
	}
	if refund.Status == "" {
		refund.Status = models.RefundStatusPending
	}
	applyRefundSessionMetadata(refund, session)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(refund).Error; err != nil {
			return err
		}
		return NewLedgerRepo(tx).CreateRefundHoldWithDB(ctx, tx, *refund, session)
	})
}

func (r *RefundRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.Refund, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var refunds []models.Refund
	err := r.db.WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&refunds).Error
	return refunds, err
}

func (r *RefundRepo) ListPage(ctx context.Context, page, limit int, status string) ([]models.Refund, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Model(&models.Refund{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var refunds []models.Refund
	err := q.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&refunds).Error
	return refunds, total, err
}

func (r *RefundRepo) ListByMerchantPage(ctx context.Context, merchantID uuid.UUID, page, limit int) ([]models.Refund, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Model(&models.Refund{}).Where("merchant_id = ?", merchantID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var refunds []models.Refund
	err := q.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&refunds).Error
	return refunds, total, err
}

func (r *RefundRepo) ListByDomainPage(ctx context.Context, merchantID, domainID uuid.UUID, page, limit int) ([]models.Refund, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Model(&models.Refund{}).Where("merchant_id = ? AND domain_id = ?", merchantID, domainID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var refunds []models.Refund
	err := q.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&refunds).Error
	return refunds, total, err
}

func (r *RefundRepo) Find(ctx context.Context, id uuid.UUID) (*models.Refund, error) {
	var refund models.Refund
	if err := r.db.WithContext(ctx).First(&refund, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &refund, nil
}

func (r *RefundRepo) FindByDomain(ctx context.Context, merchantID, domainID, id uuid.UUID) (*models.Refund, error) {
	var refund models.Refund
	if err := r.db.WithContext(ctx).
		Where("merchant_id = ? AND domain_id = ? AND id = ?", merchantID, domainID, id).
		First(&refund).Error; err != nil {
		return nil, err
	}
	return &refund, nil
}

func (r *RefundRepo) FindByDomainIdempotencyKey(ctx context.Context, merchantID, domainID uuid.UUID, key string) (*models.Refund, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var refund models.Refund
	if err := r.db.WithContext(ctx).
		Where("merchant_id = ? AND domain_id = ? AND idempotency_key = ?", merchantID, domainID, key).
		Order("created_at DESC").
		First(&refund).Error; err != nil {
		return nil, err
	}
	return &refund, nil
}

func (r *RefundRepo) ActiveTotalRawByPayment(ctx context.Context, paymentID uuid.UUID) (*big.Int, error) {
	var raw string
	err := r.db.WithContext(ctx).
		Model(&models.Refund{}).
		Select("COALESCE(SUM(amount_raw::numeric), 0)::text").
		Where("payment_id = ? AND status IN ?", paymentID, []string{
			models.RefundStatusPending,
			models.RefundStatusProcessing,
			models.RefundStatusApproved,
			models.RefundStatusSucceeded,
		}).
		Where("amount_raw ~ '^[0-9]+$'").
		Scan(&raw).Error
	if err != nil {
		return nil, err
	}
	if raw == "" {
		raw = "0"
	}
	total, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil, gorm.ErrInvalidData
	}
	return total, nil
}

func (r *RefundRepo) SumActiveAmountRawByMerchantSince(ctx context.Context, merchantID uuid.UUID, chain string, token *string, symbol string, since time.Time) (*big.Int, error) {
	type amountRow struct {
		AmountRaw string
	}
	query := r.db.WithContext(ctx).
		Model(&models.Refund{}).
		Select("amount_raw").
		Where("merchant_id = ? AND created_at >= ? AND status IN ?", merchantID, since, []string{
			models.RefundStatusPending,
			models.RefundStatusProcessing,
			models.RefundStatusApproved,
			models.RefundStatusSucceeded,
		}).
		Where("LOWER(chain) = ?", strings.ToLower(strings.TrimSpace(chain)))

	tokenValue := ""
	if token != nil {
		tokenValue = strings.TrimSpace(*token)
	}
	if tokenValue != "" {
		query = query.Where("LOWER(COALESCE(token, '')) = ?", strings.ToLower(tokenValue))
	} else {
		query = query.Where("COALESCE(token, '') = ''")
		if strings.TrimSpace(symbol) != "" {
			query = query.Where("UPPER(symbol) = ?", strings.ToUpper(strings.TrimSpace(symbol)))
		}
	}

	var rows []amountRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	total := big.NewInt(0)
	for _, row := range rows {
		value, ok := new(big.Int).SetString(strings.TrimSpace(row.AmountRaw), 10)
		if !ok || value.Sign() < 0 {
			return nil, gorm.ErrInvalidData
		}
		total.Add(total, value)
	}
	return total, nil
}

func (r *RefundRepo) ListProcessingWithTxHash(ctx context.Context, limit int) ([]models.Refund, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var refunds []models.Refund
	err := r.db.WithContext(ctx).
		Where("status = ?", models.RefundStatusProcessing).
		Where("tx_hash <> ''").
		Order("updated_at ASC").
		Limit(limit).
		Find(&refunds).Error
	return refunds, err
}

func (r *RefundRepo) ClaimPending(ctx context.Context, id uuid.UUID, reviewedBy string) (*models.Refund, error) {
	var claimed models.Refund
	now := time.Now()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status = ?", id, models.RefundStatusPending).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusPending).
			Updates(map[string]any{
				"status":      models.RefundStatusProcessing,
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
		refund.Status = models.RefundStatusProcessing
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.Error = ""
		claimed = refund
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (r *RefundRepo) ClaimPendingWithHold(ctx context.Context, id uuid.UUID, reviewedBy string, session models.PaymentSession, ledger *LedgerRepo) (*models.Refund, error) {
	return r.ClaimPendingWithHoldAndSource(ctx, id, reviewedBy, session, models.Wallet{}, "", ledger)
}

func (r *RefundRepo) ClaimPendingWithHoldAndSource(ctx context.Context, id uuid.UUID, reviewedBy string, session models.PaymentSession, sourceWallet models.Wallet, toAddress string, ledger *LedgerRepo) (*models.Refund, error) {
	if ledger == nil {
		return nil, ErrLedgerReservationRequired
	}
	var claimed models.Refund
	now := time.Now()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status = ?", id, models.RefundStatusPending).Error; err != nil {
			return err
		}
		applyRefundSessionMetadata(&refund, session)
		walletID := sourceWallet.ID
		if walletID != uuid.Nil {
			refund.WalletID = &walletID
		}
		if err := NewLedgerRepo(tx).CreateRefundHoldWithDB(ctx, tx, refund, session); err != nil {
			return err
		}
		if walletID != uuid.Nil {
			if err := NewLedgerRepo(tx).AlignRefundHoldWalletWithDB(ctx, tx, refund.ID, walletID); err != nil {
				return err
			}
		}
		updates := map[string]any{
			"status":      models.RefundStatusProcessing,
			"reviewed_by": reviewedBy,
			"reviewed_at": &now,
			"chain":       refund.Chain,
			"token":       refund.Token,
			"symbol":      refund.Symbol,
			"decimals":    refund.Decimals,
			"to_address":  strings.TrimSpace(toAddress),
			"error":       "",
		}
		if walletID != uuid.Nil {
			updates["wallet_id"] = &walletID
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusPending).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		refund.Status = models.RefundStatusProcessing
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.ToAddress = strings.TrimSpace(toAddress)
		refund.Error = ""
		claimed = refund
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (r *RefundRepo) RecordBroadcast(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	result := r.db.WithContext(ctx).Model(&models.Refund{}).
		Where("id = ? AND status = ?", id, models.RefundStatusProcessing).
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

func applyRefundSessionMetadata(refund *models.Refund, session models.PaymentSession) {
	if refund == nil || session.SelectedChainID == nil {
		return
	}
	if strings.TrimSpace(refund.Chain) == "" {
		refund.Chain = constants.ChainName(*session.SelectedChainID)
	}
	if refund.Token == nil {
		refund.Token = session.SelectedToken
	}
	if strings.TrimSpace(refund.Symbol) == "" {
		refund.Symbol = strings.ToUpper(strings.TrimSpace(session.SelectedSymbol))
	}
	if strings.TrimSpace(refund.Symbol) == "" {
		refund.Symbol = strings.ToUpper(constants.ChainName(*session.SelectedChainID))
	}
	if refund.Decimals == 0 {
		refund.Decimals = session.SelectedDecimals
	}
}

func (r *RefundRepo) MarkSucceeded(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	result := r.db.WithContext(ctx).Model(&models.Refund{}).
		Where("id = ? AND status IN ?", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).
		Updates(map[string]any{
			"status":       models.RefundStatusSucceeded,
			"reviewed_by":  reviewedBy,
			"reviewed_at":  &now,
			"tx_hash":      txHash,
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
}

func (r *RefundRepo) MarkSucceededWithLedger(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string, session models.PaymentSession, ledger *LedgerRepo) error {
	if ledger == nil {
		return ErrLedgerReservationRequired
	}
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			First(&refund, "id = ? AND status IN ?", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).Error; err != nil {
			return err
		}
		if err := NewLedgerRepo(tx).PostRefundDebitWithDB(ctx, tx, refund, session, txHash); err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status IN ?", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).
			Updates(map[string]any{
				"status":       models.RefundStatusSucceeded,
				"reviewed_by":  reviewedBy,
				"reviewed_at":  &now,
				"tx_hash":      txHash,
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

func (r *RefundRepo) MarkRejected(ctx context.Context, id uuid.UUID, reviewedBy string, reason string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusPending).
			Updates(map[string]any{
				"status":      models.RefundStatusRejected,
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
		return NewLedgerRepo(tx).VoidRefundHoldWithDB(ctx, tx, id)
	})
}

func (r *RefundRepo) MarkFailed(ctx context.Context, id uuid.UUID, reviewedBy string, errText string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status IN ?", id, []string{models.RefundStatusPending, models.RefundStatusProcessing, models.RefundStatusApproved}).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status IN ?", id, []string{models.RefundStatusPending, models.RefundStatusProcessing, models.RefundStatusApproved}).
			Updates(map[string]any{
				"status":      models.RefundStatusFailed,
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
		if strings.TrimSpace(refund.TxHash) != "" {
			return nil
		}
		return NewLedgerRepo(tx).VoidRefundHoldWithDB(ctx, tx, id)
	})
}

func (r *RefundRepo) MarkFailedFinalWithLedgerRelease(ctx context.Context, id uuid.UUID, reviewedBy string, errText string, ledger *LedgerRepo) error {
	if ledger == nil {
		return ErrLedgerReservationRequired
	}
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			First(&refund, "id = ? AND status IN ? AND tx_hash <> ''", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status IN ?", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).
			Updates(map[string]any{
				"status":       models.RefundStatusFailed,
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
		return NewLedgerRepo(tx).VoidRefundHoldWithDB(ctx, tx, id)
	})
}

func (r *RefundRepo) SetProcessingError(ctx context.Context, id uuid.UUID, errText string) error {
	result := r.db.WithContext(ctx).Model(&models.Refund{}).
		Where("id = ? AND status = ?", id, models.RefundStatusProcessing).
		Update("error", errText)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
