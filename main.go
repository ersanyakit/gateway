package main

import (
	"context"
	"core/api/routes"
	"core/blockchain"
	"core/constants"
	"core/models"
	"core/services/realtime"
	"core/services/txrescan"
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
	"strconv"
	"strings"
	"syscall"
	"time"

	coreApplication "core/application"
	coreHelpers "core/helpers"
	"core/repositories"
	coreDB "core/services/database"

	"github.com/google/uuid"
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

func transactionFinalityInterval() time.Duration {
	raw := os.Getenv("TRANSACTION_FINALITY_INTERVAL")
	if raw == "" {
		return 20 * time.Second
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return 20 * time.Second
	}
	return interval
}

func chainConfirmationRequirement(chainID constants.ChainID) uint {
	envKeys := []string{fmt.Sprintf("CHAIN_%d_CONFIRMATIONS", chainID)}
	if chainName := constants.ChainName(chainID); chainName != "" {
		envKeys = append(envKeys, strings.ToUpper(strings.ReplaceAll(chainName, "-", "_"))+"_CONFIRMATIONS")
	}
	envKeys = append(envKeys, "FINALITY_CONFIRMATIONS_DEFAULT")
	for _, key := range envKeys {
		if key == "" {
			continue
		}
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			value, err := strconv.ParseUint(raw, 10, 32)
			if err == nil && value > 0 {
				return uint(value)
			}
		}
	}
	switch chainID {
	case constants.Bitcoin:
		return 3
	case constants.Solana:
		return 1
	case constants.TRON:
		return 20
	default:
		return 12
	}
}

func transactionConfirmations(blockNumber string, state *models.ChainState) uint {
	block, err := strconv.ParseInt(strings.TrimSpace(blockNumber), 10, 64)
	if err != nil || block <= 0 || state == nil || state.LastProcessedBlock < block {
		return 0
	}
	return uint(state.LastProcessedBlock - block + 1)
}

var addrIndex *addressindex.AddressIndex

func walletForAddress(ctx context.Context, chainID constants.ChainID, address string) (*models.Wallet, bool, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, false, nil
	}

	if addrIndex != nil {
		if info, ok := addrIndex.Get(chainID, address); ok {
			wallet, err := coreApplication.CORE.Router.WalletRepo.FindByID(ctx, info.WalletID)
			if err == nil {
				return wallet, true, nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, false, err
			}
		}
	}

	wallet, err := coreApplication.CORE.Router.WalletRepo.FindByChainAddress(ctx, chainID, address)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if addrIndex != nil {
		addrIndex.Add(chainID, address, addressindex.WalletInfo{
			WalletID:   wallet.ID,
			MerchantID: wallet.MerchantID,
			DomainID:   wallet.DomainID,
			ProductID:  wallet.ProductID,
			UserID:     wallet.UserID,
		})
	}
	return wallet, true, nil
}

func transactionWalletMatch(ctx context.Context, txParam types.TransactionParam) (*models.Wallet, bool, bool, error) {
	if txParam.To != nil {
		wallet, ok, err := walletForAddress(ctx, txParam.ChainID, *txParam.To)
		if err != nil {
			return nil, false, false, err
		}
		if ok {
			return wallet, true, true, nil
		}
	}
	if txParam.From != nil {
		wallet, ok, err := walletForAddress(ctx, txParam.ChainID, *txParam.From)
		if err != nil {
			return nil, false, false, err
		}
		if ok {
			return wallet, false, true, nil
		}
	}
	return nil, false, false, nil
}

func bindTransactionWallet(ctx context.Context, eventType string, txParam types.TransactionParam, wallet *models.Wallet) (*models.Transaction, error) {
	uniqueHash, err := coreApplication.CORE.Router.TransactionRepo.UniqueHash(txParam)
	if err != nil {
		return nil, err
	}
	return coreApplication.CORE.Router.TransactionRepo.BindWallet(ctx, uniqueHash, eventType, wallet)
}

