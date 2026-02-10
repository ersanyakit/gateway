package main

import (
	"context"
	"core/global"
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

	solanaChain, _ := global.Get().Factory.GetChain("solana")
	ethereumChain, _ := global.Get().Factory.GetChain("ethereum")
	tronChain, _ := global.Get().Factory.GetChain("tron")
	bitcoinChain, _ := global.Get().Factory.GetChain("bitcoin")
	avalancheChain, _ := global.Get().Factory.GetChain("avalanche")
	binanceChain, _ := global.Get().Factory.GetChain("binance")

	solanaWallet, _ := solanaChain.Create(mainCtx)
	fmt.Printf("%s %s %s %s\n", solanaChain.Name(), solanaWallet.Address, solanaWallet.PrivateKey, solanaWallet.MnemonicPhrase)

	ethereumWallet, _ := ethereumChain.Create(mainCtx)
	fmt.Printf("%s %s %s %s\n", ethereumChain.Name(), ethereumWallet.Address, ethereumWallet.PrivateKey, ethereumWallet.MnemonicPhrase)

	tronWallet, _ := tronChain.Create(mainCtx)
	fmt.Printf("%s %s %s %s\n", tronChain.Name(), tronWallet.Address, tronWallet.PrivateKey, tronWallet.MnemonicPhrase)

	bitcoinWallet, _ := bitcoinChain.Create(mainCtx)
	fmt.Printf("%s %s %s %s\n", bitcoinChain.Name(), bitcoinWallet.Address, bitcoinWallet.PrivateKey, bitcoinWallet.MnemonicPhrase)

	avalancheWallet, _ := avalancheChain.Create(mainCtx)
	fmt.Printf("%s %s %s %s\n", avalancheChain.Name(), avalancheWallet.Address, avalancheWallet.PrivateKey, avalancheWallet.MnemonicPhrase)

	binanceWallet, _ := binanceChain.Create(mainCtx)
	fmt.Printf("%s %s %s %s\n", binanceChain.Name(), binanceWallet.Address, binanceWallet.PrivateKey, binanceWallet.MnemonicPhrase)

	fmt.Println(global.Get().Factory.ListChains())

	wallets, errs := global.Get().Factory.CreateWallets(mainCtx)
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

	global.Get().Factory.StartAllWorkers(mainCtx)

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
