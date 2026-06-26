package asset

import "core/constants"

type EVMAsset struct {
	BaseAsset
	ContractAddress string
}

func NewEVMNative(chainID constants.ChainID, symbol, name string, decimals uint8) *EVMAsset {
	return &EVMAsset{
		BaseAsset: BaseAsset{
			ChainID:   chainID,
			ChainType: ChainEVM,
			Type:      AssetNative,
			Symbol:    symbol,
			Name:      name,
			Decimals:  decimals,
			Native:    true,
		},
		ContractAddress: "0x0000000000000000000000000000000000000000",
	}
}

func NewERC20(chainID constants.ChainID, contract, symbol, name string, decimals uint8) *EVMAsset {
	return &EVMAsset{
		BaseAsset: BaseAsset{
			ChainID:   chainID,
			ChainType: ChainEVM,
			Type:      AssetERC20,
			Symbol:    symbol,
			Name:      name,
			Decimals:  decimals,
			Native:    false,
		},
		ContractAddress: contract,
	}
}

func (e *EVMAsset) GetIdentifier() string {
	if e.Native {
		return e.Symbol
	}
	return e.ContractAddress
}
