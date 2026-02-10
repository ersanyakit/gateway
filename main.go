package main

import (
	"context"
	"core/asset"
	"core/blockchain"
	"core/blockchain/chains"
	"core/constants"
	"core/workers/dispatcher"
	"log"
	"os"
	"os/signal"
	"syscall"

	ethereumWorker "core/workers/listeners/ethereum"
	"fmt"
)

func main() {
	mainCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	factory := blockchain.NewChainFactory()

	factory.RegisterChain("solana", chains.NewSolanaChain())
	factory.RegisterChain("ethereum", chains.NewEthereumChain())
	factory.RegisterChain("tron", chains.NewTronChain())
	factory.RegisterChain("bitcoin", chains.NewBitcoinChain())

	solanaChain, _ := factory.GetChain("solana")
	ethereumChain, _ := factory.GetChain("ethereum")
	tronChain, _ := factory.GetChain("tron")
	bitcoinChain, _ := factory.GetChain("bitcoin")

	// Ethereum
	asset.Global().Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	asset.Global().Register(asset.NewERC20(constants.Ethereum, "0xdAC17F958D2ee523a2206206994597C13D831ec7", "USDT", "Tether USD", 6))
	asset.Global().Register(asset.NewERC20(constants.Ethereum, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "USDC", "USDC", 6))
	asset.Global().Register(asset.NewERC20(constants.Ethereum, "0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599", "WBTC", "Wrapped BTC", 8))

	// Avalanche
	asset.Global().Register(asset.NewEVMNative(constants.Avalanche, "AVAX", "Avalanche", 18))
	asset.Global().Register(asset.NewERC20(constants.Avalanche, "0x0555E30da8f98308EdB960aa94C0Db47230d2B9c", "WBTC", "Wrapped BTC", 8))

	// BNB
	asset.Global().Register(asset.NewEVMNative(constants.BSC, "BNB", "Binance Coin", 18))
	asset.Global().Register(asset.NewERC20(constants.BSC, "0x0555E30da8f98308EdB960aa94C0Db47230d2B9c", "WBTC", "Wrapped BTC", 8))

	// Bitcoin Mainnet
	asset.Global().Register(asset.NewBTC())

	// Solana Mainnet
	asset.Global().Register(asset.NewSOL(constants.Solana))

	// Tron Mainnet
	asset.Global().Register(asset.NewTRX(constants.TRON))

	solanaWallet, _ := solanaChain.Create(mainCtx)
	fmt.Printf("%s %s %s %s\n", solanaChain.Name(), solanaWallet.Address, solanaWallet.PrivateKey, solanaWallet.MnemonicPhrase)

	ethereumWallet, _ := ethereumChain.Create(mainCtx)
	fmt.Printf("%s %s %s %s\n", ethereumChain.Name(), ethereumWallet.Address, ethereumWallet.PrivateKey, ethereumWallet.MnemonicPhrase)

	tronWallet, _ := tronChain.Create(mainCtx)
	fmt.Printf("%s %s %s %s\n", tronChain.Name(), tronWallet.Address, tronWallet.PrivateKey, tronWallet.MnemonicPhrase)

	bitcoinWallet, _ := bitcoinChain.Create(mainCtx)
	fmt.Printf("%s %s %s %s\n", bitcoinChain.Name(), bitcoinWallet.Address, bitcoinWallet.PrivateKey, bitcoinWallet.MnemonicPhrase)

	fmt.Println(factory.ListChains())

	wallets, errs := factory.CreateWallets(mainCtx)
	for chainName, wallet := range wallets {
		fmt.Printf("Wallet created on %s: %s\n", chainName, wallet.Address)
	}
	for chainName, err := range errs {
		fmt.Printf("Failed to create wallet on %s: %v\n", chainName, err)
	}

	bus := dispatcher.NewDispatcher()
	ethChan := bus.Subscribe("ETH", 100)

	ethereumWorker := ethereumWorker.NewRpcListener(ethereumChain.WSS()[0])
	ethereumChain.AddWorker(ethereumWorker)

	//ethereumChain.StartWorkers(mainCtx)

	factory.StartAllWorkers(mainCtx)

	go func() {
		for event := range ethChan {
			fmt.Println("CODE OR DIE", event.Type, event.Chain)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	log.Println("Shutting down...")
	ethereumWorker.Stop()
	bus.Shutdown()
}
