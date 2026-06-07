package constants

type ChainID int64

const (
	Bitcoin     ChainID = 0 // Non-EVM
	Ethereum    ChainID = 1
	Base        ChainID = 8453
	Arbitrum    ChainID = 42161
	Binance     ChainID = 56
	Unichain    ChainID = 130
	Avalanche   ChainID = 43114
	Chiliz      ChainID = 88888
	ChilizSpicy ChainID = 88882
	Solana      ChainID = 99999999
	TRON        ChainID = 99999998
)

var chainNames = map[ChainID]string{
	Bitcoin:     "bitcoin",
	Ethereum:    "ethereum",
	Base:        "base",
	Arbitrum:    "arbitrum",
	Binance:     "bnbchain",
	Unichain:    "unichain",
	Avalanche:   "avalanche",
	Chiliz:      "chiliz",
	ChilizSpicy: "chiliz-spicy",
	Solana:      "solana",
	TRON:        "tron",
}

var chainLogoSlugs = map[ChainID]string{
	Bitcoin:     "bitcoinchain",
	Ethereum:    "ethereumchain",
	Base:        "basechain",
	Arbitrum:    "arbitrumchain",
	Binance:     "bnbchain",
	Unichain:    "unichain",
	Avalanche:   "avalanchechain",
	Chiliz:      "chilizchain",
	ChilizSpicy: "chilizchain",
	Solana:      "solanachain",
	TRON:        "tronchain",
}

func AllChainIDs() []ChainID {
	return []ChainID{
		Bitcoin,
		Ethereum,
		Chiliz,
		ChilizSpicy,
		Solana,
		TRON,
		Base,
		Arbitrum,
		Unichain,
		Avalanche,
		Binance,
	}
}

var testnetChains = map[ChainID]bool{
	ChilizSpicy: true,
}

func IsTestnet(chainID ChainID) bool {
	return testnetChains[chainID]
}

func IsSupportedChainID(chainID ChainID) bool {
	_, ok := chainNames[chainID]
	return ok
}

func ChainName(chainID ChainID) string {
	return chainNames[chainID]
}

func ChainLogoSlug(chainID ChainID) string {
	return chainLogoSlugs[chainID]
}