func handleDepositWebhook(ctx context.Context, notifier *webhooksvc.Notifier, eventType string, txParam types.TransactionParam) (*models.Transaction, error) {
	if txParam.To == nil || txParam.Amount == nil || !isPositiveAmount(*txParam.Amount) {
		return nil, nil
	}

	wallet, ok, err := walletForAddress(ctx, txParam.ChainID, *txParam.To)
	if err != nil || !ok {
		return nil, err
	}

	uniqueHash, err := coreApplication.CORE.Router.TransactionRepo.UniqueHash(txParam)
	if err != nil {
		return nil, err
	}

	txModel, err := coreApplication.CORE.Router.TransactionRepo.BindWallet(ctx, uniqueHash, eventType, wallet)
	if err != nil {
		return nil, err
	}
	if coreApplication.CORE.Router.LedgerRepo != nil {
		if err := coreApplication.CORE.Router.LedgerRepo.CreateDepositPending(ctx, *txModel); err != nil {
			log.Println("Ledger pending deposit error:", err)
		}
	}
	if txModel.Status != models.TransactionStatusConfirmed || txModel.FinalizedAt == nil {
		return txModel, nil
	}
	if txModel.WebhookSentAt != nil {
		return txModel, nil
	}

	go func() {
		deliveryCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		deliveryID := createTransactionWebhookDelivery(deliveryCtx, wallet.Domain, *txModel)
		err := notifier.Deliver(deliveryCtx, wallet.Domain, *txModel)
		if err != nil {
			fmt.Println("Webhook delivery error:", err)
			markWebhookDeliveryAttempt(context.Background(), deliveryID, false, err)
			_ = coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(context.Background(), uniqueHash, false, err)
		} else {
			markWebhookDeliveryAttempt(context.Background(), deliveryID, true, nil)
			_ = coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(context.Background(), uniqueHash, true, nil)
		}
	}()

	go autoSweepDeposit(txModel)

	return txModel, nil
}

func applyTransactionFinality(ctx context.Context, txParam types.TransactionParam) (*models.Transaction, error) {
	uniqueHash, err := coreApplication.CORE.Router.TransactionRepo.UniqueHash(txParam)
	if err != nil {
		return nil, err
	}
	if txParam.Status != nil && strings.EqualFold(*txParam.Status, models.TransactionStatusFailed) {
		return coreApplication.CORE.Router.TransactionRepo.MarkFailed(ctx, uniqueHash)
	}
	state, err := coreApplication.CORE.Router.ChainStateRepo.Get(ctx, txParam.ChainID)
	if err != nil {
		return nil, err
	}
	required := chainConfirmationRequirement(txParam.ChainID)
	txModel, err := coreApplication.CORE.Router.TransactionRepo.FindByUniqueHash(ctx, uniqueHash)
	if err != nil {
		return nil, err
	}
	confirmations := transactionConfirmations(txModel.BlockNumber, state)
	finalized := confirmations >= required
	return coreApplication.CORE.Router.TransactionRepo.MarkFinality(ctx, uniqueHash, confirmations, required, finalized)
}

func finalizePendingTransactions(ctx context.Context, notifier *webhooksvc.Notifier) {
	rows, err := coreApplication.CORE.Router.TransactionRepo.ListPendingFinality(ctx, 500)
	if err != nil {
		log.Println("Pending finality query error:", err)
		return
	}
	for _, row := range rows {
		state, err := coreApplication.CORE.Router.ChainStateRepo.Get(ctx, row.ChainID)
		if err != nil {
			log.Println("Finality chain state error:", err)
			continue
		}
		required := row.ConfirmationsRequired
		if required == 0 {
			required = chainConfirmationRequirement(row.ChainID)
		}
		confirmations := transactionConfirmations(row.BlockNumber, state)
		if confirmations < required {
			_, _ = coreApplication.CORE.Router.TransactionRepo.MarkFinality(ctx, row.UniqueHash, confirmations, required, false)
			continue
		}
		finalized, err := coreApplication.CORE.Router.TransactionRepo.MarkFinality(ctx, row.UniqueHash, confirmations, required, true)
		if err != nil {
			log.Println("Finality update error:", err)
			continue
		}
		handlePaymentDeposit(ctx, notifier, finalized)
		go autoSweepDeposit(finalized)
	}
}

func ensureMerchantReserveWallet(ctx context.Context, merchantID uuid.UUID) (*models.Wallet, error) {
	wallet, err := coreApplication.CORE.Router.WalletRepo.FindReserveWallet(ctx, merchantID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		domain, createErr := coreApplication.CORE.Router.DomainService.CreateReserve(ctx, merchantID)
		if createErr != nil {
			return nil, fmt.Errorf("reserve domain: %w", createErr)
		}
		wallet, err = coreApplication.CORE.Router.WalletRepo.CreateReserveWallet(ctx, merchantID, domain.ID, domain.HDAccountID)
	}
	if err != nil {
		return nil, err
	}
	if err := coreApplication.CORE.Router.WalletRepo.EnsureAllAddresses(ctx, wallet.ID, coreApplication.CORE.Router.Blockchains()); err != nil {
		return nil, err
	}
	return coreApplication.CORE.Router.WalletRepo.FindByID(ctx, wallet.ID)
}

