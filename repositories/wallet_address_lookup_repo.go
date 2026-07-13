package repositories

import (
	"context"
	"core/constants"
	"core/models"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrWalletAddressOwnershipConflict = errors.New("wallet address ownership conflict")

type WalletAddressLookupRepo struct {
	db *gorm.DB
}

func NewWalletAddressLookupRepo(db *gorm.DB) *WalletAddressLookupRepo {
	return &WalletAddressLookupRepo{db: db}
}

func (r *WalletAddressLookupRepo) DB() *gorm.DB { return r.db }

func NormalizeWalletLookupAddress(chainID constants.ChainID, address string) string {
	address = strings.TrimSpace(address)
	switch chainID {
	case constants.Ethereum, constants.Avalanche, constants.Binance, constants.Base, constants.Arbitrum, constants.Unichain, constants.Chiliz, constants.ChilizSpicy:
		return strings.ToLower(address)
	default:
		return address
	}
}

func (r *WalletAddressLookupRepo) UpsertWallet(ctx context.Context, wallet models.Wallet) error {
	if r == nil || r.db == nil {
		return errors.New("wallet address lookup repository is not configured")
	}
	rows := WalletAddressLookupRows(wallet)
	if len(rows) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			if err := upsertWalletAddressLookupRow(tx, row); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *WalletAddressLookupRepo) BackfillWallets(ctx context.Context, batchSize int) (int, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("wallet address lookup repository is not configured")
	}
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 500
	}
	backfilled := 0
	var wallets []models.Wallet
	err := r.db.WithContext(ctx).
		Select(
			"id",
			"merchant_id",
			"domain_id",
			"product_id",
			"user_id",
			"bitcoin_address",
			"ethereum_address",
			"avalanche_address",
			"binance_address",
			"base_address",
			"arbitrum_address",
			"unichain_address",
			"tron_address",
			"solana_address",
			"chiliz_address",
			"chiliz_spicy_address",
		).
		FindInBatches(&wallets, batchSize, func(_ *gorm.DB, _ int) error {
			writer := NewWalletAddressLookupRepo(r.db)
			for _, wallet := range wallets {
				if err := writer.UpsertWallet(ctx, wallet); err != nil {
					return err
				}
				backfilled++
			}
			return nil
		}).Error
	return backfilled, err
}

func (r *WalletAddressLookupRepo) FindWallet(ctx context.Context, chainID constants.ChainID, address string) (*models.Wallet, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("wallet address lookup repository is not configured")
	}
	normalized := NormalizeWalletLookupAddress(chainID, address)
	if normalized == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var lookup models.WalletAddressLookup
	if err := r.db.WithContext(ctx).
		Where("chain_id = ? AND normalized_address = ?", chainID, normalized).
		First(&lookup).Error; err != nil {
		return nil, err
	}
	var wallet models.Wallet
	if err := r.db.WithContext(ctx).
		Preload("Domain").
		First(&wallet, "id = ?", lookup.WalletID).Error; err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (r *WalletAddressLookupRepo) CountByChain(ctx context.Context) (map[constants.ChainID]int64, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("wallet address lookup repository is not configured")
	}
	type row struct {
		ChainID constants.ChainID
		Count   int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&models.WalletAddressLookup{}).
		Select("chain_id, COUNT(*) AS count").
		Group("chain_id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[constants.ChainID]int64, len(rows))
	for _, row := range rows {
		counts[row.ChainID] = row.Count
	}
	return counts, nil
}

func WalletAddressLookupRows(wallet models.Wallet) []models.WalletAddressLookup {
	candidates := []struct {
		chainID constants.ChainID
		address string
	}{
		{constants.Bitcoin, wallet.BitcoinAddress},
		{constants.Ethereum, wallet.EthereumAddress},
		{constants.Avalanche, wallet.AvalancheAddress},
		{constants.Binance, wallet.BinanceAddress},
		{constants.Base, wallet.BaseAddress},
		{constants.Arbitrum, wallet.ArbitrumAddress},
		{constants.Unichain, wallet.UnichainAddress},
		{constants.TRON, wallet.TronAddress},
		{constants.TRONTestnet, wallet.TronAddress},
		{constants.Solana, wallet.SolanaAddress},
		{constants.Chiliz, wallet.ChilizAddress},
		{constants.ChilizSpicy, wallet.ChilizSpicyAddress},
	}
	rows := make([]models.WalletAddressLookup, 0, len(candidates))
	for _, candidate := range candidates {
		address := strings.TrimSpace(candidate.address)
		normalized := NormalizeWalletLookupAddress(candidate.chainID, address)
		if normalized == "" {
			continue
		}
		rows = append(rows, models.WalletAddressLookup{
			ID:                uuid.New(),
			ChainID:           candidate.chainID,
			ChainName:         constants.ChainName(candidate.chainID),
			Address:           address,
			NormalizedAddress: normalized,
			Asset:             "native",
			MerchantID:        wallet.MerchantID,
			DomainID:          wallet.DomainID,
			WalletID:          wallet.ID,
			ProductID:         wallet.ProductID,
			UserID:            wallet.UserID,
			Source:            "wallet_columns",
		})
	}
	return rows
}

func upsertWalletAddressLookupRow(tx *gorm.DB, row models.WalletAddressLookup) error {
	now := time.Now().UTC()
	if row.ID == uuid.Nil {
		row.ID = uuid.New()
	}
	if row.Asset == "" {
		row.Asset = "native"
	}
	if row.Source == "" {
		row.Source = "wallet_columns"
	}
	if row.ChainName == "" {
		row.ChainName = constants.ChainName(row.ChainID)
	}
	row.Address = strings.TrimSpace(row.Address)
	row.NormalizedAddress = NormalizeWalletLookupAddress(row.ChainID, row.NormalizedAddress)
	if row.NormalizedAddress == "" {
		row.NormalizedAddress = NormalizeWalletLookupAddress(row.ChainID, row.Address)
	}
	if row.NormalizedAddress == "" {
		return nil
	}

	var existing models.WalletAddressLookup
	err := tx.Where("chain_id = ? AND normalized_address = ?", row.ChainID, row.NormalizedAddress).
		First(&existing).Error
	if err == nil {
		if existing.WalletID != row.WalletID {
			return fmt.Errorf(
				"%w: chain=%s normalized_address=%s existing_wallet=%s new_wallet=%s",
				ErrWalletAddressOwnershipConflict,
				constants.ChainName(row.ChainID),
				row.NormalizedAddress,
				existing.WalletID,
				row.WalletID,
			)
		}
		return tx.Model(&existing).Updates(map[string]any{
			"chain_name":         row.ChainName,
			"address":            row.Address,
			"asset":              row.Asset,
			"merchant_id":        row.MerchantID,
			"domain_id":          row.DomainID,
			"product_id":         row.ProductID,
			"user_id":            row.UserID,
			"source":             row.Source,
			"normalized_address": row.NormalizedAddress,
			"updated_at":         now,
		}).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	row.CreatedAt = now
	row.UpdatedAt = now
	return tx.Create(&row).Error
}
