package asset

import (
	"core/constants"
	"strings"
	"sync"
)

type Registry struct {
	mu     sync.RWMutex
	assets map[constants.ChainID]map[string]Asset // chainID -> identifier -> asset
}

func NewRegistry() *Registry {
	return &Registry{
		assets: make(map[constants.ChainID]map[string]Asset),
	}
}

func (r *Registry) Normalize(id string) string {
	return strings.ToLower(id)
}

func (r *Registry) Register(a Asset) {
	r.mu.Lock()
	defer r.mu.Unlock()

	chainID := a.GetChainID()
	identifier := r.Normalize(a.GetIdentifier())

	if _, ok := r.assets[chainID]; !ok {
		r.assets[chainID] = make(map[string]Asset)
	}

	r.assets[chainID][identifier] = a
}

func (r *Registry) Get(chainID constants.ChainID, identifier string) (Asset, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain, ok := r.assets[chainID]
	if !ok {
		return nil, false
	}

	a, ok := chain[r.Normalize(identifier)]
	return a, ok
}

func (r *Registry) Exists(chainID constants.ChainID, identifier string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain, ok := r.assets[chainID]
	if !ok {
		return false
	}

	_, ok = chain[r.Normalize(identifier)]
	return ok
}

func (r *Registry) ListByChain(chainID constants.ChainID) []Asset {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain, ok := r.assets[chainID]
	if !ok {
		return nil
	}

	list := make([]Asset, 0, len(chain))
	for _, a := range chain {
		list = append(list, a)
	}

	return list
}

func (r *Registry) ListAllGrouped() map[constants.ChainID][]Asset {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[constants.ChainID][]Asset)

	for chainID, chainAssets := range r.assets {
		for _, a := range chainAssets {
			result[chainID] = append(result[chainID], a)
		}
	}

	return result
}

func (r *Registry) ListAll() []Asset {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []Asset

	for _, chainAssets := range r.assets {
		for _, a := range chainAssets {
			list = append(list, a)
		}
	}

	return list
}

func (r *Registry) GetBySymbol(chainID constants.ChainID, symbol string) (Asset, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain, ok := r.assets[chainID]
	if !ok {
		return nil, false
	}

	for _, a := range chain {
		if strings.EqualFold(a.GetSymbol(), symbol) {
			return a, true
		}
	}

	return nil, false
}

func (r *Registry) IterateByChain(chainID constants.ChainID, fn func(Asset)) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain, ok := r.assets[chainID]
	if !ok {
		return
	}

	for _, a := range chain {
		fn(a)
	}
}
