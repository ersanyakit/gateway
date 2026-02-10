package asset

import "core/constants"

type AssetType uint8

const (
	AssetNative AssetType = iota
	AssetERC20
	AssetTRC20
	AssetSPL
	AssetUTXO
)

type ChainType uint8

const (
	ChainEVM ChainType = iota
	ChainTron
	ChainSolana
	ChainBitcoin
)

type Asset interface {
	GetChainID() constants.ChainID
	GetChainType() ChainType
	GetType() AssetType
	GetSymbol() string
	GetName() string
	GetDecimals() uint8
	IsNative() bool
	GetIdentifier() string
}
