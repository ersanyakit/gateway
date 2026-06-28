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
	factory.RegisterChain("tron-testnet", chains.NewTronTestnetChain())
	factory.RegisterAlias("trx-testnet", "tron-testnet")
	factory.RegisterAlias("shasta", "tron-testnet")
	factory.RegisterAlias("tron-shasta", "tron-testnet")
	factory.RegisterChain("bitcoin", chains.NewBitcoinChain())
	factory.RegisterChain("avalanche", chains.NewAvalancheChain())
	factory.RegisterChain("bnbchain", chains.NewBinanceChain())
	factory.RegisterAlias("binance", "bnbchain")
	factory.RegisterAlias("bsc", "bnbchain")
	factory.RegisterChain("chiliz", chains.NewChilizChain())
	factory.RegisterChain("chiliz-spicy", chains.NewChilizSpicyChain())
	factory.RegisterAlias("spicy", "chiliz-spicy")
	factory.RegisterChain("base", chains.NewBaseChain())
	factory.RegisterChain("arbitrum", chains.NewArbitrumChain())
	factory.RegisterAlias("arb", "arbitrum")
	factory.RegisterAlias("arbitrum-one", "arbitrum")
	factory.RegisterChain("unichain", chains.NewUnichainChain())
	return factory
}
