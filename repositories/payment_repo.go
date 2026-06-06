package repositories

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PaymentRepo struct {
	db *gorm.DB
}

func NewPaymentRepo(db *gorm.DB) *PaymentRepo {
	return &PaymentRepo{db: db}
}

func (r *PaymentRepo) DB() *gorm.DB {
	return r.db
}

func (r *PaymentRepo) CountAll(ctx context.Context, out *int64) {
	r.db.WithContext(ctx).Model(&models.PaymentSession{}).Count(out)
}

func (r *PaymentRepo) Create(ctx context.Context, session *models.PaymentSession) error {
	if session.ID == uuid.Nil {
		session.ID = uuid.New()
	}
	if session.SessionToken == "" {
		token, err := newPaymentSessionToken()
		if err != nil {
			return err
		}
		session.SessionToken = token
	}
	if session.Status == "" {
		session.Status = models.PaymentStatusPending
	}
	now := time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now

	return r.db.WithContext(ctx).Create(session).Error
}

func (r *PaymentRepo) FindByToken(ctx context.Context, token string) (*models.PaymentSession, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Preload("Wallet").
		First(&session, "session_token = ?", token).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *PaymentRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.PaymentSession, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Preload("Wallet").
		First(&session, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *PaymentRepo) List(ctx context.Context, limit int) ([]models.PaymentSession, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var sessions []models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Merchant").
		Preload("Domain").
		Order("created_at DESC").
		Limit(limit).
		Find(&sessions).Error
	return sessions, err
}

func (r *PaymentRepo) ListPage(ctx context.Context, page, limit int) ([]models.PaymentSession, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&models.PaymentSession{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var sessions []models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Merchant").
		Preload("Domain").
		Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&sessions).Error
	return sessions, total, err
}

func (r *PaymentRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.PaymentSession, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var sessions []models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&sessions).Error
	return sessions, err
}

func (r *PaymentRepo) ListByMerchantPage(ctx context.Context, merchantID uuid.UUID, status string, page, limit int) ([]models.PaymentSession, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	base := r.db.WithContext(ctx).Where("merchant_id = ?", merchantID)
	if status != "" {
		base = base.Where("status = ?", status)
	}
	var total int64
	if err := base.Model(&models.PaymentSession{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var sessions []models.PaymentSession
	err := base.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&sessions).Error
	return sessions, total, err
}

func (r *PaymentRepo) FindByOrderID(ctx context.Context, merchantID uuid.UUID, orderID string) (*models.PaymentSession, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).
		Where("merchant_id = ? AND order_id = ?", merchantID, orderID).
		Order("created_at DESC").
		First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *PaymentRepo) StatsByMerchant(ctx context.Context, merchantID uuid.UUID) (map[string]int64, error) {
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Select("status, COUNT(*) as count").
		Where("merchant_id = ?", merchantID).
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]int64)
	for _, r := range rows {
		result[r.Status] = r.Count
	}
	return result, nil
}

func (r *PaymentRepo) SelectAsset(ctx context.Context, token string, chainID constants.ChainID, symbol string, assetToken *string, decimals uint8, amountRaw string, depositAddress string) (*models.PaymentSession, error) {
	updates := map[string]interface{}{
		"selected_chain_id":   &chainID,
		"selected_token":      assetToken,
		"selected_symbol":     symbol,
		"selected_decimals":   decimals,
		"expected_amount_raw": amountRaw,
		"deposit_address":     depositAddress,
		"status":              models.PaymentStatusAwaitingPayment,
		"updated_at":          time.Now(),
	}

	if err := r.db.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Where("session_token = ?", token).
		Where("status IN ?", []string{models.PaymentStatusPending, models.PaymentStatusAwaitingPayment}).
		Updates(updates).Error; err != nil {
		return nil, err
	}

	return r.FindByToken(ctx, token)
}

func (r *PaymentRepo) ResetSelection(ctx context.Context, token string) (*models.PaymentSession, error) {
	updates := map[string]interface{}{
		"selected_chain_id":   nil,
		"selected_token":      nil,
		"selected_symbol":     "",
		"selected_decimals":   uint8(0),
		"expected_amount_raw": "",
		"deposit_address":     "",
		"status":              models.PaymentStatusPending,
		"updated_at":          time.Now(),
	}

	if err := r.db.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Where("session_token = ?", token).
		Where("status IN ?", []string{models.PaymentStatusPending, models.PaymentStatusAwaitingPayment}).
		Updates(updates).Error; err != nil {
		return nil, err
	}

	return r.FindByToken(ctx, token)
}

func (r *PaymentRepo) Cancel(ctx context.Context, token string) (*models.PaymentSession, bool, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Domain").
			First(&session, "session_token = ?", token).Error; err != nil {
			return err
		}
		if session.Status == models.PaymentStatusPaid {
			return nil
		}
		if session.Status == models.PaymentStatusCanceled || session.Status == models.PaymentStatusExpired || session.Status == models.PaymentStatusFailed {
			return nil
		}

		session.Status = models.PaymentStatusCanceled
		session.WebhookEvent = "payment_failed"
		session.UpdatedAt = time.Now()
		return tx.Save(&session).Error
	})
	if err != nil {
		return nil, false, err
	}
	return &session, session.WebhookEvent == "payment_failed" && session.WebhookSentAt == nil, nil
}

