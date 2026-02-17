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
	MerchantID uuid.UUID
	DomainID   uuid.UUID
}

type AddressIndex struct {
	mu    sync.RWMutex
	ctx   context.Context
	db    *gorm.DB
	index map[constants.ChainID]map[string]WalletInfo
}

func NewAddressIndex() *AddressIndex {
	return &AddressIndex{
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
			"bitcoin_address",
			"ethereum_address",
			"avalanche_address",
			"tron_address",
			"solana_address",
			"chiliz_address",
		).
		Find(&wallets).Error

	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, w := range wallets {
		info := WalletInfo{
			MerchantID: w.MerchantID,
			DomainID:   w.DomainID,
		}
		a.addUnsafe(constants.Bitcoin, w.BitcoinAddress, info)
		a.addUnsafe(constants.Ethereum, w.EthereumAddress, info)
		a.addUnsafe(constants.Avalanche, w.AvalancheAddress, info)
		a.addUnsafe(constants.TRON, w.TronAddress, info)
		a.addUnsafe(constants.Solana, w.SolanaAddress, info)
		a.addUnsafe(constants.Chiliz, w.ChilizAddress, info)
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

	address = strings.ToLower(address)
	if a.index[chainID] == nil {
		a.index[chainID] = make(map[string]WalletInfo)
	}
	a.index[chainID][address] = info
}

func (a *AddressIndex) Get(chainID constants.ChainID, address string) (WalletInfo, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	address = strings.ToLower(address)
	chainMap, ok := a.index[chainID]
	if !ok {
		return WalletInfo{}, false
	}

	info, exists := chainMap[address]
	return info, exists
}
