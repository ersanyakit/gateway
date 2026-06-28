package addressindex

import (
	"context"
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
}

func NewAddressIndex(ctx context.Context, db *gorm.DB) *AddressIndex {
	return &AddressIndex{
		ctx:   ctx,
		db:    db,
		index: make(map[constants.ChainID]map[string]WalletInfo),
	}
}

func (a *AddressIndex) Load() error {
	var wallets []models.Wallet

	err := a.db.WithContext(a.ctx).
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
		Find(&wallets).Error

	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, w := range wallets {
		info := WalletInfo{
			WalletID:   w.ID,
			MerchantID: w.MerchantID,
			DomainID:   w.DomainID,
			ProductID:  w.ProductID,
			UserID:     w.UserID,
		}
		a.addUnsafe(constants.Bitcoin, w.BitcoinAddress, info)
		a.addUnsafe(constants.Ethereum, w.EthereumAddress, info)
		a.addUnsafe(constants.Avalanche, w.AvalancheAddress, info)
		a.addUnsafe(constants.Binance, w.BinanceAddress, info)
		a.addUnsafe(constants.Base, w.BaseAddress, info)
		a.addUnsafe(constants.Arbitrum, w.ArbitrumAddress, info)
		a.addUnsafe(constants.Unichain, w.UnichainAddress, info)
		a.addUnsafe(constants.TRON, w.TronAddress, info)
		a.addUnsafe(constants.TRONTestnet, w.TronAddress, info)
		a.addUnsafe(constants.Solana, w.SolanaAddress, info)
		a.addUnsafe(constants.Chiliz, w.ChilizAddress, info)
		a.addUnsafe(constants.ChilizSpicy, w.ChilizSpicyAddress, info)
	}

	return nil
}

func (a *AddressIndex) Add(chainID constants.ChainID, address string, info WalletInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.addUnsafe(chainID, address, info)
}

func (a *AddressIndex) addUnsafe(chainID constants.ChainID, address string, info WalletInfo) {
	if address == "" {
		return
	}

	address = normalizeAddress(chainID, address)
	if a.index[chainID] == nil {
		a.index[chainID] = make(map[string]WalletInfo)
	}
	a.index[chainID][address] = info
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

func normalizeAddress(chainID constants.ChainID, address string) string {
	address = strings.TrimSpace(address)
	switch chainID {
	case constants.Ethereum, constants.Avalanche, constants.Binance, constants.Base, constants.Arbitrum, constants.Unichain, constants.Chiliz, constants.ChilizSpicy:
		return strings.ToLower(address)
	default:
		return address
	}
}
