package addressindex

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type WalletInfo struct {
	WalletID   uuid.UUID
	MerchantID uuid.UUID
	DomainID   uuid.UUID
	ProductID  string
	UserID     string
}

type AddressIndex struct {
	mu    sync.RWMutex
	ctx   context.Context
	db    *gorm.DB
	index map[constants.ChainID]map[string]WalletInfo
	ready bool
}

func NewAddressIndex(ctx context.Context, db *gorm.DB) *AddressIndex {
	return &AddressIndex{
		ctx:   ctx,
		db:    db,
		index: make(map[constants.ChainID]map[string]WalletInfo),
	}
}

func (a *AddressIndex) Load() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	limit := addressIndexPreloadLimit()
	if limit == 0 {
		a.index = make(map[constants.ChainID]map[string]WalletInfo)
		a.ready = false
		return nil
	}

	nextIndex := make(map[constants.ChainID]map[string]WalletInfo)

	if err := a.loadLegacyWalletRows(nextIndex, limit); err != nil {
		return err
	}
	if a.db.Migrator().HasTable(&models.WalletAddressLookup{}) {
		if err := a.loadLookupRows(nextIndex, limit); err != nil {
			return err
		}
	}
	if a.db.Migrator().HasTable(&models.WalletAddress{}) {
		if err := a.loadWalletAddressRows(nextIndex, limit); err != nil {
			return err
		}
	}

	a.index = nextIndex
	a.ready = limit < 0

	return nil
}

// Ready reports whether the index contains the complete ownership set and a
// negative lookup can therefore be treated as authoritative. A configured
// preload limit is intentionally fail-closed because it can truncate owners.
func (a *AddressIndex) Ready() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ready
}

// AddWallet synchronously publishes every chain address of a newly created or
// backfilled wallet so listener filtering never races wallet creation.
func (a *AddressIndex) AddWallet(wallet models.Wallet) {
	if a == nil {
		return
	}
	info := WalletInfo{
		WalletID:   wallet.ID,
		MerchantID: wallet.MerchantID,
		DomainID:   wallet.DomainID,
		ProductID:  wallet.ProductID,
		UserID:     wallet.UserID,
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	addAddressIndexUnsafe(a.index, constants.Bitcoin, wallet.BitcoinAddress, info)
	addAddressIndexUnsafe(a.index, constants.Ethereum, wallet.EthereumAddress, info)
	addAddressIndexUnsafe(a.index, constants.Avalanche, wallet.AvalancheAddress, info)
	addAddressIndexUnsafe(a.index, constants.Binance, wallet.BinanceAddress, info)
	addAddressIndexUnsafe(a.index, constants.Base, wallet.BaseAddress, info)
	addAddressIndexUnsafe(a.index, constants.Arbitrum, wallet.ArbitrumAddress, info)
	addAddressIndexUnsafe(a.index, constants.Unichain, wallet.UnichainAddress, info)
	addAddressIndexUnsafe(a.index, constants.TRON, wallet.TronAddress, info)
	addAddressIndexUnsafe(a.index, constants.TRONTestnet, wallet.TronAddress, info)
	addAddressIndexUnsafe(a.index, constants.Solana, wallet.SolanaAddress, info)
	addAddressIndexUnsafe(a.index, constants.Chiliz, wallet.ChilizAddress, info)
	addAddressIndexUnsafe(a.index, constants.ChilizSpicy, wallet.ChilizSpicyAddress, info)
}

func (a *AddressIndex) loadLegacyWalletRows(index map[constants.ChainID]map[string]WalletInfo, limit int) error {
	var wallets []models.Wallet
	query := a.db.WithContext(a.ctx).
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
		)
	if err := applyAddressIndexPreloadLimit(query, limit).Find(&wallets).Error; err != nil {
		return err
	}
	for _, w := range wallets {
		info := WalletInfo{
			WalletID:   w.ID,
			MerchantID: w.MerchantID,
			DomainID:   w.DomainID,
			ProductID:  w.ProductID,
			UserID:     w.UserID,
		}
		addAddressIndexUnsafe(index, constants.Bitcoin, w.BitcoinAddress, info)
		addAddressIndexUnsafe(index, constants.Ethereum, w.EthereumAddress, info)
		addAddressIndexUnsafe(index, constants.Avalanche, w.AvalancheAddress, info)
		addAddressIndexUnsafe(index, constants.Binance, w.BinanceAddress, info)
		addAddressIndexUnsafe(index, constants.Base, w.BaseAddress, info)
		addAddressIndexUnsafe(index, constants.Arbitrum, w.ArbitrumAddress, info)
		addAddressIndexUnsafe(index, constants.Unichain, w.UnichainAddress, info)
		addAddressIndexUnsafe(index, constants.TRON, w.TronAddress, info)
		addAddressIndexUnsafe(index, constants.TRONTestnet, w.TronAddress, info)
		addAddressIndexUnsafe(index, constants.Solana, w.SolanaAddress, info)
		addAddressIndexUnsafe(index, constants.Chiliz, w.ChilizAddress, info)
		addAddressIndexUnsafe(index, constants.ChilizSpicy, w.ChilizSpicyAddress, info)
	}
	return nil
}

