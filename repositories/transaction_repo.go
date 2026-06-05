package repositories

import (
	"context"
	"core/models"
	"core/types"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TransactionRepo struct {
	db *gorm.DB
}

func (r *TransactionRepo) DB() *gorm.DB {
	return r.db
}

func NewTransactionRepo(db *gorm.DB) *TransactionRepo {
	return &TransactionRepo{db: db}
}

func (r *TransactionRepo) UniqueHash(params types.TransactionParam) (string, error) {
	if params.Hash == nil {
		return "", errors.New("hash is required")
	}
	logIndexStr := ""
	if params.LogIndex != nil {
		logIndexStr = *params.LogIndex
	}

	return fmt.Sprintf("%d-%s-%s", params.ChainID, *params.Hash, logIndexStr), nil
}

func (r *TransactionRepo) Create(params types.TransactionParam) error {
	uniqueHash, err := r.UniqueHash(params)
	if err != nil {
		return err
	}
	if params.Block == nil {
		return errors.New("block number is required")
	}
	if params.From == nil || params.To == nil {
		return errors.New("from/to required")
	}
	if params.Symbol == nil {
		return errors.New("symbol is required")
	}
	if params.Amount == nil {
		return errors.New("amount is required")
	}

	return r.DB().Transaction(func(tx *gorm.DB) error {
		status := "pending"
		if params.Status != nil && *params.Status != "" {
			status = *params.Status
		}
		blockHash := ""
		if params.BlockHash != nil {
			blockHash = *params.BlockHash
		}
		var token interface{}
		if params.Token != nil {
			token = *params.Token
		}

		now := time.Now()
		txModel := &models.Transaction{
			ID:          uuid.New(),
			ChainID:     params.ChainID,
			Hash:        *params.Hash,
			LogIndex:    params.LogIndex,
			BlockNumber: *params.Block,
			Symbol:      *params.Symbol,
			Decimals:    params.Decimals,
			BlockHash:   blockHash,
			Token:       params.Token,
			FromAddress: *params.From,
			ToAddress:   *params.To,
			Amount:      *params.Amount,
			UniqueHash:  uniqueHash,
			Status:      status,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "unique_hash"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"block_hash":   blockHash,
				"token":        token,
				"symbol":       *params.Symbol,
				"decimals":     params.Decimals,
				"from_address": *params.From,
				"to_address":   *params.To,
				"amount":       *params.Amount,
				"status":       status,
				"updated_at":   now,
			}),
		}).Create(txModel).Error; err != nil {
			return err
		}

		return nil
	})
}

func (r *TransactionRepo) FindByUniqueHash(ctx context.Context, uniqueHash string) (*models.Transaction, error) {
	var txModel models.Transaction
	err := r.DB().WithContext(ctx).
		First(&txModel, "unique_hash = ?", uniqueHash).Error
	if err != nil {
		return nil, err
	}
	return &txModel, nil
}

func (r *TransactionRepo) BindWallet(ctx context.Context, uniqueHash, eventType string, wallet *models.Wallet) (*models.Transaction, error) {
	merchantID := wallet.MerchantID
	domainID := wallet.DomainID
	walletID := wallet.ID

	updates := map[string]interface{}{
		"event_type":  eventType,
		"wallet_id":   &walletID,
		"merchant_id": &merchantID,
		"domain_id":   &domainID,
		"product_id":  wallet.ProductID,
		"user_id":     wallet.UserID,
		"updated_at":  time.Now(),
	}

	if err := r.DB().WithContext(ctx).
		Model(&models.Transaction{}).
		Where("unique_hash = ?", uniqueHash).
		Updates(updates).Error; err != nil {
		return nil, err
	}

	return r.FindByUniqueHash(ctx, uniqueHash)
}

func (r *TransactionRepo) MarkWebhookAttempt(ctx context.Context, uniqueHash string, delivered bool, lastErr error) error {
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

	return r.DB().WithContext(ctx).
		Model(&models.Transaction{}).
		Where("unique_hash = ?", uniqueHash).
		Updates(updates).Error
}

func (r *TransactionRepo) ListPendingWebhooks(ctx context.Context, limit int) ([]models.Transaction, error) {
	if limit <= 0 {
		limit = 100
	}

	var transactions []models.Transaction
	err := r.DB().WithContext(ctx).
		Where("wallet_id IS NOT NULL").
		Where("webhook_sent_at IS NULL").
		Order("created_at ASC").
		Limit(limit).
		Find(&transactions).Error
	return transactions, err
}

func (r *TransactionRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.Transaction, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var transactions []models.Transaction
	err := r.DB().WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&transactions).Error
	return transactions, err
}

func (r *TransactionRepo) List(ctx context.Context, limit int) ([]models.Transaction, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var transactions []models.Transaction
	err := r.DB().WithContext(ctx).
		Order("created_at DESC").
		Limit(limit).
		Find(&transactions).Error
	return transactions, err
}

