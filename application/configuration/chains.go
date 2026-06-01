package application

import (
	"core/blockchain"
	"core/blockchain/chains"
)

func NewChainFactory() *blockchain.ChainFactory {
	factory := blockchain.NewChainFactory()

	factory.RegisterChain("solana", chains.NewSolanaChain())
	factory.RegisterChain("ethereum", chains.NewEthereumChain())
	factory.RegisterChain("tron", chains.NewTronChain())
	factory.RegisterChain("bitcoin", chains.NewBitcoinChain())
	factory.RegisterChain("avalanche", chains.NewAvalancheChain())
	factory.RegisterChain("bnbchain", chains.NewBinanceChain())
	factory.RegisterAlias("binance", "bnbchain")
	factory.RegisterAlias("bsc", "bnbchain")
	factory.RegisterChain("chiliz", chains.NewChilizChain())
	factory.RegisterChain("base", chains.NewBaseChain())
	factory.RegisterChain("unichain", chains.NewUnichainChain())
	return factory
}
