package main

import (
	"context"
	"core/blockchain"
	"core/blockchain/chains"
	"core/workers/dispatcher"
	"fmt"
)

func main() {

	factory := blockchain.NewChainFactory()

	factory.RegisterChain("solana", chains.NewSolanaChain())
	factory.RegisterChain("ethereum", chains.NewEthereumChain())
	factory.RegisterChain("tron", chains.NewTronChain())
	factory.RegisterChain("bitcoin", chains.NewBitcoinChain())

	solanaChain, _ := factory.GetChain("solana")
	ethereumChain, _ := factory.GetChain("ethereum")
	tronChain, _ := factory.GetChain("tron")
	bitcoinChain, _ := factory.GetChain("bitcoin")

	solanaWallet, _ := solanaChain.Create(context.Background())
	fmt.Printf("%s %s %s %s\n", solanaChain.Name(), solanaWallet.Address, solanaWallet.PrivateKey, solanaWallet.MnemonicPhrase)

	ethereumWallet, _ := ethereumChain.Create(context.Background())
	fmt.Printf("%s %s %s %s\n", ethereumChain.Name(), ethereumWallet.Address, ethereumWallet.PrivateKey, ethereumWallet.MnemonicPhrase)

	tronWallet, _ := tronChain.Create(context.Background())
	fmt.Printf("%s %s %s %s\n", tronChain.Name(), tronWallet.Address, tronWallet.PrivateKey, tronWallet.MnemonicPhrase)

	bitcoinWallet, _ := bitcoinChain.Create(context.Background())
	fmt.Printf("%s %s %s %s\n", bitcoinChain.Name(), bitcoinWallet.Address, bitcoinWallet.PrivateKey, bitcoinWallet.MnemonicPhrase)

	fmt.Println(factory.ListChains())

	wallets, errs := factory.CreateWallets(context.Background())
	for chainName, wallet := range wallets {
		fmt.Printf("Wallet created on %s: %s\n", chainName, wallet.Address)
	}
	for chainName, err := range errs {
		fmt.Printf("Failed to create wallet on %s: %v\n", chainName, err)
	}

	bus := dispatcher.NewDispatcher()
	ethChan := bus.Subscribe("ETH", 100)

	go func() {
		for event := range ethChan {
			fmt.Println("CODE OR DIE", event.Type, event.Chain)
		}
	}()

}
