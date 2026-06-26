package asset

import "core/constants"

type SolanaAsset struct {
	BaseAsset
	MintAddress string
}

func NewSOL(chainID constants.ChainID) *SolanaAsset {
	return &SolanaAsset{
		BaseAsset: BaseAsset{
			ChainID:   chainID,
			ChainType: ChainSolana,
			Type:      AssetNative,
			Symbol:    "SOL",
			Name:      "Solana",
			Decimals:  9,
			Native:    true,
		},
	}
}

func NewSPL(chainID constants.ChainID, mint, symbol, name string, decimals uint8) *SolanaAsset {
	return &SolanaAsset{
		BaseAsset: BaseAsset{
			ChainID:   chainID,
			ChainType: ChainSolana,
			Type:      AssetSPL,
			Symbol:    symbol,
			Name:      name,
			Decimals:  decimals,
			Native:    false,
		},
		MintAddress: mint,
	}
}

func (s *SolanaAsset) GetIdentifier() string {
	if s.Native {
		return s.Symbol
	}
	return s.MintAddress
}