// autoSweepDeposit moves funds from a user wallet (HDAddressId > 0) to the merchant's reserve wallet.
// For ERC-20 deposits it prefunds gas from the reserve wallet first if the user wallet is low on native balance.
// Runs asynchronously — errors are logged, not propagated.
func autoSweepDeposit(txModel *models.Transaction) {
	if txModel == nil || txModel.MerchantID == nil || txModel.WalletID == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	userWallet, err := coreApplication.CORE.Router.WalletRepo.FindByID(ctx, *txModel.WalletID)
	if err != nil || userWallet == nil {
		log.Printf("auto-sweep: wallet %s not found: %v", txModel.WalletID, err)
		return
	}
	if userWallet.HDAddressId == 0 {
		return // reserve wallet — never sweep from it
	}

	chain, err := coreApplication.CORE.Router.Blockchains().GetChainByID(txModel.ChainID)
	if err != nil {
		log.Printf("auto-sweep: chain %d not found: %v", txModel.ChainID, err)
		return
	}

	reserveWallet, err := ensureMerchantReserveWallet(ctx, *txModel.MerchantID)
	if err != nil {
		log.Printf("auto-sweep: no reserve wallet for merchant %s: %v", txModel.MerchantID, err)
		return
	}

	reserveAddr := repositories.WalletAddressForChainID(*reserveWallet, txModel.ChainID)
	if reserveAddr == "" {
		log.Printf("auto-sweep: reserve wallet has no address for chain %d", txModel.ChainID)
		return
	}

	// Re-derive user wallet private key from mnemonic
	userDetails, err := chain.CreateHDWallet(ctx, int(userWallet.HDAccountID), int(userWallet.HDAddressId))
	if err != nil {
		log.Printf("auto-sweep: re-derive wallet [acct=%d idx=%d] failed: %v", userWallet.HDAccountID, userWallet.HDAddressId, err)
		return
	}

	if txModel.Token != nil && *txModel.Token != "" {
		// ERC-20 deposit: ensure user wallet has enough native gas first
		reserveDetails, err := chain.CreateHDWallet(ctx, int(reserveWallet.HDAccountID), int(reserveWallet.HDAddressId))
		if err != nil {
			log.Printf("auto-sweep: re-derive reserve wallet failed: %v", err)
			return
		}
		prefunded, err := chain.PrefundGas(ctx, *reserveDetails, userDetails.Address)
		if err != nil {
			log.Printf("auto-sweep: gas prefund [chain=%d addr=%s]: %v", txModel.ChainID, userDetails.Address, err)
			// Continue anyway — maybe there's already enough gas
		} else if prefunded {
			log.Printf("auto-sweep: gas prefunded to %s on chain %d", userDetails.Address, txModel.ChainID)
			// Brief pause for the prefund tx to be picked up before sweeping
			time.Sleep(5 * time.Second)
		}
		result, err := chain.SweepERC20To(ctx, *userDetails, *txModel.Token, reserveAddr)
		if err != nil {
			log.Printf("auto-sweep ERC-20 [chain=%d token=%s]: %v", txModel.ChainID, *txModel.Token, err)
			return
		}
		log.Printf("auto-sweep ERC-20 [chain=%d token=%s]: swept to reserve tx=%s", txModel.ChainID, *txModel.Token, result.TxHash)
		return
	}

	// Native deposit sweep
	result, err := chain.SweepTo(ctx, *userDetails, reserveAddr)
	if err != nil {
		log.Printf("auto-sweep native [chain=%d]: %v", txModel.ChainID, err)
		return
	}
	log.Printf("auto-sweep native [chain=%d]: swept to reserve tx=%s", txModel.ChainID, result.TxHash)
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
	if coreApplication.CORE.Router.LedgerRepo != nil {
		if err := coreApplication.CORE.Router.LedgerRepo.PostDepositAvailable(ctx, *session, *txModel); err != nil {
			log.Println("Ledger available deposit error:", err)
		}
	}
	publishPaymentUpdate(session)
	if session.WebhookSentAt != nil {
		return
	}

	deliveryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	deliveryID := createPaymentWebhookDelivery(deliveryCtx, session.Domain, *session)
	err = notifier.DeliverPayment(deliveryCtx, session.Domain, *session)
	cancel()
	if err != nil {
		log.Println("Payment webhook delivery error:", err)
		markWebhookDeliveryAttempt(ctx, deliveryID, false, err)
		_ = coreApplication.CORE.Router.PaymentRepo.MarkWebhookAttempt(ctx, session.ID, false, err)
		return
	}
	markWebhookDeliveryAttempt(ctx, deliveryID, true, nil)
	if err := coreApplication.CORE.Router.PaymentRepo.MarkWebhookAttempt(ctx, session.ID, true, nil); err != nil {
		log.Println("Payment webhook mark delivered error:", err)
	}
}

