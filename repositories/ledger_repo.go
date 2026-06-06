package repositories

import (
	"context"
	"core/constants"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type LedgerRepo struct {
	db *gorm.DB
}

func NewLedgerRepo(db *gorm.DB) *LedgerRepo {
	return &LedgerRepo{db: db}
}

func (r *LedgerRepo) DB() *gorm.DB { return r.db }

func (r *LedgerRepo) amountIsPositive(amountRaw string) bool {
	value, ok := new(big.Int).SetString(amountRaw, 10)
	return ok && value.Sign() > 0
}

func (r *LedgerRepo) exists(ctx context.Context, key string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("idempotency_key = ?", key).
		Count(&count).Error
	return count > 0, err
}

func (r *LedgerRepo) CreateDepositPending(ctx context.Context, txModel models.Transaction) error {
	if txModel.MerchantID == nil || txModel.Amount == "" || !r.amountIsPositive(txModel.Amount) {
		return nil
	}
	key := "deposit-pending:" + txModel.UniqueHash
	exists, err := r.exists(ctx, key)
	if err != nil || exists {
		return err
	}
	now := time.Now()
	domainID := txModel.DomainID
	walletID := txModel.WalletID
	entries := []models.LedgerEntry{
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              domainID,
			WalletID:              walletID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       txModel.Hash,
			ChainID:               txModel.ChainID,
			Token:                 txModel.Token,
			Symbol:                txModel.Symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeDepositPending,
			Account:               models.LedgerAccountMerchantPending,
			Direction:             models.LedgerDirectionCredit,
			Status:                models.LedgerStatusPending,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             txModel.UniqueHash,
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              domainID,
			WalletID:              walletID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       txModel.Hash,
			ChainID:               txModel.ChainID,
			Token:                 txModel.Token,
			Symbol:                txModel.Symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeDepositPending,
			Account:               models.LedgerAccountPlatformClearing,
			Direction:             models.LedgerDirectionDebit,
			Status:                models.LedgerStatusPending,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             txModel.UniqueHash,
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
	}
	return r.db.WithContext(ctx).Create(&entries).Error
}

func (r *LedgerRepo) PostDepositAvailable(ctx context.Context, session models.PaymentSession, txModel models.Transaction) error {
	if txModel.MerchantID == nil || txModel.Amount == "" || !r.amountIsPositive(txModel.Amount) {
		return nil
	}
	key := "deposit-available:" + session.ID.String() + ":" + txModel.UniqueHash
	exists, err := r.exists(ctx, key)
	if err != nil || exists {
		return err
	}
	now := time.Now()
	paymentID := session.ID
	domainID := txModel.DomainID
	walletID := txModel.WalletID
	entries := []models.LedgerEntry{
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              domainID,
			WalletID:              walletID,
			PaymentID:             &paymentID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       txModel.Hash,
			ChainID:               txModel.ChainID,
			Token:                 txModel.Token,
			Symbol:                txModel.Symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeDepositAvailable,
			Account:               models.LedgerAccountMerchantAvailable,
			Direction:             models.LedgerDirectionCredit,
			Status:                models.LedgerStatusPosted,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             session.ID.String(),
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              domainID,
			WalletID:              walletID,
			PaymentID:             &paymentID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       txModel.Hash,
			ChainID:               txModel.ChainID,
			Token:                 txModel.Token,
			Symbol:                txModel.Symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeDepositAvailable,
			Account:               models.LedgerAccountMerchantPending,
			Direction:             models.LedgerDirectionDebit,
			Status:                models.LedgerStatusPosted,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             session.ID.String(),
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
	}
	return r.db.WithContext(ctx).Create(&entries).Error
}

func (r *LedgerRepo) CreateWithdrawalHold(ctx context.Context, request models.WithdrawalRequest) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.createWithdrawalHold(ctx, tx, request)
	})
}

func (r *LedgerRepo) CreateWithdrawalHoldWithDB(ctx context.Context, tx *gorm.DB, request models.WithdrawalRequest) error {
	return r.createWithdrawalHold(ctx, tx, request)
}

