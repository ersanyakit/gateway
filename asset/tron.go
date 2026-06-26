package asset

import "core/constants"

type TronAsset struct {
	BaseAsset
	ContractAddress string
}

func NewTRX(chainID constants.ChainID) *TronAsset {
	return &TronAsset{
		BaseAsset: BaseAsset{
			ChainID:   chainID,
			ChainType: ChainTron,
			Type:      AssetNative,
			Symbol:    "TRX",
			Name:      "Tron",
			Decimals:  6,
			Native:    true,
		},
	}
}

func NewTRC20(chainID constants.ChainID, contract, symbol, name string, decimals uint8) *TronAsset {
	return &TronAsset{
		BaseAsset: BaseAsset{
			ChainID:   chainID,
			ChainType: ChainTron,
			Type:      AssetTRC20,
			Symbol:    symbol,
			Name:      name,
			Decimals:  decimals,
			Native:    false,
		},
		ContractAddress: contract,
	}
}

func (t *TronAsset) GetIdentifier() string {
	if t.Native {
		return t.Symbol
	}
	return t.ContractAddress
}