func (r *PaymentRepo) MarkPaidByTransaction(ctx context.Context, txModel models.Transaction) (*models.PaymentSession, bool, error) {
	if txModel.WalletID == nil || txModel.Amount == "" {
		return nil, false, nil
	}
	if txModel.Status != models.TransactionStatusConfirmed || txModel.FinalizedAt == nil {
		return nil, false, nil
	}

	txAmount, ok := new(big.Int).SetString(txModel.Amount, 10)
	if !ok || txAmount.Sign() <= 0 {
		return nil, false, nil
	}

	var sessions []models.PaymentSession
	query := r.db.WithContext(ctx).
		Preload("Domain").
		Where("wallet_id = ?", *txModel.WalletID).
		Where("status = ?", models.PaymentStatusAwaitingPayment).
		Where("selected_chain_id = ?", txModel.ChainID).
		Where("selected_symbol = ?", txModel.Symbol)

	if txModel.Token == nil || *txModel.Token == "" {
		query = query.Where("selected_token IS NULL OR selected_token = ''")
	} else {
		query = query.Where("LOWER(selected_token) = LOWER(?)", *txModel.Token)
	}

	if err := query.Order("created_at ASC").Find(&sessions).Error; err != nil {
		return nil, false, err
	}

	for _, candidate := range sessions {
		expected, ok := new(big.Int).SetString(candidate.ExpectedAmountRaw, 10)
		if !ok || expected.Sign() <= 0 {
			continue
		}
		// Accept up to 0.5% underpayment to handle price-rounding at conversion time.
		// txAmount * 1000 >= expected * 995  →  txAmount >= expected * 99.5%
		threshold := new(big.Int).Mul(expected, big.NewInt(995))
		if new(big.Int).Mul(txAmount, big.NewInt(1000)).Cmp(threshold) < 0 {
			continue
		}

		var paidSession models.PaymentSession
		paid := false
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Preload("Domain").
				First(&paidSession, "id = ?", candidate.ID).Error; err != nil {
				return err
			}
			if paidSession.Status == models.PaymentStatusPaid {
				return nil
			}
			if paidSession.Status != models.PaymentStatusAwaitingPayment {
				return nil
			}

			now := time.Now()
			paidSession.Status = models.PaymentStatusPaid
			paidSession.PaidAt = &now
			paidSession.ConfirmedAt = txModel.FinalizedAt
			paidSession.ConfirmationsRequired = txModel.ConfirmationsRequired
			paidSession.TxUniqueHash = &txModel.UniqueHash
			paidSession.TxHash = &txModel.Hash
			paidSession.WebhookEvent = "payment_succeeded"
			paidSession.UpdatedAt = now
			paid = true
			return tx.Save(&paidSession).Error
		})
		if err != nil {
			return nil, false, err
		}
		if paid {
			return &paidSession, true, nil
		}
	}

	return nil, false, nil
}

func (r *PaymentRepo) MarkWebhookAttempt(ctx context.Context, sessionID uuid.UUID, delivered bool, lastErr error) error {
	updates := map[string]interface{}{
		"webhook_attempts":     gorm.Expr("webhook_attempts + 1"),
		"webhook_locked_until": nil,
		"updated_at":           time.Now(),
	}
	if delivered {
		now := time.Now()
		updates["webhook_sent_at"] = &now
		updates["webhook_last_error"] = ""
	} else if lastErr != nil {
		updates["webhook_last_error"] = lastErr.Error()
	}

	return r.db.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Where("id = ?", sessionID).
		Updates(updates).Error
}

func (r *PaymentRepo) ListPendingWebhooks(ctx context.Context, limit int) ([]models.PaymentSession, error) {
	if limit <= 0 {
		limit = 100
	}

	var sessions []models.PaymentSession
	now := time.Now()
	lockUntil := now.Add(2 * time.Minute)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Preload("Domain").
			Where("webhook_event <> ''").
			Where("webhook_sent_at IS NULL").
			Where("webhook_locked_until IS NULL OR webhook_locked_until < ?", now).
			Order("updated_at ASC").
			Limit(limit).
			Find(&sessions).Error; err != nil {
			return err
		}
		if len(sessions) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(sessions))
		for _, row := range sessions {
			ids = append(ids, row.ID)
		}
		return tx.Model(&models.PaymentSession{}).
			Where("id IN ?", ids).
			Update("webhook_locked_until", &lockUntil).Error
	})
	return sessions, err
}

func (r *PaymentRepo) MarkExpiredSessions(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Where("status IN ?", []string{models.PaymentStatusPending, models.PaymentStatusAwaitingPayment}).
		Where("expires_at IS NOT NULL").
		Where("expires_at < ?", time.Now()).
		Updates(map[string]interface{}{
			"status":        models.PaymentStatusExpired,
			"webhook_event": "payment_expired",
			"updated_at":    time.Now(),
		})
	return result.RowsAffected, result.Error
}

func newPaymentSessionToken() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func ErrPaymentSessionNotReady(session *models.PaymentSession) error {
	if session == nil {
		return errors.New("payment session not found")
	}
	return fmt.Errorf("payment session status is %s", session.Status)
}
