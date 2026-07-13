package repositories

import (
	"context"
	"errors"
	"strings"
	"testing"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestWalletAddressLookupBackfillAndFindWallet(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WalletAddressLookup{}); err != nil {
		t.Fatalf("automigrate wallet lookup: %v", err)
	}

	ctx := context.Background()
	wallet := seedWalletAddressLookupWallet(t, db, "0xAbC123")
	repo := NewWalletAddressLookupRepo(db)
	count, err := repo.BackfillWallets(ctx, 100)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if count != 1 {
		t.Fatalf("backfilled wallets = %d, want 1", count)
	}

	found, err := repo.FindWallet(ctx, constants.Ethereum, "0xabc123")
	if err != nil {
		t.Fatalf("find normalized evm wallet: %v", err)
	}
	if found.ID != wallet.ID || found.Domain.ID != wallet.DomainID {
		t.Fatalf("found wallet/domain = %s/%s, want %s/%s", found.ID, found.Domain.ID, wallet.ID, wallet.DomainID)
	}

	counts, err := repo.CountByChain(ctx)
	if err != nil {
		t.Fatalf("count by chain: %v", err)
	}
	if counts[constants.Ethereum] != 1 {
		t.Fatalf("ethereum lookup count = %d, want 1", counts[constants.Ethereum])
	}
}

func TestWalletAddressLookupRejectsConflictingOwnership(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WalletAddressLookup{}); err != nil {
		t.Fatalf("automigrate wallet lookup: %v", err)
	}

	ctx := context.Background()
	first := seedWalletAddressLookupWallet(t, db, "0xAbC123")
	second := seedWalletAddressLookupWallet(t, db, "0xabc123")
	repo := NewWalletAddressLookupRepo(db)
	if err := repo.UpsertWallet(ctx, first); err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	err := repo.UpsertWallet(ctx, second)
	if !errors.Is(err, ErrWalletAddressOwnershipConflict) {
		t.Fatalf("conflict error = %v, want ErrWalletAddressOwnershipConflict", err)
	}
}

func TestWalletRepoFindByChainAddressBackfillsLookup(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WalletAddressLookup{}); err != nil {
		t.Fatalf("automigrate wallet lookup: %v", err)
	}

	wallet := seedWalletAddressLookupWallet(t, db, "0xAbC123")
	merchantRepo := NewMerchantRepo(db, nil)
	domainRepo := NewDomainRepo(merchantRepo)
	walletRepo := NewWalletRepo(domainRepo)

	found, err := walletRepo.FindByChainAddress(context.Background(), constants.Ethereum, "0xabc123")
	if err != nil {
		t.Fatalf("find by chain address: %v", err)
	}
	if found.ID != wallet.ID {
		t.Fatalf("found wallet = %s, want %s", found.ID, wallet.ID)
	}

	var rows int64
	if err := db.Model(&models.WalletAddressLookup{}).
		Where("chain_id = ? AND normalized_address = ?", constants.Ethereum, "0xabc123").
		Count(&rows).Error; err != nil {
		t.Fatalf("count lookup rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("lookup rows = %d, want 1", rows)
	}
}

func seedWalletAddressLookupWallet(t *testing.T, db *gorm.DB, evmAddress string) models.Wallet {
	t.Helper()
	merchantID := uuid.New()
	domainID := uuid.New()
	merchant := models.Merchant{ID: merchantID, Name: "Lookup Merchant " + merchantID.String(), Email: merchantID.String() + "@example.test"}
	hdAccountID := 1000 + uint32(domainID[0])<<8 + uint32(domainID[1])
	domain := models.Domain{
		ID:          domainID,
		MerchantID:  merchantID,
		DomainURL:   "lookup-" + domainID.String() + ".example.test",
		APIKey:      "key-" + domainID.String(),
		APISecret:   "secret-" + domainID.String(),
		HDAccountID: hdAccountID,
	}
	walletID := uuid.New()
	addressSuffix := strings.ReplaceAll(walletID.String(), "-", "")
	wallet := models.Wallet{
		ID:                 walletID,
		HDAccountID:        hdAccountID,
		HDAddressId:        uint32(len(evmAddress) + 20),
		MerchantID:         merchantID,
		DomainID:           domainID,
		ProductID:          "wallet:" + domainID.String(),
		UserID:             "user:" + domainID.String(),
		BitcoinAddress:     "btc-" + addressSuffix,
		EthereumAddress:    evmAddress,
		AvalancheAddress:   "avax-" + addressSuffix,
		BinanceAddress:     "bnb-" + addressSuffix,
		BaseAddress:        "base-" + addressSuffix,
		ArbitrumAddress:    "arb-" + addressSuffix,
		UnichainAddress:    "uni-" + addressSuffix,
		TronAddress:        "tron-" + addressSuffix,
		SolanaAddress:      "sol-" + addressSuffix,
		ChilizAddress:      "chz-" + addressSuffix,
		ChilizSpicyAddress: "spicy-" + addressSuffix,
	}
	if err := db.Create(&merchant).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.Create(&domain).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if err := db.Create(&wallet).Error; err != nil {
		t.Fatalf("seed wallet: %v", err)
	}
	return wallet
}
