package asset

type BitcoinAsset struct {
	BaseAsset
}

func NewBTC() *BitcoinAsset {
	return &BitcoinAsset{
		BaseAsset: BaseAsset{
			ChainID:   0,
			ChainType: ChainBitcoin,
			Type:      AssetUTXO,
			Symbol:    "BTC",
			Name:      "Bitcoin",
			Decimals:  8,
			Native:    true,
		},
	}
}

func (b *BitcoinAsset) GetIdentifier() string {
	return b.Symbol
}
