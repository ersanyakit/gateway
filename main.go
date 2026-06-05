package main

import (
	"context"
	"core/api/routes"
	"core/blockchain"
	"core/constants"
	"core/models"
	"core/services/realtime"
	webhooksvc "core/services/webhook"
	"core/types"
	"core/workers/dispatcher"
	addressindex "core/workers/indexer"
	btcListener "core/workers/listeners/bitcoin"
	evmListener "core/workers/listeners/evm"
	solListener "core/workers/listeners/solana"
	tronListener "core/workers/listeners/tron"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"strings"
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

var addrIndex *addressindex.AddressIndex

func handleDepositWebhook(ctx context.Context, notifier *webhooksvc.Notifier, eventType string, txParam types.TransactionParam) (*models.Transaction, error) {
	if txParam.To == nil || txParam.Amount == nil || !isPositiveAmount(*txParam.Amount) {
		return nil, nil
	}

	var wallet *models.Wallet
	if addrIndex != nil {
		if info, ok := addrIndex.Get(txParam.ChainID, *txParam.To); ok {
			w, err := coreApplication.CORE.Router.WalletRepo.FindByID(ctx, info.WalletID)
			if err == nil {
				wallet = w
			}
		}
	}
	if wallet == nil {
		w, err := coreApplication.CORE.Router.WalletRepo.FindByChainAddress(ctx, txParam.ChainID, *txParam.To)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		wallet = w
		if addrIndex != nil {
			addrIndex.Add(txParam.ChainID, *txParam.To, addressindex.WalletInfo{
				WalletID:   wallet.ID,
				MerchantID: wallet.MerchantID,
				DomainID:   wallet.DomainID,
				ProductID:  wallet.ProductID,
				UserID:     wallet.UserID,
			})
		}
	}

	uniqueHash, err := coreApplication.CORE.Router.TransactionRepo.UniqueHash(txParam)
	if err != nil {
		return nil, err
	}

	txModel, err := coreApplication.CORE.Router.TransactionRepo.BindWallet(ctx, uniqueHash, eventType, wallet)
	if err != nil {
		return nil, err
	}
	if txModel.WebhookSentAt != nil {
		return txModel, nil
	}

	go func() {
		deliveryCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := notifier.Deliver(deliveryCtx, wallet.Domain, *txModel)
		if err != nil {
			fmt.Println("Webhook delivery error:", err)
			_ = coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(context.Background(), uniqueHash, false, err)
		} else {
			_ = coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(context.Background(), uniqueHash, true, nil)
		}
	}()

	return txModel, nil
}

func handlePaymentDeposit(ctx context.Context, notifier *webhooksvc.Notifier, txModel *models.Transaction) {
	if txModel == nil || coreApplication.CORE.Router.PaymentRepo == nil {
		return
	}

	session, changed, err := coreApplication.CORE.Router.PaymentRepo.MarkPaidByTransaction(ctx, *txModel)
	if err != nil {
		log.Println("Payment match error:", err)
		return
	}
	if !changed || session == nil {
		return
	}
	publishPaymentUpdate(session)
	if session.WebhookSentAt != nil {
		return
	}

	deliveryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	err = notifier.DeliverPayment(deliveryCtx, session.Domain, *session)
	cancel()
	if err != nil {
		log.Println("Payment webhook delivery error:", err)
		_ = coreApplication.CORE.Router.PaymentRepo.MarkWebhookAttempt(ctx, session.ID, false, err)
		return
	}
	if err := coreApplication.CORE.Router.PaymentRepo.MarkWebhookAttempt(ctx, session.ID, true, nil); err != nil {
		log.Println("Payment webhook mark delivered error:", err)
	}
}

func publishPaymentUpdate(session *models.PaymentSession) {
	if session == nil || coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.PaymentHub == nil {
		return
	}
	coreApplication.CORE.Router.PaymentHub.Broadcast(session.SessionToken, realtime.PaymentEvent{
		Event:       "payment.updated",
		Status:      session.Status,
		Paid:        session.Status == models.PaymentStatusPaid,
		PaymentID:   session.ID.String(),
		TxHash:      ptrValue(session.TxHash),
		SuccessPath: "/checkout/" + session.SessionToken + "/return/success",
		CancelPath:  "/checkout/" + session.SessionToken + "/cancel",
		UpdatedAt:   time.Now().UnixMilli(),
	})
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

	if coreApplication.CORE.Router.PaymentRepo == nil {
		return
	}
	sessions, err := coreApplication.CORE.Router.PaymentRepo.ListPendingWebhooks(ctx, 100)
	if err != nil {
		log.Println("Pending payment webhook query error:", err)
		return
	}
	for _, session := range sessions {
		deliveryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = notifier.DeliverPayment(deliveryCtx, session.Domain, session)
		cancel()
		if err != nil {
			log.Println("Payment webhook retry error:", err)
			_ = coreApplication.CORE.Router.PaymentRepo.MarkWebhookAttempt(ctx, session.ID, false, err)
			continue
		}
		if err := coreApplication.CORE.Router.PaymentRepo.MarkWebhookAttempt(ctx, session.ID, true, nil); err != nil {
			log.Println("Payment webhook mark delivered error:", err)
		}
	}
}

func bootstrapAdminAccount(ctx context.Context) {
	email := strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))
	password := os.Getenv("ADMIN_PASSWORD")
	name := strings.TrimSpace(os.Getenv("ADMIN_NAME"))
	if email == "" {
		email = "admin@gateway.local"
	}
	if password == "" {
		password = "admin123"
	}
	if name == "" {
		name = "Admin"
	}
	created, err := coreApplication.CORE.Router.AdminRepo.EnsureBootstrapAdmin(ctx, email, name, password)
	if err != nil {
		log.Printf("Bootstrap admin error: %v\n", err)
		return
	}
	if created != nil {
		log.Printf("Bootstrap admin created: %s (2FA not yet configured — login to set up)\n", email)
	}
}

