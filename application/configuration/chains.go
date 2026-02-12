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
	factory.RegisterChain("binance", chains.NewBinanceChain())

	return factory
}
