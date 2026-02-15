package constants

type ChainID int64

const (
	Bitcoin   ChainID = 0 // Non-EVM
	Ethereum  ChainID = 1
	Binance   ChainID = 56
	Avalanche ChainID = 43114
	Chiliz    ChainID = 88888
	Solana    ChainID = 99999999
	TRON      ChainID = 99999998
)
