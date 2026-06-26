package asset

import "core/constants"

type BaseAsset struct {
	ChainID   constants.ChainID
	ChainType ChainType
	Type      AssetType

	Symbol   string
	Name     string
	Decimals uint8

	Native bool
}

func (b *BaseAsset) GetChainID() constants.ChainID { return b.ChainID }
func (b *BaseAsset) GetChainType() ChainType       { return b.ChainType }
func (b *BaseAsset) GetType() AssetType            { return b.Type }
func (b *BaseAsset) GetSymbol() string             { return b.Symbol }
func (b *BaseAsset) GetName() string               { return b.Name }
func (b *BaseAsset) GetDecimals() uint8            { return b.Decimals }
func (b *BaseAsset) IsNative() bool                { return b.Native }