func (r *LedgerRepo) createWithdrawalHold(ctx context.Context, tx *gorm.DB, request models.WithdrawalRequest) error {
	key := "withdrawal-hold:" + request.ID.String()
	exists, err := r.existsWithDB(ctx, tx, key)
	if err != nil || exists {
		return err
	}
	if !r.amountIsPositive(request.AmountRaw) {
		return errors.New("withdrawal amount must be positive")
	}
	now := time.Now()
	withdrawalID := request.ID
	walletID := request.WalletID
	chainID, ok := ledgerChainIDFromName(request.Chain)
	if !ok {
		return errors.New("unsupported withdrawal chain")
	}
	symbol := strings.ToUpper(strings.TrimSpace(request.Symbol))
	if symbol == "" {
		symbol = strings.ToUpper(strings.TrimSpace(request.Chain))
	}
	if err := r.lockLedgerAsset(ctx, tx, request.MerchantID, chainID, request.Token); err != nil {
		return err
	}
	if err := r.ensureAvailableBalance(ctx, tx, request.MerchantID, chainID, request.Token, request.AmountRaw); err != nil {
		return err
	}
	entries := []models.LedgerEntry{
		{
			ID:             uuid.New(),
			MerchantID:     request.MerchantID,
			WalletID:       &walletID,
			WithdrawalID:   &withdrawalID,
			ChainID:        chainID,
			Token:          request.Token,
			Symbol:         symbol,
			Decimals:       request.Decimals,
			EntryType:      models.LedgerEntryTypeWithdrawalHold,
			Account:        models.LedgerAccountMerchantAvailable,
			Direction:      models.LedgerDirectionDebit,
			Status:         models.LedgerStatusPending,
			AmountRaw:      request.AmountRaw,
			IdempotencyKey: key,
			Reference:      request.ID.String(),
			PostedAt:       &now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             uuid.New(),
			MerchantID:     request.MerchantID,
			WalletID:       &walletID,
			WithdrawalID:   &withdrawalID,
			ChainID:        chainID,
			Token:          request.Token,
			Symbol:         symbol,
			Decimals:       request.Decimals,
			EntryType:      models.LedgerEntryTypeWithdrawalHold,
			Account:        models.LedgerAccountWithdrawalTransit,
			Direction:      models.LedgerDirectionCredit,
			Status:         models.LedgerStatusPending,
			AmountRaw:      request.AmountRaw,
			IdempotencyKey: key,
			Reference:      request.ID.String(),
			PostedAt:       &now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	return tx.WithContext(ctx).Create(&entries).Error
}

func (r *LedgerRepo) PostWithdrawalDebit(ctx context.Context, request models.WithdrawalRequest, txHash string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.postWithdrawalDebit(ctx, tx, request, txHash)
	})
}

func (r *LedgerRepo) PostWithdrawalDebitWithDB(ctx context.Context, tx *gorm.DB, request models.WithdrawalRequest, txHash string) error {
	return r.postWithdrawalDebit(ctx, tx, request, txHash)
}

func (r *LedgerRepo) postWithdrawalDebit(ctx context.Context, tx *gorm.DB, request models.WithdrawalRequest, txHash string) error {
	key := "withdrawal-debit:" + request.ID.String()
	exists, err := r.existsWithDB(ctx, tx, key)
	if err != nil || exists {
		return err
	}
	if !r.amountIsPositive(request.AmountRaw) {
		return errors.New("withdrawal amount must be positive")
	}
	now := time.Now()
	withdrawalID := request.ID
	walletID := request.WalletID
	chainID, ok := ledgerChainIDFromName(request.Chain)
	if !ok {
		return errors.New("unsupported withdrawal chain")
	}
	symbol := strings.ToUpper(strings.TrimSpace(request.Symbol))
	if symbol == "" {
		symbol = strings.ToUpper(strings.TrimSpace(request.Chain))
	}
	entries := []models.LedgerEntry{
		{
			ID:              uuid.New(),
			MerchantID:      request.MerchantID,
			WalletID:        &walletID,
			WithdrawalID:    &withdrawalID,
			TransactionHash: txHash,
			ChainID:         chainID,
			Token:           request.Token,
			Symbol:          symbol,
			Decimals:        request.Decimals,
			EntryType:       models.LedgerEntryTypeWithdrawalDebit,
			Account:         models.LedgerAccountWithdrawalTransit,
			Direction:       models.LedgerDirectionDebit,
			Status:          models.LedgerStatusPosted,
			AmountRaw:       request.AmountRaw,
			IdempotencyKey:  key,
			Reference:       request.ID.String(),
			PostedAt:        &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              uuid.New(),
			MerchantID:      request.MerchantID,
			WalletID:        &walletID,
			WithdrawalID:    &withdrawalID,
			TransactionHash: txHash,
			ChainID:         chainID,
			Token:           request.Token,
			Symbol:          symbol,
			Decimals:        request.Decimals,
			EntryType:       models.LedgerEntryTypeWithdrawalDebit,
			Account:         models.LedgerAccountPlatformClearing,
			Direction:       models.LedgerDirectionCredit,
			Status:          models.LedgerStatusPosted,
			AmountRaw:       request.AmountRaw,
			IdempotencyKey:  key,
			Reference:       request.ID.String(),
			PostedAt:        &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}
	return tx.WithContext(ctx).Create(&entries).Error
}

func (r *LedgerRepo) VoidWithdrawalHold(ctx context.Context, withdrawalID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.voidWithdrawalHold(ctx, tx, withdrawalID)
	})
}