func (r *TransactionRepo) ListPage(ctx context.Context, page, limit int) ([]models.Transaction, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var total int64
	if err := r.DB().WithContext(ctx).Model(&models.Transaction{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []models.Transaction
	err := r.DB().WithContext(ctx).
		Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&rows).Error
	return rows, total, err
}

type WalletBalanceRow struct {
	WalletID uuid.UUID
	ChainID  int64
	Symbol   string
	Decimals uint8
	Deposited string
	TxCount  int64
}

type WalletLockedRow struct {
	WalletID uuid.UUID
	Locked   string
}

func (r *TransactionRepo) AllWalletDeposits(ctx context.Context) ([]WalletBalanceRow, error) {
	var rows []WalletBalanceRow
	err := r.DB().WithContext(ctx).Raw(`
		SELECT wallet_id,
		       chain_id,
		       symbol,
		       decimals,
		       SUM(amount::numeric)::text AS deposited,
		       COUNT(*) AS tx_count
		FROM transactions
		WHERE wallet_id IS NOT NULL
		  AND status = 'confirmed'
		  AND amount ~ '^[0-9]+$'
		  AND amount::numeric > 0
		GROUP BY wallet_id, chain_id, symbol, decimals
		ORDER BY wallet_id, chain_id, symbol
	`).Scan(&rows).Error
	return rows, err
}

func (r *TransactionRepo) MerchantDepositSummary(ctx context.Context, merchantID uuid.UUID) ([]models.DepositSummary, error) {
	var summaries []models.DepositSummary
	err := r.DB().WithContext(ctx).Raw(`
		SELECT
			chain_id,
			token,
			symbol,
			decimals,
			SUM(amount::numeric)::text AS amount_raw,
			COUNT(*) AS transaction_count,
			COUNT(DISTINCT user_id) AS user_count,
			MIN(created_at) AS first_deposit_at,
			MAX(created_at) AS last_deposit_at
		FROM transactions
		WHERE merchant_id = ?
			AND wallet_id IS NOT NULL
			AND status = ?
			AND amount ~ '^[0-9]+$'
			AND amount::numeric > 0
		GROUP BY chain_id, token, symbol, decimals
		ORDER BY chain_id ASC, symbol ASC
	`, merchantID, "confirmed").Scan(&summaries).Error
	return summaries, err
}

func (r *TransactionRepo) DomainDepositSummary(params types.DepositSummaryParams) ([]models.DepositSummary, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}

	selectParts := []string{
		"domain_id",
		"chain_id",
		"token",
		"symbol",
		"decimals",
		"SUM(amount::numeric)::text AS amount_raw",
		"COUNT(*) AS transaction_count",
		"COUNT(DISTINCT user_id) AS user_count",
		"MIN(created_at) AS first_deposit_at",
		"MAX(created_at) AS last_deposit_at",
	}
	groupParts := []string{"domain_id", "chain_id", "token", "symbol", "decimals"}
	orderParts := []string{"chain_id ASC", "symbol ASC"}

	if params.ShouldGroupByUser() {
		selectParts = append([]string{"product_id", "user_id"}, selectParts...)
		groupParts = append([]string{"product_id", "user_id"}, groupParts...)
		orderParts = append(orderParts, "product_id ASC", "user_id ASC")
	}

	whereParts := []string{
		"domain_id = ?",
		"wallet_id IS NOT NULL",
		"status = ?",
		"amount ~ '^[0-9]+$'",
		"amount::numeric > 0",
	}
	args := []interface{}{*params.DomainID, "confirmed"}

	if params.MerchantID != nil {
		whereParts = append(whereParts, "merchant_id = ?")
		args = append(args, *params.MerchantID)
	}
	if params.ProductID != nil {
		whereParts = append(whereParts, "product_id = ?")
		args = append(args, *params.ProductID)
	}
	if params.UserID != nil {
		whereParts = append(whereParts, "user_id = ?")
		args = append(args, *params.UserID)
	}
	if params.ChainID != nil {
		whereParts = append(whereParts, "chain_id = ?")
		args = append(args, *params.ChainID)
	}
	if params.Symbol != nil {
		whereParts = append(whereParts, "LOWER(symbol) = LOWER(?)")
		args = append(args, *params.Symbol)
	}

	query := fmt.Sprintf(
		"SELECT %s FROM transactions WHERE %s GROUP BY %s ORDER BY %s",
		strings.Join(selectParts, ", "),
		strings.Join(whereParts, " AND "),
		strings.Join(groupParts, ", "),
		strings.Join(orderParts, ", "),
	)

	var summaries []models.DepositSummary
	if err := r.DB().WithContext(params.Context).Raw(query, args...).Scan(&summaries).Error; err != nil {
		return nil, err
	}

	return summaries, nil
}
