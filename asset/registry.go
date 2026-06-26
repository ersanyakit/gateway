package asset

import (
	"core/constants"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu            sync.RWMutex
	assets        map[constants.ChainID]map[string]Asset // chainID -> identifier -> asset
	natives       map[constants.ChainID]Asset
	definitions   map[string]AssetDefinition
	symbolAliases map[string]string // lower(alias) → lower(canonical)
}

func NewRegistry() *Registry {
	return &Registry{
		assets:        make(map[constants.ChainID]map[string]Asset),
		natives:       make(map[constants.ChainID]Asset),
		definitions:   make(map[string]AssetDefinition),
		symbolAliases: make(map[string]string),
	}
}

// RegisterAlias declares alias as equivalent to canonical for grouping, logo, and name resolution.
// e.g. RegisterAlias("WBTC", "BTC") — WBTC is treated as a BTC variant.
func (r *Registry) RegisterAlias(alias, canonical string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.symbolAliases[r.Normalize(alias)] = r.Normalize(canonical)
}

// CanonicalSymbol resolves a symbol through its alias chain and returns the canonical form.
// If the symbol has no alias it returns the symbol uppercased unchanged.
func (r *Registry) CanonicalSymbol(symbol string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return strings.ToUpper(r.canonicalKeyLocked(symbol))
}

// IsAlias reports whether symbol is registered as an alias of another symbol.
func (r *Registry) IsAlias(symbol string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.symbolAliases[r.Normalize(symbol)]
	return ok
}

// LogoURL returns the configured coin logo URL for a symbol, resolving aliases first.
func (r *Registry) LogoURL(symbol string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	def, ok := r.definitions[r.canonicalKeyLocked(symbol)]
	if !ok {
		return ""
	}
	return CoinLogoURLFromSlug(def.LogoSlug)
}

// ChainLogoURL returns the chain icon URL for a chain ID.
func (r *Registry) ChainLogoURL(chainID constants.ChainID) string {
	return ChainLogoURL(chainID)
}

func (r *Registry) Normalize(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func (r *Registry) canonicalKeyLocked(symbol string) string {
	key := r.Normalize(symbol)
	if canon, ok := r.symbolAliases[key]; ok {
		return canon
	}
	return key
}

func (r *Registry) Register(a Asset) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.registerLocked(a)
	r.registerDefinitionForAssetLocked(a)
}

func (r *Registry) RegisterDefinition(def AssetDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := r.Normalize(def.Symbol)
	if key == "" {
		return
	}
	r.definitions[key] = cloneDefinition(def)
	for _, deployment := range def.Deployments {
		if !deployment.IsEnabled() {
			continue
		}
		r.registerLocked(NewDeploymentAsset(def, deployment))
	}
}

func (r *Registry) ListDefinitions() []AssetDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]AssetDefinition, 0, len(r.definitions))
	for _, def := range r.definitions {
		list = append(list, cloneDefinition(def))
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.ToUpper(list[i].Symbol) < strings.ToUpper(list[j].Symbol)
	})
	return list
}

func (r *Registry) registerLocked(a Asset) {
	if a == nil {
		return
	}
	chainID := a.GetChainID()
	identifier := r.Normalize(a.GetIdentifier())

	if _, ok := r.assets[chainID]; !ok {
		r.assets[chainID] = make(map[string]Asset)
	}

	r.assets[chainID][identifier] = a

	if a.IsNative() {
		r.natives[chainID] = a
	}
}

func (r *Registry) registerDefinitionForAssetLocked(a Asset) {
	if a == nil {
		return
	}
	key := r.Normalize(a.GetSymbol())
	if key == "" {
		return
	}
	def := r.definitions[key]
	if def.Symbol == "" {
		def.Symbol = strings.ToUpper(strings.TrimSpace(a.GetSymbol()))
		def.Name = strings.TrimSpace(a.GetName())
		def.Type = a.GetType()
		def.Decimals = a.GetDecimals()
	}
	def.Deployments = append(def.Deployments, Deployment{
		ChainID:  a.GetChainID(),
		Symbol:   a.GetSymbol(),
		Name:     a.GetName(),
		Address:  TokenAddress(a),
		Mint:     MintAddress(a),
		Decimals: a.GetDecimals(),
		Native:   a.IsNative(),
		Enabled:  true,
		Type:     a.GetType(),
	})
	r.definitions[key] = def
}

func (r *Registry) GetNative(chainID constants.ChainID) (Asset, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	a, ok := r.natives[chainID]
	return a, ok
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
	sortAssets(list)

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
		sortAssets(result[chainID])
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
	sortAssets(list)

	return list
}

func (r *Registry) GetBySymbol(chainID constants.ChainID, symbol string) (Asset, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain, ok := r.assets[chainID]
	if !ok {
		return nil, false
	}

	var fallback Asset
	for _, a := range chain {
		if strings.EqualFold(a.GetSymbol(), symbol) {
			if a.IsNative() {
				return a, true
			}
			if fallback == nil {
				fallback = a
			}
		}
	}
	if fallback != nil {
		return fallback, true
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

func cloneDefinition(def AssetDefinition) AssetDefinition {
	out := def
	if def.Deployments != nil {
		out.Deployments = append([]Deployment(nil), def.Deployments...)
	}
	return out
}

func sortAssets(list []Asset) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].GetChainID() != list[j].GetChainID() {
			return int64(list[i].GetChainID()) < int64(list[j].GetChainID())
		}
		if !strings.EqualFold(list[i].GetSymbol(), list[j].GetSymbol()) {
			return strings.ToUpper(list[i].GetSymbol()) < strings.ToUpper(list[j].GetSymbol())
		}
		return strings.ToLower(list[i].GetIdentifier()) < strings.ToLower(list[j].GetIdentifier())
	})
}