func (r *LedgerRepo) VoidWithdrawalHoldWithDB(ctx context.Context, tx *gorm.DB, withdrawalID uuid.UUID) error {
	return r.voidWithdrawalHold(ctx, tx, withdrawalID)
}

func (r *LedgerRepo) voidWithdrawalHold(ctx context.Context, tx *gorm.DB, withdrawalID uuid.UUID) error {
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("withdrawal_id = ? AND entry_type = ? AND status = ?", withdrawalID, models.LedgerEntryTypeWithdrawalHold, models.LedgerStatusPending).
		Updates(map[string]any{
			"status":     models.LedgerStatusVoided,
			"voided_at":  &now,
			"updated_at": now,
		}).Error
}

func (r *LedgerRepo) existsWithDB(ctx context.Context, tx *gorm.DB, key string) (bool, error) {
	var count int64
	err := tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("idempotency_key = ?", key).
		Count(&count).Error
	return count > 0, err
}

func (r *LedgerRepo) lockLedgerAsset(ctx context.Context, tx *gorm.DB, merchantID uuid.UUID, chainID constants.ChainID, token *string) error {
	tokenKey := "native"
	if token != nil && strings.TrimSpace(*token) != "" {
		tokenKey = strings.ToLower(strings.TrimSpace(*token))
	}
	lockKey := fmt.Sprintf("ledger-balance:%s:%d:%s", merchantID.String(), chainID, tokenKey)
	return tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockKey).Error
}

func (r *LedgerRepo) ensureAvailableBalance(ctx context.Context, tx *gorm.DB, merchantID uuid.UUID, chainID constants.ChainID, token *string, amountRaw string) error {
	requested, ok := new(big.Int).SetString(amountRaw, 10)
	if !ok || requested.Sign() <= 0 {
		return errors.New("withdrawal amount must be positive")
	}

	query := tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Select("COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END), 0)::text").
		Where("merchant_id = ? AND chain_id = ? AND account = ? AND status IN ?", merchantID, chainID, models.LedgerAccountMerchantAvailable, []string{models.LedgerStatusPending, models.LedgerStatusPosted}).
		Where("amount_raw ~ '^[0-9]+$'")
	if token == nil || strings.TrimSpace(*token) == "" {
		query = query.Where("token IS NULL OR token = ''")
	} else {
		query = query.Where("LOWER(token) = LOWER(?)", strings.TrimSpace(*token))
	}

	var raw string
	if err := query.Scan(&raw).Error; err != nil {
		return err
	}
	if raw == "" {
		raw = "0"
	}
	available, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return fmt.Errorf("invalid available balance: %s", raw)
	}
	if available.Cmp(requested) < 0 {
		return fmt.Errorf("insufficient available balance: available=%s amount=%s", available.String(), requested.String())
	}
	return nil
}

func (r *LedgerRepo) PostRefundDebit(ctx context.Context, refund models.Refund, session models.PaymentSession, txHash string) error {
	return r.PostRefundDebitWithDB(ctx, r.db, refund, session, txHash)
}

