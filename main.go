package main

import (
	"context"
	"core/api/routes"
	"core/blockchain"
	"core/constants"
	"core/models"
	webhooksvc "core/services/webhook"
	"core/types"
	"core/workers/dispatcher"
	btcListener "core/workers/listeners/bitcoin"
	evmListener "core/workers/listeners/evm"
	solListener "core/workers/listeners/solana"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"syscall"
	"time"

	coreApplication "core/application"
	coreDB "core/services/database"

	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

func ptrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func isPositiveAmount(value string) bool {
	amount, ok := new(big.Int).SetString(value, 10)
	return ok && amount.Sign() > 0
}

func webhookRetryInterval() time.Duration {
	raw := os.Getenv("WEBHOOK_RETRY_INTERVAL")
	if raw == "" {
		return 30 * time.Second
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return 30 * time.Second
	}
	return interval
}

func handleDepositWebhook(ctx context.Context, notifier *webhooksvc.Notifier, eventType string, txParam types.TransactionParam) error {
	if txParam.To == nil || txParam.Amount == nil || !isPositiveAmount(*txParam.Amount) {
		return nil
	}

	wallet, err := coreApplication.CORE.Router.WalletRepo.FindByChainAddress(ctx, txParam.ChainID, *txParam.To)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	uniqueHash, err := coreApplication.CORE.Router.TransactionRepo.UniqueHash(txParam)
	if err != nil {
		return err
	}

	txModel, err := coreApplication.CORE.Router.TransactionRepo.BindWallet(ctx, uniqueHash, eventType, wallet)
	if err != nil {
		return err
	}
	if txModel.WebhookSentAt != nil {
		return nil
	}

	deliveryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	err = notifier.Deliver(deliveryCtx, wallet.Domain, *txModel)
	if err != nil {
		fmt.Println("Webhook delivery error:", err)
		return coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(ctx, uniqueHash, false, err)
	}

	return coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(ctx, uniqueHash, true, nil)
}

func retryPendingWebhooks(ctx context.Context, notifier *webhooksvc.Notifier) {
	transactions, err := coreApplication.CORE.Router.TransactionRepo.ListPendingWebhooks(ctx, 100)
	if err != nil {
		log.Println("Pending webhook query error:", err)
		return
	}

	for _, txModel := range transactions {
		if txModel.WalletID == nil || txModel.WebhookSentAt != nil {
			continue
		}

		wallet, err := coreApplication.CORE.Router.WalletRepo.FindByID(ctx, *txModel.WalletID)
		if err != nil {
			log.Println("Webhook wallet lookup error:", err)
			_ = coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(ctx, txModel.UniqueHash, false, err)
			continue
		}

		deliveryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = notifier.Deliver(deliveryCtx, wallet.Domain, txModel)
		cancel()

		if err != nil {
			log.Println("Webhook retry error:", err)
			_ = coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(ctx, txModel.UniqueHash, false, err)
			continue
		}

		if err := coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(ctx, txModel.UniqueHash, true, nil); err != nil {
			log.Println("Webhook mark delivered error:", err)
		}
	}
}

func startWebhookRetryWorker(ctx context.Context, notifier *webhooksvc.Notifier) {
	ticker := time.NewTicker(webhookRetryInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			retryPendingWebhooks(ctx, notifier)
		}
	}
}

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

	_ = godotenv.Load()

	var err error
	coreApplication.CORE, err = NewApp()
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Registered chains:", coreApplication.CORE.Router.Blockchains().ListChains())
	deletedChainStates, err := coreApplication.CORE.Router.ChainStateRepo.DeleteUnsupported(
		mainCtx,
		coreApplication.CORE.Router.Blockchains().ListChainIDs(),
	)
	if err != nil {
		log.Fatal(err)
	}
	if deletedChainStates > 0 {
		log.Printf("Deleted %d unsupported chain state rows\n", deletedChainStates)
	}

	bus := dispatcher.NewDispatcher()
	assetRegistry := coreApplication.CORE.Router.AssetRegistry()
	webhookNotifier := webhooksvc.NewNotifier()
	go startWebhookRetryWorker(mainCtx, webhookNotifier)

	chainNames := []string{
		"bitcoin",
		"ethereum",
		"chiliz",
		"solana",
		"tron",
		"base",
		"unichain",
		"avalanche",
		"bnbchain",
	}

	subscribeBus := func(chain blockchain.Chain) {
		events := bus.Subscribe(chain.ChainID(), 1000)
		go func() {
			for {
				select {
				case <-mainCtx.Done():
					return
				case event, ok := <-events:
					if !ok {
						return
					}
					if event.Transaction == nil {
						if event.Ack != nil {
							event.Ack <- nil
						}
						continue
					}

					tx := event.Transaction
					fmt.Printf(
						"[BUS] chain=%s chain_id=%d type=%s hash=%s block=%s from=%s to=%s amount=%s symbol=%s log=%s\n",
						chain.Name(),
						tx.ChainID,
						event.Type,
						ptrValue(tx.Hash),
						ptrValue(tx.Block),
						ptrValue(tx.From),
						ptrValue(tx.To),
						ptrValue(tx.Amount),
						ptrValue(tx.Symbol),
						ptrValue(tx.LogIndex),
					)

					err := coreApplication.CORE.Router.TransactionRepo.Create(*tx)
					if err != nil {
						fmt.Println("Transaction save error:", err)
					} else if webhookErr := handleDepositWebhook(mainCtx, webhookNotifier, event.Type, *tx); webhookErr != nil {
						err = webhookErr
						fmt.Println("Deposit processing error:", webhookErr)
					}
					if event.Ack != nil {
						event.Ack <- err
					}
				}
			}
		}()
	}

	for _, chainName := range chainNames {
		chain, err := coreApplication.CORE.Router.MerchantRepo.Blockchains().GetChain(chainName)
		if err != nil {
			log.Printf("[%s] chain not found: %v\n", chainName, err)
			continue
		}

		state, err := coreApplication.CORE.Router.ChainStateRepo.Get(mainCtx, chain.ChainID())
		if err != nil {
			log.Printf("[%s] chain state error: %v\n", chainName, err)
			continue
		}

		var worker blockchain.Worker
		switch chain.ChainID() {
		case constants.Bitcoin:
			worker = btcListener.NewRpcListener(
				chain,
				assetRegistry,
				state,
				bus,
				func(s *models.ChainState) error {
					return coreApplication.CORE.Router.ChainStateRepo.Update(mainCtx, s)
				},
			)
		case constants.Solana:
			worker = solListener.NewRpcListener(
				chain,
				assetRegistry,
				state,
				bus,
				func(s *models.ChainState) error {
					return coreApplication.CORE.Router.ChainStateRepo.Update(mainCtx, s)
				},
			)
		default:
			worker = evmListener.NewRpcListener(
				chain,
				assetRegistry,
				state,
				bus,
				func(s *models.ChainState) error {
					return coreApplication.CORE.Router.ChainStateRepo.Update(mainCtx, s)
				},
			)
		}

		if err := chain.AddWorker(worker); err != nil {
			log.Printf("[%s] add worker error: %v\n", chain.Name(), err)
			continue
		}
		subscribeBus(chain)
	}

	for chainName, startErr := range coreApplication.CORE.Router.Blockchains().StartAllWorkers(mainCtx) {
		log.Printf("[%s] worker start error: %v\n", chainName, startErr)
	}

	fiberApp := coreApplication.CORE.Router.GetFiber()
	port := os.Getenv("PORT")
	log.Println("App running on", port)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- fiberApp.Listen(port)
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-c:
		log.Println("Shutting down...", sig)
	case err := <-serverErr:
		log.Fatal(err)
	}

	coreApplication.CORE.Router.Blockchains().StopAllWorkers(context.Background())
	cancel()
	bus.Shutdown()
	if err := fiberApp.Shutdown(); err != nil {
		log.Println("Fiber shutdown error:", err)
	}
}
