package application

import (
	"core/blockchain"
	"core/blockchain/chains"
	"log"
)

func NewChainFactory() *blockchain.ChainFactory {
	factory := blockchain.NewChainFactory()
	register := func(name string, chain blockchain.Chain) {
		if err := factory.RegisterChain(name, chain); err != nil {
			log.Printf("chain registration name=%q error=%v", name, err)
		}
	}

	register("solana", chains.NewSolanaChain())
	register("ethereum", chains.NewEthereumChain())
	register("tron", chains.NewTronChain())
	register("tron-testnet", chains.NewTronTestnetChain())
	factory.RegisterAlias("trx-testnet", "tron-testnet")
	factory.RegisterAlias("nile", "tron-testnet")
	factory.RegisterAlias("tron-nile", "tron-testnet")
	factory.RegisterAlias("trx-nile", "tron-testnet")
	factory.RegisterAlias("shasta", "tron-testnet")
	factory.RegisterAlias("tron-shasta", "tron-testnet")
	register("bitcoin", chains.NewBitcoinChain())
	register("avalanche", chains.NewAvalancheChain())
	register("bnbchain", chains.NewBinanceChain())
	factory.RegisterAlias("binance", "bnbchain")
	factory.RegisterAlias("bsc", "bnbchain")
	register("chiliz", chains.NewChilizChain())
	register("chiliz-spicy", chains.NewChilizSpicyChain())
	factory.RegisterAlias("spicy", "chiliz-spicy")
	register("base", chains.NewBaseChain())
	register("arbitrum", chains.NewArbitrumChain())
	factory.RegisterAlias("arb", "arbitrum")
	factory.RegisterAlias("arbitrum-one", "arbitrum")
	register("unichain", chains.NewUnichainChain())
	return factory
}
