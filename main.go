package main

import (
	"context"
	"core/api/routes"
	"core/workers/dispatcher"
	ethereumWorker "core/workers/listeners/ethereum"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	coreApplication "core/application"
	coreDB "core/services/database"

	"github.com/joho/godotenv"
)

func NewApp() (*coreApplication.App, error) {
	if coreApplication.CORE == nil {

		err := coreDB.InitDB()
		if err != nil {
			return nil, err
		}

		coreApplication.CORE = &coreApplication.App{
			DB:     coreDB.DB,
			Router: routes.NewRouter(coreDB.DB),
		}

		migrateFlag := flag.Bool("migrate", false, "Run DB migrations")
		seedFlag := flag.Bool("seed", false, "Run DB seed")
		installFlag := flag.Bool("install", false, "Run DB migrate & seed")

		flag.Parse()

		if *installFlag {
			*seedFlag = true
			*migrateFlag = true
		}

		if *migrateFlag {
			fmt.Println("Migration:BEGIN")
			err = coreDB.Migrate(coreApplication.CORE)
			if err != nil {
				fmt.Println(err)
			}

			fmt.Println("Migration:END")
		}

		if *seedFlag {
			err = coreDB.Seed(coreApplication.CORE)
			if err != nil {
				fmt.Println(err)
			}
		}
	}

	return coreApplication.CORE, nil
}

func GetApp() (*coreApplication.App, error) {
	return NewApp()
}

func main() {
	mainCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	coreApplication.CORE, err = NewApp()
	if err != nil {
		log.Fatal(err)
	}

	solanaChain, _ := coreApplication.CORE.Router.Blockchains().GetChain("solana")
	ethereumChain, _ := coreApplication.CORE.Router.Blockchains().GetChain("ethereum")
	tronChain, _ := coreApplication.CORE.Router.Blockchains().GetChain("tron")
	bitcoinChain, _ := coreApplication.CORE.Router.Blockchains().GetChain("bitcoin")
	avalancheChain, _ := coreApplication.CORE.Router.Blockchains().GetChain("avalanche")
	binanceChain, _ := coreApplication.CORE.Router.Blockchains().GetChain("binance")

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

	fmt.Println(coreApplication.CORE.Router.Blockchains().ListChains())

	wallets, errs := coreApplication.CORE.Router.Blockchains().CreateWallets(mainCtx)
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

	coreApplication.CORE.Router.Blockchains().StartAllWorkers(mainCtx)

	go func() {
		for event := range ethChan {
			fmt.Println("CODE OR DIE", event.Type, event.Chain)
		}
	}()

	fiberApp := coreApplication.CORE.Router.GetFiber()
	log.Println("App running on", os.Getenv("PORT"))
	log.Fatal(fiberApp.Listen(os.Getenv("PORT")))

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	log.Println("Shutting down...")
	bus.Shutdown()
}
