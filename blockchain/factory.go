package blockchain

import (
	"context"
	"errors"
	"sync"
)

var ErrChainNotFound = errors.New("chain not found")

type ChainFactory struct {
	mu     sync.RWMutex
	chains map[string]Chain
}

func NewChainFactory() *ChainFactory {
	return &ChainFactory{
		chains: make(map[string]Chain),
	}
}

func (f *ChainFactory) RegisterChain(name string, chain Chain) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chains[name] = chain
}

func (f *ChainFactory) GetChain(name string) (Chain, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	chain, ok := f.chains[name]
	if !ok {
		return nil, ErrChainNotFound
	}
	return chain, nil
}

func (f *ChainFactory) UnregisterChain(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.chains, name)
}

func (f *ChainFactory) ListChains() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	names := make([]string, 0, len(f.chains))
	for name := range f.chains {
		names = append(names, name)
	}
	return names
}

func (f *ChainFactory) CreateWallets(ctx context.Context) (map[string]*WalletDetails, map[string]error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	wallets := make(map[string]*WalletDetails)
	errorsMap := make(map[string]error)

	for name, chain := range f.chains {
		wallet, err := chain.Create(ctx)
		if err != nil {
			errorsMap[name] = err
			continue
		}
		wallets[name] = wallet
	}
	return wallets, errorsMap
}
