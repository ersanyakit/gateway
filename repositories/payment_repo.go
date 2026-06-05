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
		if txAmount.Cmp(expected) < 0 {
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
		"webhook_attempts": gorm.Expr("webhook_attempts + 1"),
		"updated_at":       time.Now(),
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
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Where("webhook_event <> ''").
		Where("webhook_sent_at IS NULL").
		Order("updated_at ASC").
		Limit(limit).
		Find(&sessions).Error
	return sessions, err
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
