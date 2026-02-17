package main

import (
	"context"
	"core/api/routes"
	"core/constants"
	"core/helpers"
	"core/models"
	"core/types"
	"core/workers/dispatcher"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	coreApplication "core/application"
	coreDB "core/services/database"
	"core/workers/listeners/ethereum"

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

	createMerchantParams := types.MerchantParams{
		Context:        mainCtx,
		Name:           helpers.StrPtr("ersan"),
		Email:          helpers.StrPtr("ersanyakit@gmail.com"),
		EmailRepeat:    helpers.StrPtr("ersanyakit@gmail.com"),
		Password:       helpers.StrPtr("passinput1"),
		PasswordRepeat: helpers.StrPtr("passinput1"),
	}
	merchantReg, err := coreApplication.CORE.Router.MerchantService.Create(createMerchantParams)
	if err != nil {
		fmt.Println("Error", err, merchantReg)
	}

	merchantFindByEmail, err := coreApplication.CORE.Router.MerchantService.FindByEmail(createMerchantParams)
	if err != nil {
		fmt.Println("Error", err, merchantReg)
	}

	createMerchantParams.ID = &merchantFindByEmail.ID
	merchantFindByID, err := coreApplication.CORE.Router.MerchantService.FindByID(createMerchantParams)
	if err != nil {
		fmt.Println("Error", err, merchantFindByID)
	}
	fmt.Println("MerchantInfo ", merchantFindByID.ID, merchantFindByEmail.ID)

	createDomainParams := types.DomainParams{
		Context:       mainCtx,
		MerchantID:    helpers.StrPtr(merchantFindByID.ID.String()),
		DomainURL:     helpers.StrPtr("coolvibes.io"),
		WebhookURL:    helpers.StrPtr("https://coolvibes.io/webhook"),
		WebhookSecret: helpers.StrPtr("randompassword"),
	}

	domainReg, err := coreApplication.CORE.Router.DomainService.Create(createDomainParams)
	if err != nil {
		fmt.Println("Error", err)
	}

	if domainReg != nil {
		fmt.Println("DomainInfo ", domainReg.ID, domainReg.MerchantID, domainReg.HDAccountID)
	}

	domainFind, err := coreApplication.CORE.Router.DomainService.FindByURL(createDomainParams)
	if err != nil {
		fmt.Println("Error", err)
	}

	walletParams := types.WalletParams{
		Context:    mainCtx,
		MerchantId: helpers.StrPtr(domainFind.MerchantID.String()), // MerchantID Domain üzerinden
		DomainId:   helpers.StrPtr(domainFind.ID.String()),
	}
	walletReg, err := coreApplication.CORE.Router.WalletService.Create(walletParams)
	if err != nil {
		fmt.Println("Error", err)
	}

	if walletReg != nil {
		fmt.Println("Wallet", walletReg.HDAddressId)
	}
	fmt.Println(coreApplication.CORE.Router.Blockchains().ListChains())

	bus := dispatcher.NewDispatcher()
	assetRegistry := coreApplication.CORE.Router.AssetRegistry()

	ethChain, err := coreApplication.CORE.Router.MerchantRepo.Blockchains().GetChain("ethereum")

	state, _ := coreApplication.CORE.Router.ChainStateRepo.Get(mainCtx, ethChain.ChainID())

	ethWorker := ethereum.NewRpcListener(
		ethChain,
		assetRegistry,
		state,
		bus,
		func(s *models.ChainState) error {
			return coreApplication.CORE.Router.ChainStateRepo.Update(mainCtx, s)
		},
	)

	ethChain.AddWorker(ethWorker)

	//ethChain.StartWorkers(mainCtx)

	coreApplication.CORE.Router.Blockchains().StartAllWorkers(mainCtx)

	ethChan := bus.Subscribe(constants.Ethereum, 100)

	go func() {
		for event := range ethChan {
			fmt.Println("EVENT GELDI:", event.Type)

			switch event.Type {

			case "transfer":
				transaction := event.Transaction
				err := coreApplication.CORE.Router.TransactionRepo.Create(*transaction)
				if err != nil {
					fmt.Println("Error", err)
				}

			}
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
