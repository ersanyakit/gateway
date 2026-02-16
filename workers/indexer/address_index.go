package addressindex

import (
	"context"
	"strings"
	"sync"

	"core/constants"
	"core/models"

	"gorm.io/gorm"
)

type AddressIndex struct {
	mu    sync.RWMutex
	ctx   context.Context
	db    *gorm.DB
	index map[constants.ChainID]map[string]struct{}
}

func NewAddressIndex() *AddressIndex {
	return &AddressIndex{
		index: make(map[constants.ChainID]map[string]struct{}),
	}
}

func (a *AddressIndex) Load() error {
	var wallets []models.Wallet

	err := a.db.WithContext(a.ctx).
		Select(
			"id",
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

		a.addUnsafe(constants.Bitcoin, w.BitcoinAddress)
		a.addUnsafe(constants.Ethereum, w.EthereumAddress)
		a.addUnsafe(constants.Avalanche, w.AvalancheAddress)
		a.addUnsafe(constants.TRON, w.TronAddress)
		a.addUnsafe(constants.Solana, w.SolanaAddress)
		a.addUnsafe(constants.Chiliz, w.ChilizAddress)
	}

	return nil
}

func (a *AddressIndex) Exists(chainID constants.ChainID, address string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	address = strings.ToLower(address)

	chainMap, ok := a.index[chainID]
	if !ok {
		return false
	}

	_, exists := chainMap[address]
	return exists
}

func (a *AddressIndex) Add(chainID constants.ChainID, address string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.addUnsafe(chainID, address)
}

func (a *AddressIndex) addUnsafe(chainID constants.ChainID, address string) {
	if address == "" {
		return
	}

	address = strings.ToLower(address)

	if a.index[chainID] == nil {
		a.index[chainID] = make(map[string]struct{})
	}

	a.index[chainID][address] = struct{}{}
}