func createTransactionWebhookDelivery(ctx context.Context, domain models.Domain, txModel models.Transaction) uuid.UUID {
	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.WebhookDeliveryRepo == nil {
		return uuid.Nil
	}
	if txModel.MerchantID == nil || txModel.DomainID == nil {
		return uuid.Nil
	}
	delivery := &models.WebhookDelivery{
		MerchantID:    *txModel.MerchantID,
		DomainID:      *txModel.DomainID,
		TransactionID: &txModel.ID,
		EventID:       txModel.UniqueHash,
		EventType:     txModel.EventType,
		TargetURL:     domain.WebhookURL,
		Status:        models.WebhookDeliveryStatusPending,
	}
	if err := coreApplication.CORE.Router.WebhookDeliveryRepo.Create(ctx, delivery); err != nil {
		log.Println("Webhook delivery log create error:", err)
		return uuid.Nil
	}
	return delivery.ID
}

func createPaymentWebhookDelivery(ctx context.Context, domain models.Domain, session models.PaymentSession) uuid.UUID {
	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.WebhookDeliveryRepo == nil {
		return uuid.Nil
	}
	delivery := &models.WebhookDelivery{
		MerchantID: session.MerchantID,
		DomainID:   session.DomainID,
		PaymentID:  &session.ID,
		EventID:    session.ID.String() + ":" + session.WebhookEvent,
		EventType:  session.WebhookEvent,
		TargetURL:  domain.WebhookURL,
		Status:     models.WebhookDeliveryStatusPending,
	}
	if err := coreApplication.CORE.Router.WebhookDeliveryRepo.Create(ctx, delivery); err != nil {
		log.Println("Payment webhook delivery log create error:", err)
		return uuid.Nil
	}
	return delivery.ID
}

func markWebhookDeliveryAttempt(ctx context.Context, deliveryID uuid.UUID, delivered bool, lastErr error) {
	if deliveryID == uuid.Nil || coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.WebhookDeliveryRepo == nil {
		return
	}
	if err := coreApplication.CORE.Router.WebhookDeliveryRepo.MarkAttempt(ctx, deliveryID, delivered, lastErr); err != nil {
		log.Println("Webhook delivery log update error:", err)
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
		deliveryID := createTransactionWebhookDelivery(deliveryCtx, wallet.Domain, txModel)
		err = notifier.Deliver(deliveryCtx, wallet.Domain, txModel)
		cancel()

		if err != nil {
			log.Println("Webhook retry error:", err)
			markWebhookDeliveryAttempt(ctx, deliveryID, false, err)
			_ = coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(ctx, txModel.UniqueHash, false, err)
			continue
		}

		markWebhookDeliveryAttempt(ctx, deliveryID, true, nil)
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
		deliveryID := createPaymentWebhookDelivery(deliveryCtx, session.Domain, session)
		err = notifier.DeliverPayment(deliveryCtx, session.Domain, session)
		cancel()
		if err != nil {
			log.Println("Payment webhook retry error:", err)
			markWebhookDeliveryAttempt(ctx, deliveryID, false, err)
			_ = coreApplication.CORE.Router.PaymentRepo.MarkWebhookAttempt(ctx, session.ID, false, err)
			continue
		}
		markWebhookDeliveryAttempt(ctx, deliveryID, true, nil)
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
	if name == "" {
		name = "Admin"
	}
	if password == "" {
		// No hardcoded default — generate a strong one-time random password.
		rnd, err := coreHelpers.GenerateBcryptSafeSecret()
		if err != nil {
			log.Printf("Bootstrap admin: failed to generate random password: %v\n", err)
			return
		}
		password = rnd
		passwordFile := ".bootstrap_admin_password"
		if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0600); err != nil {
			log.Printf("[BOOTSTRAP] ADMIN_PASSWORD not set and generated password file could not be written: %v\n", err)
			return
		}
		log.Printf("[BOOTSTRAP] ADMIN_PASSWORD not set in .env; generated first-run password file: %s\n", passwordFile)
		log.Printf("[BOOTSTRAP] Set ADMIN_PASSWORD in .env before restarting to use a fixed password.\n")
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

func startTransactionFinalityWorker(ctx context.Context, notifier *webhooksvc.Notifier) {
	ticker := time.NewTicker(transactionFinalityInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			finalizePendingTransactions(ctx, notifier)
		}
	}
}

func finalizeProcessingTransfers(ctx context.Context) {
	router := coreApplication.CORE.Router
	if router.WithdrawalRepo != nil {
		withdrawals, err := router.WithdrawalRepo.ListProcessingWithTxHash(ctx, 100)
		if err != nil {
			log.Println("Processing withdrawal query error:", err)
		} else {
			for _, request := range withdrawals {
				if err := router.WithdrawalRepo.FinalizeProcessingWithLedger(ctx, request.ID, router.LedgerRepo); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					log.Printf("Processing withdrawal finalize error %s: %v\n", request.ID, err)
				}
			}
		}
	}
	if router.RefundRepo != nil && router.PaymentRepo != nil {
		refunds, err := router.RefundRepo.ListProcessingWithTxHash(ctx, 100)
		if err != nil {
			log.Println("Processing refund query error:", err)
		} else {
			for _, refund := range refunds {
				session, err := router.PaymentRepo.FindByID(ctx, refund.PaymentID)
				if err != nil {
					log.Printf("Processing refund payment lookup error %s: %v\n", refund.ID, err)
					continue
				}
				if err := router.RefundRepo.MarkSucceededWithLedger(ctx, refund.ID, refund.ReviewedBy, refund.TxHash, *session, router.LedgerRepo); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					log.Printf("Processing refund finalize error %s: %v\n", refund.ID, err)
				}
			}
		}
	}
}

func startTransferFinalizationWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	finalizeProcessingTransfers(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			finalizeProcessingTransfers(ctx)
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
	coreApplication.CORE.Router.TxRescanService = txrescan.New(
		coreApplication.CORE.Router.Blockchains(),
		assetRegistry,
		bus,
		coreApplication.CORE.Router.TransactionRepo,
		coreApplication.CORE.Router.WalletRepo,
	)
	webhookNotifier := webhooksvc.NewNotifier()
	go startWebhookRetryWorker(mainCtx, webhookNotifier)
	go startSessionExpiryWorker(mainCtx)
	go startTransactionFinalityWorker(mainCtx, webhookNotifier)
	go startTransferFinalizationWorker(mainCtx)

	chainNames := []string{
		"bitcoin",
		"ethereum",
		"chiliz",
		"chiliz-spicy",
		"solana",
		"tron",
		"base",
		"arbitrum",
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

						wallet, inbound, matched, err := transactionWalletMatch(mainCtx, *tx)
						if err != nil {
							fmt.Println("Wallet match error:", err)
							if event.Ack != nil {
								event.Ack <- err
							}
							continue
						}
						if !matched {
							if event.Ack != nil {
								event.Ack <- nil
							}
							continue
						}

						err = coreApplication.CORE.Router.TransactionRepo.Create(*tx)
						if err != nil {
							fmt.Println("Transaction save error:", err)
						} else {
							if _, finalityErr := applyTransactionFinality(mainCtx, *tx); finalityErr != nil {
								err = finalityErr
								fmt.Println("Transaction finality error:", finalityErr)
							}
							if err == nil {
								if inbound {
									txModel, webhookErr := handleDepositWebhook(mainCtx, webhookNotifier, event.Type, *tx)
									if webhookErr != nil {
										err = webhookErr
										fmt.Println("Deposit processing error:", webhookErr)
									}
									if err == nil {
										if txModel == nil {
											if _, bindErr := bindTransactionWallet(mainCtx, event.Type, *tx, wallet); bindErr != nil {
												err = bindErr
												fmt.Println("Transaction wallet bind error:", bindErr)
											}
										} else {
											handlePaymentDeposit(mainCtx, webhookNotifier, txModel)
										}
									}
								} else {
									if _, bindErr := bindTransactionWallet(mainCtx, event.Type, *tx, wallet); bindErr != nil {
										err = bindErr
										fmt.Println("Transaction wallet bind error:", bindErr)
									}
								}
							}
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
