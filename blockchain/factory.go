package blockchain

import (
	"context"
	"core/constants"
	"errors"
	"sort"
	"sync"
)

var ErrChainNotFound = errors.New("chain not found")

type ChainFactory struct {
	mu      sync.RWMutex
	chains  map[string]Chain
	aliases map[string]string
}

func NewChainFactory() *ChainFactory {
	return &ChainFactory{
		chains:  make(map[string]Chain),
		aliases: make(map[string]string),
	}
}

func (f *ChainFactory) RegisterChain(name string, chain Chain) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chains[name] = chain
}

func (f *ChainFactory) RegisterAlias(alias string, chainName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aliases[alias] = chainName
}

func (f *ChainFactory) GetChain(name string) (Chain, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if canonicalName, ok := f.aliases[name]; ok {
		name = canonicalName
	}
	chain, ok := f.chains[name]
	if !ok {
		return nil, ErrChainNotFound
	}
	return chain, nil
}

func (f *ChainFactory) GetChainByID(chainID constants.ChainID) (Chain, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, chain := range f.chains {
		if chain.ChainID() == chainID {
			return chain, nil
		}
	}
	return nil, ErrChainNotFound
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
	sort.Strings(names)
	return names
}

func (f *ChainFactory) ListChainIDs() []constants.ChainID {
	f.mu.RLock()
	defer f.mu.RUnlock()

	ids := make([]constants.ChainID, 0, len(f.chains))
	for _, chain := range f.chains {
		ids = append(ids, chain.ChainID())
	}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})
	return ids
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

func (f *ChainFactory) CreateHDWallets(ctx context.Context, hdAccountId, hdWalletId int) (map[string]*WalletDetails, map[string]error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	wallets := make(map[string]*WalletDetails)
	errorsMap := make(map[string]error)

	for name, chain := range f.chains {
		wallet, err := chain.CreateHDWallet(ctx, hdAccountId, hdWalletId)
		if err != nil {
			errorsMap[name] = err
			continue
		}
		wallets[name] = wallet
	}
	return wallets, errorsMap
}

func (f *ChainFactory) StartAllWorkers(ctx context.Context) map[string]error {
	f.mu.RLock()
	chains := make(map[string]Chain, len(f.chains))
	for k, v := range f.chains {
		chains[k] = v
	}
	f.mu.RUnlock()
	errMap := make(map[string]error)
	for name, chain := range chains {
		if starter, ok := chain.(interface {
			StartWorkers(context.Context) error
		}); ok {
			if err := starter.StartWorkers(ctx); err != nil {
				errMap[name] = err
			}
		}
	}
	return errMap
}

func (f *ChainFactory) StopAllWorkers(ctx context.Context) map[string]error {
	if ctx == nil {
		ctx = context.Background()
	}

	f.mu.RLock()
	chains := make(map[string]Chain, len(f.chains))
	for k, v := range f.chains {
		chains[k] = v
	}
	f.mu.RUnlock()

	type stopResult struct {
		name string
		err  error
	}

	errMap := make(map[string]error)
	results := make(chan stopResult, len(chains))
	pending := make(map[string]struct{}, len(chains))

	for name, chain := range chains {
		pending[name] = struct{}{}
		go func(name string, chain Chain) {
			if stopper, ok := chain.(interface {
				StopWorkers() error
			}); ok {
				results <- stopResult{name: name, err: stopper.StopWorkers()}
				return
			}
			results <- stopResult{name: name}
		}(name, chain)
	}

	for len(pending) > 0 {
		select {
		case result := <-results:
			delete(pending, result.name)
			if result.err != nil {
				errMap[result.name] = result.err
			}
		case <-ctx.Done():
			for name := range pending {
				errMap[name] = ctx.Err()
			}
			return errMap
		}
	}

	return errMap
}