func (a *AddressIndex) loadWalletAddressRows(index map[constants.ChainID]map[string]WalletInfo, limit int) error {
	var rows []models.WalletAddress
	query := a.db.WithContext(a.ctx).
		Where("lifecycle_status NOT IN ?", terminalWalletAddressStatuses()).
		Order("updated_at ASC")
	if err := applyAddressIndexPreloadLimit(query, limit).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		addAddressIndexUnsafe(index, row.ChainID, row.NormalizedAddress, WalletInfo{
			WalletID:   row.WalletID,
			MerchantID: row.MerchantID,
			DomainID:   row.DomainID,
			ProductID:  row.ProductID,
			UserID:     row.UserID,
		})
	}
	return nil
}

func (a *AddressIndex) loadLookupRows(index map[constants.ChainID]map[string]WalletInfo, limit int) error {
	var rows []models.WalletAddressLookup
	query := a.db.WithContext(a.ctx).Order("updated_at ASC")
	if err := applyAddressIndexPreloadLimit(query, limit).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		addAddressIndexUnsafe(index, row.ChainID, row.NormalizedAddress, WalletInfo{
			WalletID:   row.WalletID,
			MerchantID: row.MerchantID,
			DomainID:   row.DomainID,
			ProductID:  row.ProductID,
			UserID:     row.UserID,
		})
	}
	return nil
}

func (a *AddressIndex) Add(chainID constants.ChainID, address string, info WalletInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.addUnsafe(chainID, address, info)
}

func (a *AddressIndex) addUnsafe(chainID constants.ChainID, address string, info WalletInfo) {
	addAddressIndexUnsafe(a.index, chainID, address, info)
}

func addAddressIndexUnsafe(index map[constants.ChainID]map[string]WalletInfo, chainID constants.ChainID, address string, info WalletInfo) {
	if address == "" {
		return
	}

	address = normalizeAddress(chainID, address)
	if index[chainID] == nil {
		index[chainID] = make(map[string]WalletInfo)
	}
	index[chainID][address] = info
}

func (a *AddressIndex) Get(chainID constants.ChainID, address string) (WalletInfo, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	address = normalizeAddress(chainID, address)
	chainMap, ok := a.index[chainID]
	if !ok {
		return WalletInfo{}, false
	}

	info, exists := chainMap[address]
	return info, exists
}

// SameMerchant reports whether both addresses are platform-owned by the same
// merchant. Same-merchant movements are internal custody transfers, not new
// merchant deposits.
func (a *AddressIndex) SameMerchant(chainID constants.ChainID, fromAddress, toAddress string) bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	chainMap := a.index[chainID]
	from, fromOwned := chainMap[normalizeAddress(chainID, fromAddress)]
	to, toOwned := chainMap[normalizeAddress(chainID, toAddress)]
	return fromOwned && toOwned && from.MerchantID != uuid.Nil && from.MerchantID == to.MerchantID
}

func normalizeAddress(chainID constants.ChainID, address string) string {
	address = strings.TrimSpace(address)
	switch chainID {
	case constants.Ethereum, constants.Avalanche, constants.Binance, constants.Base, constants.Arbitrum, constants.Unichain, constants.Chiliz, constants.ChilizSpicy:
		return strings.ToLower(address)
	default:
		return address
	}
}

func addressIndexPreloadLimit() int {
	raw := strings.TrimSpace(os.Getenv("ADDRESS_INDEX_PRELOAD_LIMIT"))
	if raw == "" {
		return -1
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return -1
	}
	return limit
}

func applyAddressIndexPreloadLimit(query *gorm.DB, limit int) *gorm.DB {
	if limit > 0 {
		return query.Limit(limit)
	}
	return query
}

func terminalWalletAddressStatuses() []string {
	return []string{
		models.WalletAddressStatusExpired,
		models.WalletAddressStatusReleased,
		models.WalletAddressStatusRetired,
	}
}
