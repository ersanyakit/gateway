package repositories

import (
	"context"
	"core/blockchain"
	"core/constants"
	"core/models"
	"core/types"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type WalletRepo struct {
	domainRepo *DomainRepo
}

func (r *WalletRepo) DB() *gorm.DB {
	return r.domainRepo.DB()
}

func (r *WalletRepo) Domain() *DomainRepo {
	return r.domainRepo
}

func NewWalletRepo(domainRepo *DomainRepo) *WalletRepo {
	return &WalletRepo{domainRepo: domainRepo}
}

func (r *WalletRepo) GetNextHDIndex(ctx context.Context, merchantID, domainID uuid.UUID) (uint32, error) {
	return r.getNextHDIndex(ctx, r.DB(), merchantID, domainID)
}

func (r *WalletRepo) getNextHDIndex(ctx context.Context, db *gorm.DB, merchantID, domainID uuid.UUID) (uint32, error) {
	var maxIndex uint32
	err := db.WithContext(ctx).
		Model(&models.Wallet{}).
		Where("merchant_id = ? AND domain_id = ?", merchantID, domainID).
		Select("COALESCE(MAX(hd_address_id), 0)").
		Scan(&maxIndex).Error
	if err != nil {
		return 0, err
	}
	return maxIndex + 1, nil
}

func (r *WalletRepo) CreateEx(params types.WalletParams) (*models.Wallet, error) {
	return r.Create(params)
}

func (r *WalletRepo) Create(params types.WalletParams) (*models.Wallet, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}

	tx := r.DB().WithContext(params.Context).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	merchantUUID, err := uuid.Parse(*params.MerchantId)
	if err != nil {
		tx.Rollback()
		return nil, errors.New("invalid merchant id")
	}

	domainUUID, err := uuid.Parse(*params.DomainId)
	if err != nil {
		tx.Rollback()
		return nil, errors.New("invalid domain id")
	}

	domainParams := types.DomainParams{
		Context:  params.Context,
		DomainID: params.DomainId,
	}

	domain, err := r.domainRepo.FindByID(domainParams)
	if err != nil {
		tx.Rollback()
		fmt.Println("Domain bulunamadı:", err)
		return nil, err
	}
	if domain.MerchantID != merchantUUID {
		tx.Rollback()
		return nil, errors.New("domain does not belong to merchant")
	}

	lockKey := fmt.Sprintf("wallet-hd-index:%s:%s", merchantUUID.String(), domainUUID.String())
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockKey).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	var existing models.Wallet
	err = tx.
		Where(
			"merchant_id = ? AND domain_id = ? AND product_id = ? AND user_id = ?",
			merchantUUID,
			domainUUID,
			*params.ProductId,
			*params.UserId,
		).
		First(&existing).Error
	if err == nil {
		if commitErr := tx.Commit().Error; commitErr != nil {
			return nil, commitErr
		}
		if err := r.EnsureAllAddresses(params.Context, existing.ID, r.domainRepo.MerchantRepo().blockchains); err != nil {
			return nil, fmt.Errorf("wallet address backfill failed: %w", err)
		}
		return r.FindByID(params.Context, existing.ID)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		tx.Rollback()
		return nil, err
	}

	hdAccountId, err := r.getNextHDIndex(params.Context, tx, merchantUUID, domainUUID)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	walletsMap, errorsMap := r.domainRepo.MerchantRepo().blockchains.CreateHDWallets(params.Context, int(domain.HDAccountID), int(hdAccountId))
	if len(errorsMap) > 0 {
		tx.Rollback()
		errStrings := make([]string, 0, len(errorsMap))
		for chainName, err := range errorsMap {
			errStrings = append(errStrings, chainName+": "+err.Error())
		}
		return nil, fmt.Errorf("failed to create wallets: %s", strings.Join(errStrings, "; "))
	}
	requiredChains := []string{"bitcoin", "ethereum", "avalanche", "bnbchain", "base", "arbitrum", "unichain", "tron", "solana", "chiliz"}
	for _, chainName := range requiredChains {
		if walletsMap[chainName] == nil {
			tx.Rollback()
			return nil, fmt.Errorf("missing wallet for chain %s", chainName)
		}
	}

	chilizSpicyAddr := ""
	if walletsMap["chiliz-spicy"] != nil {
		chilizSpicyAddr = walletsMap["chiliz-spicy"].Address
	}

	wallet := &models.Wallet{
		ID:                 uuid.New(),
		HDAddressId:        hdAccountId,
		HDAccountID:        domain.HDAccountID,
		MerchantID:         merchantUUID,
		DomainID:           domainUUID,
		ProductID:          *params.ProductId,
		UserID:             *params.UserId,
		BitcoinAddress:     walletsMap["bitcoin"].Address,
		EthereumAddress:    walletsMap["ethereum"].Address,
		AvalancheAddress:   walletsMap["avalanche"].Address,
		BinanceAddress:     walletsMap["bnbchain"].Address,
		BaseAddress:        walletsMap["base"].Address,
		ArbitrumAddress:    walletsMap["arbitrum"].Address,
		UnichainAddress:    walletsMap["unichain"].Address,
		TronAddress:        walletsMap["tron"].Address,
		SolanaAddress:      walletsMap["solana"].Address,
		ChilizAddress:      walletsMap["chiliz"].Address,
		ChilizSpicyAddress: chilizSpicyAddr,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if err := tx.Create(wallet).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	// Fill any chain addresses not covered by the initial HD wallet map
	// (e.g. chains added after this wallet was first created).
	if err := r.EnsureAllAddresses(params.Context, wallet.ID, r.domainRepo.MerchantRepo().blockchains); err != nil {
		return nil, fmt.Errorf("wallet created but address backfill failed: %w", err)
	}

	return r.FindByID(params.Context, wallet.ID)
}