func (r *LedgerRepo) PostRefundDebitWithDB(ctx context.Context, tx *gorm.DB, refund models.Refund, session models.PaymentSession, txHash string) error {
	key := "refund-debit:" + refund.ID.String()
	exists, err := r.existsWithDB(ctx, tx, key)
	if err != nil || exists {
		return err
	}
	if !r.amountIsPositive(refund.AmountRaw) {
		return errors.New("refund amount must be positive")
	}
	now := time.Now()
	refundID := refund.ID
	paymentID := session.ID
	walletID := session.WalletID
	chainID := constants.ChainID(0)
	if session.SelectedChainID != nil {
		chainID = *session.SelectedChainID
	}
	entries := []models.LedgerEntry{
		{
			ID:              uuid.New(),
			MerchantID:      refund.MerchantID,
			DomainID:        &refund.DomainID,
			WalletID:        &walletID,
			PaymentID:       &paymentID,
			RefundID:        &refundID,
			TransactionHash: txHash,
			ChainID:         chainID,
			Token:           session.SelectedToken,
			Symbol:          session.SelectedSymbol,
			Decimals:        session.SelectedDecimals,
			EntryType:       models.LedgerEntryTypeRefundDebit,
			Account:         models.LedgerAccountMerchantAvailable,
			Direction:       models.LedgerDirectionDebit,
			Status:          models.LedgerStatusPosted,
			AmountRaw:       refund.AmountRaw,
			IdempotencyKey:  key,
			Reference:       refund.ID.String(),
			PostedAt:        &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              uuid.New(),
			MerchantID:      refund.MerchantID,
			DomainID:        &refund.DomainID,
			WalletID:        &walletID,
			PaymentID:       &paymentID,
			RefundID:        &refundID,
			TransactionHash: txHash,
			ChainID:         chainID,
			Token:           session.SelectedToken,
			Symbol:          session.SelectedSymbol,
			Decimals:        session.SelectedDecimals,
			EntryType:       models.LedgerEntryTypeRefundDebit,
			Account:         models.LedgerAccountPlatformClearing,
			Direction:       models.LedgerDirectionCredit,
			Status:          models.LedgerStatusPosted,
			AmountRaw:       refund.AmountRaw,
			IdempotencyKey:  key,
			Reference:       refund.ID.String(),
			PostedAt:        &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}
	return tx.WithContext(ctx).Create(&entries).Error
}

func ledgerChainIDFromName(name string) (constants.ChainID, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bitcoin", "btc":
		return constants.Bitcoin, true
	case "ethereum", "eth":
		return constants.Ethereum, true
	case "base":
		return constants.Base, true
	case "arbitrum", "arb", "arbitrum-one":
		return constants.Arbitrum, true
	case "bnbchain", "bsc", "binance":
		return constants.Binance, true
	case "unichain":
		return constants.Unichain, true
	case "avalanche", "avax":
		return constants.Avalanche, true
	case "chiliz", "chz":
		return constants.Chiliz, true
	case "chiliz-spicy", "spicy":
		return constants.ChilizSpicy, true
	case "solana", "sol":
		return constants.Solana, true
	case "tron", "trx":
		return constants.TRON, true
	default:
		return 0, false
	}
}

type LedgerBalanceRow struct {
	MerchantID uuid.UUID
	ChainID    int64
	Token      *string
	Symbol     string
	Decimals   uint8
	Account    string
	BalanceRaw string
}

func (r *LedgerRepo) MerchantBalances(ctx context.Context, merchantID uuid.UUID) ([]LedgerBalanceRow, error) {
	var rows []LedgerBalanceRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT merchant_id,
		       chain_id,
		       token,
		       symbol,
		       decimals,
		       account,
		       SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END)::text AS balance_raw
		FROM ledger_entries
		WHERE merchant_id = ?
		  AND status IN ('pending', 'posted')
		  AND amount_raw ~ '^[0-9]+$'
		GROUP BY merchant_id, chain_id, token, symbol, decimals, account
		ORDER BY chain_id ASC, symbol ASC, account ASC
	`, merchantID).Scan(&rows).Error
	return rows, err
}