func backfillMissingAddresses(ctx context.Context) {
	wallets, err := coreApplication.CORE.Router.WalletRepo.List(ctx, 10000)
	if err != nil {
		log.Printf("Backfill: wallet list error: %v\n", err)
		return
	}
	filled := 0
	for _, wallet := range wallets {
		if err := coreApplication.CORE.Router.WalletRepo.EnsureAllAddresses(
			ctx,
			wallet.ID,
			coreApplication.CORE.Router.Blockchains(),
		); err != nil {
			log.Printf("Backfill: wallet %s error: %v\n", wallet.ID, err)
		} else {
			filled++
		}
	}
	if filled > 0 {
		log.Printf("Backfill: ensured addresses for %d wallets\n", filled)
		if err := addrIndex.Load(); err != nil {
			log.Printf("Backfill: address index reload error: %v\n", err)
		}
	}
}

func startSessionExpiryWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := coreApplication.CORE.Router.PaymentRepo.MarkExpiredSessions(ctx)
			if err != nil {
				log.Println("Session expiry sweep error:", err)
			} else if n > 0 {
				log.Printf("Expired %d payment sessions\n", n)
			}
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

// @title Gateway API
// @version 1.0
// @description Multi-chain merchant payment gateway API.
// @BasePath /
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
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

	addrIndex = addressindex.NewAddressIndex(mainCtx, coreDB.DB)
	if err := addrIndex.Load(); err != nil {
		log.Printf("Address index load error: %v\n", err)
	} else {
		log.Println("Address index loaded")
	}

	go backfillMissingAddresses(mainCtx)
	go bootstrapAdminAccount(mainCtx)

	bus := dispatcher.NewDispatcher()
	assetRegistry := coreApplication.CORE.Router.AssetRegistry()
	webhookNotifier := webhooksvc.NewNotifier()
	go startWebhookRetryWorker(mainCtx, webhookNotifier)
	go startSessionExpiryWorker(mainCtx)

	chainNames := []string{
		"bitcoin",
		"ethereum",
		"chiliz",
		"chiliz-spicy",
		"solana",
		"tron",
		"base",
		"unichain",
		"avalanche",
		"bnbchain",
	}

	var isEnabled = true

	if isEnabled {
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
						} else {
							txModel, webhookErr := handleDepositWebhook(mainCtx, webhookNotifier, event.Type, *tx)
							if webhookErr != nil {
								err = webhookErr
								fmt.Println("Deposit processing error:", webhookErr)
							}
							handlePaymentDeposit(mainCtx, webhookNotifier, txModel)
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
			case constants.TRON:
				worker = tronListener.NewRpcListener(
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