func (r *WalletRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	var wallet models.Wallet
	err := r.DB().WithContext(ctx).
		Preload("Domain").
		First(&wallet, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (r *WalletRepo) FindByDomain(ctx context.Context, merchantID, domainID, walletID uuid.UUID) (*models.Wallet, error) {
	var wallet models.Wallet
	err := r.DB().WithContext(ctx).
		Preload("Domain").
		Where("merchant_id = ? AND domain_id = ? AND id = ?", merchantID, domainID, walletID).
		First(&wallet).Error
	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (r *WalletRepo) FindByDomainOwner(ctx context.Context, merchantID, domainID uuid.UUID, productID, userID string) (*models.Wallet, error) {
	var wallet models.Wallet
	err := r.DB().WithContext(ctx).
		Preload("Domain").
		Where("merchant_id = ? AND domain_id = ? AND product_id = ? AND user_id = ?", merchantID, domainID, productID, userID).
		First(&wallet).Error
	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (r *WalletRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.Wallet, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var wallets []models.Wallet
	err := r.DB().WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&wallets).Error
	return wallets, err
}

// CreateReserveWallet creates HD index-0 wallet for the given domain. Idempotent.
func (r *WalletRepo) CreateReserveWallet(ctx context.Context, merchantID, domainID uuid.UUID, hdAccountID uint32) (*models.Wallet, error) {
	var existing models.Wallet
	if err := r.DB().WithContext(ctx).
		Where("merchant_id = ? AND domain_id = ? AND hd_address_id = 0", merchantID, domainID).
		First(&existing).Error; err == nil {
		if err := r.EnsureAllAddresses(ctx, existing.ID, r.domainRepo.MerchantRepo().blockchains); err != nil {
			return nil, fmt.Errorf("reserve wallet address backfill failed: %w", err)
		}
		return r.FindByID(ctx, existing.ID)
	}

	walletsMap, errorsMap := r.domainRepo.MerchantRepo().blockchains.CreateHDWallets(ctx, int(hdAccountID), 0)
	if len(errorsMap) > 0 {
		errs := make([]string, 0, len(errorsMap))
		for chain, e := range errorsMap {
			errs = append(errs, chain+": "+e.Error())
		}
		return nil, fmt.Errorf("reserve wallet derivation failed: %s", strings.Join(errs, "; "))
	}

	chilizSpicyAddr := ""
	if walletsMap["chiliz-spicy"] != nil {
		chilizSpicyAddr = walletsMap["chiliz-spicy"].Address
	}

	wallet := &models.Wallet{
		ID:                 uuid.New(),
		HDAddressId:        0,
		HDAccountID:        hdAccountID,
		MerchantID:         merchantID,
		DomainID:           domainID,
		ProductID:          "",
		UserID:             "",
		BitcoinAddress:     safeAddr(walletsMap, "bitcoin"),
		EthereumAddress:    safeAddr(walletsMap, "ethereum"),
		AvalancheAddress:   safeAddr(walletsMap, "avalanche"),
		BinanceAddress:     safeAddr(walletsMap, "bnbchain"),
		BaseAddress:        safeAddr(walletsMap, "base"),
		ArbitrumAddress:    safeAddr(walletsMap, "arbitrum"),
		UnichainAddress:    safeAddr(walletsMap, "unichain"),
		TronAddress:        safeAddr(walletsMap, "tron"),
		SolanaAddress:      safeAddr(walletsMap, "solana"),
		ChilizAddress:      safeAddr(walletsMap, "chiliz"),
		ChilizSpicyAddress: chilizSpicyAddr,
	}
	if err := r.DB().WithContext(ctx).Create(wallet).Error; err != nil {
		return nil, err
	}
	return wallet, nil
}

func safeAddr(m map[string]*blockchain.WalletDetails, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return v.Address
	}
	return ""
}

// FindReserveWallet returns the single system reserve wallet for a merchant (_reserve_ domain, hd_address_id = 0).
func (r *WalletRepo) FindReserveWallet(ctx context.Context, merchantID uuid.UUID) (*models.Wallet, error) {
	var wallet models.Wallet
	err := r.DB().WithContext(ctx).
		Joins("JOIN domains ON domains.id = wallets.domain_id").
		Where("wallets.merchant_id = ? AND wallets.hd_address_id = 0 AND domains.domain_url = ?", merchantID, "_reserve_").
		Preload("Domain").
		First(&wallet).Error
	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

// ListReserveByMerchant returns wallets with hd_address_id = 0 (merchant reserve wallets, one per domain).
func (r *WalletRepo) ListReserveByMerchant(ctx context.Context, merchantID uuid.UUID) ([]models.Wallet, error) {
	var wallets []models.Wallet
	err := r.DB().WithContext(ctx).
		Preload("Domain").
		Where("merchant_id = ? AND hd_address_id = 0", merchantID).
		Order("created_at ASC").
		Find(&wallets).Error
	return wallets, err
}

// ListReserveWallets returns system reserve wallets (_reserve_ domain, HD index 0) across merchants.
func (r *WalletRepo) ListReserveWallets(ctx context.Context, limit int) ([]models.Wallet, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var wallets []models.Wallet
	err := r.DB().WithContext(ctx).
		Joins("JOIN domains ON domains.id = wallets.domain_id").
		Preload("Domain").
		Where("wallets.hd_address_id = 0 AND domains.domain_url = ?", "_reserve_").
		Order("wallets.created_at ASC").
		Limit(limit).
		Find(&wallets).Error
	return wallets, err
}

func (r *WalletRepo) List(ctx context.Context, limit int) ([]models.Wallet, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var wallets []models.Wallet
	err := r.DB().WithContext(ctx).
		Preload("Merchant").
		Preload("Domain").
		Order("created_at DESC").
		Limit(limit).
		Find(&wallets).Error
	return wallets, err
}

func (r *WalletRepo) ListPage(ctx context.Context, page, limit int) ([]models.Wallet, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var total int64
	if err := r.DB().WithContext(ctx).Model(&models.Wallet{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var wallets []models.Wallet
	err := r.DB().WithContext(ctx).
		Preload("Merchant").
		Preload("Domain").
		Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&wallets).Error
	return wallets, total, err
}

func (r *WalletRepo) ListByMerchantPage(ctx context.Context, merchantID uuid.UUID, limit int, offset int) ([]models.Wallet, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var total int64
	if err := r.DB().WithContext(ctx).
		Model(&models.Wallet{}).
		Where("merchant_id = ?", merchantID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var wallets []models.Wallet
	err := r.DB().WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&wallets).Error
	return wallets, total, err
}

func (r *WalletRepo) SearchByMerchantPage(ctx context.Context, merchantID uuid.UUID, search string, limit int, offset int) ([]models.Wallet, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	base := r.DB().WithContext(ctx).Where("merchant_id = ?", merchantID)
	if search != "" {
		like := "%" + search + "%"
		base = base.Where("user_id ILIKE ? OR product_id ILIKE ?", like, like)
	}
	var total int64
	if err := base.Model(&models.Wallet{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var wallets []models.Wallet
	err := base.Order("created_at DESC").Limit(limit).Offset(offset).Find(&wallets).Error
	return wallets, total, err
}

func (r *WalletRepo) SearchByDomainPage(ctx context.Context, merchantID, domainID uuid.UUID, search string, limit int, offset int) ([]models.Wallet, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	base := r.DB().WithContext(ctx).Where("merchant_id = ? AND domain_id = ?", merchantID, domainID)
	if search != "" {
		like := "%" + search + "%"
		base = base.Where("user_id ILIKE ? OR label ILIKE ?", like, like)
	}
	var total int64
	if err := base.Model(&models.Wallet{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var wallets []models.Wallet
	err := base.Order("created_at DESC").Limit(limit).Offset(offset).Find(&wallets).Error
	return wallets, total, err
}

func (r *WalletRepo) ListStaticByDomainPage(ctx context.Context, merchantID, domainID uuid.UUID, search string, limit int, offset int) ([]models.Wallet, int64, error) {
	return r.listByDomainProductPrefixPage(ctx, merchantID, domainID, "static:", search, limit, offset)
}

func (r *WalletRepo) ListProviderByDomainPage(ctx context.Context, merchantID, domainID uuid.UUID, search string, limit int, offset int) ([]models.Wallet, int64, error) {
	return r.listByDomainProductPrefixPage(ctx, merchantID, domainID, "wallet:", search, limit, offset)
}

func (r *WalletRepo) listByDomainProductPrefixPage(ctx context.Context, merchantID, domainID uuid.UUID, productPrefix string, search string, limit int, offset int) ([]models.Wallet, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	base := r.DB().WithContext(ctx).
		Where("merchant_id = ? AND domain_id = ? AND product_id LIKE ?", merchantID, domainID, productPrefix+"%")
	if search != "" {
		like := "%" + search + "%"
		base = base.Where("user_id ILIKE ?", like)
	}
	var total int64
	if err := base.Model(&models.Wallet{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var wallets []models.Wallet
	err := base.Order("created_at DESC").Limit(limit).Offset(offset).Find(&wallets).Error
	return wallets, total, err
}

func (r *WalletRepo) FindByChainAddress(ctx context.Context, chainID constants.ChainID, address string) (*models.Wallet, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, gorm.ErrRecordNotFound
	}

	db := r.DB().WithContext(ctx).Preload("Domain")
	var wallet models.Wallet
	var err error

	switch chainID {
	case constants.Bitcoin:
		err = db.First(&wallet, "bitcoin_address = ?", address).Error
	case constants.Ethereum:
		err = db.First(&wallet, "LOWER(ethereum_address) = LOWER(?)", address).Error
	case constants.Avalanche:
		err = db.First(&wallet, "LOWER(avalanche_address) = LOWER(?)", address).Error
	case constants.Binance:
		err = db.First(&wallet, "LOWER(binance_address) = LOWER(?)", address).Error
	case constants.Base:
		err = db.First(&wallet, "LOWER(base_address) = LOWER(?)", address).Error
	case constants.Arbitrum:
		err = db.First(&wallet, "LOWER(arbitrum_address) = LOWER(?)", address).Error
	case constants.Unichain:
		err = db.First(&wallet, "LOWER(unichain_address) = LOWER(?)", address).Error
	case constants.TRON, constants.TRONTestnet:
		err = db.First(&wallet, "tron_address = ?", address).Error
	case constants.Solana:
		err = db.First(&wallet, "solana_address = ?", address).Error
	case constants.Chiliz:
		err = db.First(&wallet, "LOWER(chiliz_address) = LOWER(?)", address).Error
	case constants.ChilizSpicy:
		err = db.First(&wallet, "LOWER(chiliz_spicy_address) = LOWER(?)", address).Error
	default:
		return nil, fmt.Errorf("unsupported chain id %d", chainID)
	}

	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

// chainToFieldName maps a chain name to the GORM column name.
func chainToFieldName(chainName string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(chainName)) {
	case "bitcoin":
		return "bitcoin_address", nil
	case "ethereum":
		return "ethereum_address", nil
	case "base":
		return "base_address", nil
	case "arbitrum", "arb", "arbitrum-one":
		return "arbitrum_address", nil
	case "unichain":
		return "unichain_address", nil
	case "avalanche":
		return "avalanche_address", nil
	case "bnbchain", "binance", "bsc":
		return "binance_address", nil
	case "chiliz":
		return "chiliz_address", nil
	case "chiliz-spicy", "spicy":
		return "chiliz_spicy_address", nil
	case "tron", "tron-testnet", "trx-testnet", "shasta", "tron-shasta":
		return "tron_address", nil
	case "solana":
		return "solana_address", nil
	default:
		return "", fmt.Errorf("unknown chain: %s", chainName)
	}
}

// WalletAddressForChainID returns the address field matching a given chain ID.
func WalletAddressForChainID(wallet models.Wallet, chainID constants.ChainID) string {
	switch chainID {
	case constants.Bitcoin:
		return wallet.BitcoinAddress
	case constants.Ethereum:
		return wallet.EthereumAddress
	case constants.Avalanche:
		return wallet.AvalancheAddress
	case constants.Binance:
		return wallet.BinanceAddress
	case constants.Base:
		return wallet.BaseAddress
	case constants.Arbitrum:
		return wallet.ArbitrumAddress
	case constants.Unichain:
		return wallet.UnichainAddress
	case constants.Chiliz:
		return wallet.ChilizAddress
	case constants.ChilizSpicy:
		return wallet.ChilizSpicyAddress
	case constants.TRON, constants.TRONTestnet:
		return wallet.TronAddress
	case constants.Solana:
		return wallet.SolanaAddress
	}
	return ""
}

func walletAddressForChain(wallet models.Wallet, chainName string) string {
	switch strings.ToLower(strings.TrimSpace(chainName)) {
	case "bitcoin":
		return wallet.BitcoinAddress
	case "ethereum":
		return wallet.EthereumAddress
	case "base":
		return wallet.BaseAddress
	case "arbitrum", "arb", "arbitrum-one":
		return wallet.ArbitrumAddress
	case "unichain":
		return wallet.UnichainAddress
	case "avalanche":
		return wallet.AvalancheAddress
	case "bnbchain", "binance", "bsc":
		return wallet.BinanceAddress
	case "chiliz":
		return wallet.ChilizAddress
	case "chiliz-spicy", "spicy":
		return wallet.ChilizSpicyAddress
	case "tron", "tron-testnet", "trx-testnet", "shasta", "tron-shasta":
		return wallet.TronAddress
	case "solana":
		return wallet.SolanaAddress
	}
	return ""
}

func (r *WalletRepo) FillChainAddress(ctx context.Context, walletID uuid.UUID, chainName string, blockchains *blockchain.ChainFactory) (*models.Wallet, error) {
	field, err := chainToFieldName(chainName)
	if err != nil {
		return nil, err
	}

	wallet, err := r.FindByID(ctx, walletID)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(walletAddressForChain(*wallet, chainName)) != "" {
		return wallet, nil
	}

	chain, err := blockchains.GetChain(chainName)
	if err != nil {
		return nil, fmt.Errorf("chain not found: %s", chainName)
	}

	details, err := chain.CreateHDWallet(ctx, int(wallet.HDAccountID), int(wallet.HDAddressId))
	if err != nil {
		return nil, fmt.Errorf("HD wallet derive failed: %w", err)
	}

	if err := r.DB().WithContext(ctx).
		Model(&models.Wallet{}).
		Where("id = ?", walletID).
		Update(field, details.Address).Error; err != nil {
		return nil, err
	}

	return r.FindByID(ctx, walletID)
}

// EnsureAllAddresses derives and saves any chain addresses that are null on the given wallet.
// Safe to call repeatedly — skips chains that already have an address.
func (r *WalletRepo) EnsureAllAddresses(ctx context.Context, walletID uuid.UUID, blockchains *blockchain.ChainFactory) error {
	if blockchains == nil {
		return nil
	}
	wallet, err := r.FindByID(ctx, walletID)
	if err != nil {
		return err
	}

	for _, chainName := range blockchains.ListChains() {
		if strings.TrimSpace(walletAddressForChain(*wallet, chainName)) != "" {
			continue
		}
		field, err := chainToFieldName(chainName)
		if err != nil {
			continue
		}
		chain, err := blockchains.GetChain(chainName)
		if err != nil {
			continue
		}
		details, err := chain.CreateHDWallet(ctx, int(wallet.HDAccountID), int(wallet.HDAddressId))
		if err != nil {
			fmt.Printf("[EnsureAllAddresses] %s wallet %s: %v\n", chainName, walletID, err)
			continue
		}
		if err := r.DB().WithContext(ctx).
			Model(&models.Wallet{}).
			Where("id = ?", walletID).
			Update(field, details.Address).Error; err != nil {
			fmt.Printf("[EnsureAllAddresses] db %s wallet %s: %v\n", chainName, walletID, err)
		}
	}
	return nil
}
