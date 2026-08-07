package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/models"
	"core/repositories"
	"core/services/pricing"
	"core/services/realtime"
	webhooksvc "core/services/webhook"
	x402svc "core/services/x402"
	"core/types"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
	"gorm.io/gorm"
)

type PaymentHandlerDeps struct {
	DomainRepo                  paymentDomainLookup
	WalletRepo                  paymentWalletRepository
	PaymentRepo                 paymentSessionRepository
	NetworkOperationalStateRepo paymentNetworkOperationalStateRepository
	WebhookDeliveryRepo         paymentWebhookDeliveryRepository
	MoneyEventOutboxRepo        paymentMoneyEventOutboxRepository
	ProductRepo                 paymentProductRepository
	IdempotencyRepo             paymentIdempotencyRepository
	AssetRegistry               *asset.Registry
	Blockchains                 *blockchain.ChainFactory
	PriceOracle                 pricing.PriceOracle
	Notifier                    *webhooksvc.Notifier
	PaymentHub                  *realtime.PaymentHub
	RequireSignature            bool
}

type paymentNetworkOperationalStateRepository interface {
	GetByChain(context.Context, constants.ChainID) (*models.NetworkOperationalState, error)
}

type paymentDomainLookup interface {
	v1DomainLookup
}

type paymentWalletRepository interface {
	Create(types.WalletParams) (*models.Wallet, error)
	EnsureAllAddresses(context.Context, uuid.UUID, *blockchain.ChainFactory) error
}

type paymentSessionRepository interface {
	Create(context.Context, *models.PaymentSession) error
	FindByID(context.Context, uuid.UUID) (*models.PaymentSession, error)
	FindByToken(context.Context, string) (*models.PaymentSession, error)
	SelectAsset(context.Context, string, constants.ChainID, string, *string, uint8, string, string, *models.PriceQuote) (*models.PaymentSession, error)
	ResetSelection(context.Context, string) (*models.PaymentSession, error)
	Cancel(context.Context, string) (*models.PaymentSession, bool, error)
	Expire(context.Context, string) (*models.PaymentSession, bool, error)
	MarkWebhookAttempt(context.Context, uuid.UUID, bool, error) error
}

type paymentIdempotencyRepository interface {
	RequestHash(any) (string, error)
	Begin(context.Context, uuid.UUID, uuid.UUID, string, string, time.Duration) (*models.IdempotencyKey, bool, error)
	Complete(context.Context, uuid.UUID, uuid.UUID, string) error
	Fail(context.Context, uuid.UUID, string) error
}

type paymentProductRepository interface {
	FindByID(context.Context, uuid.UUID) (*models.Product, error)
}

type paymentWebhookDeliveryRepository interface {
	EnqueuePayment(context.Context, models.Domain, models.PaymentSession) (*models.WebhookDelivery, bool, error)
	MarkAttempt(context.Context, uuid.UUID, uuid.UUID, bool, error) error
}

type paymentMoneyEventOutboxRepository interface {
	HasAggregateEvent(context.Context, string, string, string) (bool, error)
}

type paymentCreateMode int

const (
	paymentCreateModeLegacy paymentCreateMode = iota
	paymentCreateModeV1
)

type paymentCreateAssetSelection struct {
	chainID           constants.ChainID
	symbol            string
	token             *string
	decimals          uint8
	expectedAmountRaw string
	priceSource       string
	price             string
	quotedAt          time.Time
	quoteExpiresAt    time.Time
}

type checkoutResultBrand struct {
	Name    string
	LogoURL string
}

func lookupCheckoutProduct(ctx context.Context, deps PaymentHandlerDeps, productID string) (name, description, logoURL string) {
	if deps.ProductRepo == nil || productID == "" {
		return
	}
	id, err := uuid.Parse(productID)
	if err != nil {
		return
	}
	product, err := deps.ProductRepo.FindByID(ctx, id)
	if err != nil {
		return
	}
	return product.Name, product.Description, product.LogoURL
}

type CheckoutAssetOption struct {
	ChainID           int64
	ChainName         string
	ChainLogoURL      string
	GroupSymbol       string
	Symbol            string
	Name              string
	Token             string
	Decimals          uint8
	AmountRaw         string
	AmountDisplay     string
	Native            bool
	Available         bool
	QuoteAvailable    bool
	UnavailableReason string
	LogoURL           string
}

type CheckoutAssetGroup struct {
	Symbol         string
	Name           string
	AmountDisplay  string
	ChainCount     int
	URL            string
	LogoURL        string
	QuoteAvailable bool
}

const checkoutNetworkAvailabilityError = "Network availability could not be verified. Please try again shortly."

type paymentNetworkDepositAvailabilityResult struct {
	available bool
	reason    string
	err       error
}

func paymentNetworkDepositAvailability(ctx context.Context, repo paymentNetworkOperationalStateRepository, chainID constants.ChainID) (bool, string, error) {
	if repo == nil {
		return true, "", nil
	}
	state, err := repo.GetByChain(ctx, chainID)
	if err != nil {
		return false, checkoutNetworkAvailabilityError, err
	}
	if state == nil {
		return false, checkoutNetworkAvailabilityError, errors.New("network operational state is nil")
	}
	if !state.BlocksDeposits() {
		return true, "", nil
	}
	reason := strings.TrimSpace(state.Reason)
	if reason != "" {
		return false, reason, nil
	}
	switch state.Mode {
	case models.NetworkOperationalModeMaintenance:
		return false, "This network is temporarily under maintenance.", nil
	default:
		return false, "Deposits are temporarily disabled for this network.", nil
	}
}

func cachedPaymentNetworkDepositAvailability(ctx context.Context, repo paymentNetworkOperationalStateRepository, cache map[constants.ChainID]paymentNetworkDepositAvailabilityResult, chainID constants.ChainID) (bool, string, error) {
	if result, ok := cache[chainID]; ok {
		return result.available, result.reason, result.err
	}
	available, reason, err := paymentNetworkDepositAvailability(ctx, repo, chainID)
	cache[chainID] = paymentNetworkDepositAvailabilityResult{available: available, reason: reason, err: err}
	return available, reason, err
}

// HandlePaymentCreate creates a merchant payment checkout session.
// @Summary Create payment session
// @Description Creates a checkout session for a merchant order and returns a hosted checkout URL.
// @Tags Payments
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param X-API-Secret header string true "API secret returned when the domain was created or rotated"
// @Param X-Gateway-Timestamp header string true "Unix timestamp used in HMAC signature"
// @Param X-Gateway-Signature header string true "HMAC-SHA256 over method + path/query + timestamp + raw body, optionally prefixed with sha256="
// @Param Idempotency-Key header string false "Idempotency key. If omitted, order_id is used within the domain scope."
// @Param payload body types.PaymentCreateParams true "Payment create payload"
// @Success 201 {object} types.PaymentCreateResponse
// @Failure 400 {object} types.ErrorResponse
// @Failure 401 {object} types.ErrorResponse
// @Failure 409 {object} types.ErrorResponse
// @Failure 500 {object} types.ErrorResponse
// @Failure 503 {object} types.ErrorResponse
// @Router /payments/create [post]
func HandlePaymentCreate(deps PaymentHandlerDeps) fiber.Handler {
	return handlePaymentCreate(deps, paymentCreateModeLegacy)
}

func handlePaymentCreate(deps PaymentHandlerDeps, mode paymentCreateMode) fiber.Handler {
	return func(c fiber.Ctx) error {
		if deps.RequireSignature {
			if _, err := v1ResolveSignedDomainForScope(c, deps.DomainRepo, models.DomainAPIScopePaymentCreate); err != nil {
				return paymentCreateError(c, mode, fiber.StatusUnauthorized, err.Error())
			}
		}

		var params types.PaymentCreateParams
		if err := c.Bind().Body(&params); err != nil {
			return paymentCreateError(c, mode, fiber.StatusBadRequest, "Invalid JSON body: "+err.Error())
		}
		params.Context = c.Context()
		if err := params.Validate(); err != nil {
			return paymentCreateError(c, mode, fiber.StatusBadRequest, err.Error())
		}

		domain, err := resolvePaymentDomain(c, deps.DomainRepo, params)
		if err != nil {
			return paymentCreateError(c, mode, fiber.StatusUnauthorized, err.Error())
		}
		if params.ChainID != nil {
			chainID := constants.ChainID(*params.ChainID)
			if constants.IsSupportedChainID(chainID) {
				available, reason, availabilityErr := paymentNetworkDepositAvailability(params.Context, deps.NetworkOperationalStateRepo, chainID)
				if availabilityErr != nil {
					return paymentCreateError(c, mode, fiber.StatusServiceUnavailable, checkoutNetworkAvailabilityError)
				}
				if !available {
					return paymentCreateError(c, mode, fiber.StatusServiceUnavailable, reason)
				}
			}
		}

		idempotencyKey := paymentIdempotencyKey(c, params)
		var idempotencyRecord *models.IdempotencyKey
		if deps.IdempotencyRepo != nil && idempotencyKey != "" {
			requestHash, err := deps.IdempotencyRepo.RequestHash(params)
			if err != nil {
				return paymentCreateError(c, mode, fiber.StatusInternalServerError, "idempotency hash failed: "+err.Error())
			}
			record, shouldCreate, err := deps.IdempotencyRepo.Begin(params.Context, domain.ID, domain.MerchantID, idempotencyKey, requestHash, 24*time.Hour)
			if err != nil {
				status := fiber.StatusInternalServerError
				if errors.Is(err, repositories.ErrIdempotencyConflict) {
					status = fiber.StatusConflict
				}
				return paymentCreateError(c, mode, status, err.Error())
			}
			idempotencyRecord = record
			if !shouldCreate {
				if record.ResponseBody != "" {
					c.Set("Content-Type", "application/json")
					return c.Status(fiber.StatusOK).SendString(record.ResponseBody)
				}
				if record.PaymentSessionID != nil {
					session, err := deps.PaymentRepo.FindByID(params.Context, *record.PaymentSessionID)
					if err == nil {
						checkoutURL := baseURL(c) + "/checkout/" + session.SessionToken
						return c.Status(fiber.StatusOK).JSON(paymentCreateResponseBody(mode, *session, checkoutURL, session.WalletID, time.Now()))
					}
				}
				return paymentCreateError(c, mode, fiber.StatusConflict, "idempotency key is still in progress")
			}
		}

		now := time.Now()
		expiresAt := now.Add(paymentSessionTTL())
		selection, err := preparePaymentCreateAssetSelection(params.Context, deps.AssetRegistry, deps.PriceOracle, params, expiresAt, now)
		if err != nil {
			failPaymentCreateIdempotency(params.Context, deps.IdempotencyRepo, idempotencyRecord, err)
			return paymentCreateError(c, mode, fiber.StatusBadRequest, err.Error())
		}

		productID := valueOrDefault(params.ProductID, *params.OrderID)
		userID := valueOrDefault(params.UserID, *params.OrderID)
		merchantID := domain.MerchantID.String()
		domainID := domain.ID.String()
		wallet, err := deps.WalletRepo.Create(types.WalletParams{
			Context:    params.Context,
			MerchantId: &merchantID,
			DomainId:   &domainID,
			ProductId:  &productID,
			UserId:     &userID,
		})
		if err != nil {
			failPaymentCreateIdempotency(params.Context, deps.IdempotencyRepo, idempotencyRecord, err)
			return paymentCreateError(c, mode, fiber.StatusInternalServerError, "wallet create failed: "+err.Error())
		}

		session := &models.PaymentSession{
			MerchantID:       domain.MerchantID,
			DomainID:         domain.ID,
			WalletID:         wallet.ID,
			OrderID:          *params.OrderID,
			ProductID:        productID,
			UserID:           userID,
			LinkType:         models.PaymentLinkTypeFixed,
			Amount:           *params.Amount,
			Currency:         *params.Currency,
			SuccessURL:       valueOrDefault(params.SuccessURL, ""),
			CancelURL:        valueOrDefault(params.CancelURL, ""),
			IdempotencyKey:   idempotencyKey,
			Status:           models.PaymentStatusPending,
			ExpiresAt:        &expiresAt,
			SettlementPolicy: paymentCreateSettlementPolicy(params),
		}
		if requiredMemo := normalizePaymentMemoForHandler(valueOrDefault(params.RequiredMemo, "")); requiredMemo != "" {
			session.RequiredMemo = valueOrDefault(params.RequiredMemo, "")
			session.RequiredMemoNormalized = requiredMemo
		}
		if err := deps.PaymentRepo.Create(params.Context, session); err != nil {
			failPaymentCreateIdempotency(params.Context, deps.IdempotencyRepo, idempotencyRecord, err)
			return paymentCreateError(c, mode, fiber.StatusInternalServerError, "payment session create failed: "+err.Error())
		}

		if selection != nil {
			session, err = applyPaymentCreateAssetSelection(params.Context, deps.PaymentRepo, session, *wallet, selection)
			if err != nil {
				failPaymentCreateIdempotency(params.Context, deps.IdempotencyRepo, idempotencyRecord, err)
				return paymentCreateError(c, mode, fiber.StatusInternalServerError, "asset selection failed: "+err.Error())
			}
		}

		checkoutURL := baseURL(c) + "/checkout/" + session.SessionToken
		response := paymentCreateResponseBody(mode, *session, checkoutURL, wallet.ID, now)
		if idempotencyRecord != nil {
			body, err := json.Marshal(response)
			if err != nil {
				failPaymentCreateIdempotency(params.Context, deps.IdempotencyRepo, idempotencyRecord, err)
				return paymentCreateError(c, mode, fiber.StatusInternalServerError, "idempotency response encoding failed")
			}
			if err := deps.IdempotencyRepo.Complete(params.Context, idempotencyRecord.ID, session.ID, string(body)); err != nil {
				log.Printf("payment idempotency complete record_id=%s payment_id=%s error=%v", idempotencyRecord.ID, session.ID, err)
				return paymentCreateError(c, mode, fiber.StatusInternalServerError, "idempotency completion failed")
			}
		}
		return c.Status(fiber.StatusCreated).JSON(response)
	}
}

func failPaymentCreateIdempotency(ctx context.Context, repo paymentIdempotencyRepository, record *models.IdempotencyKey, cause error) {
	if repo == nil || record == nil || cause == nil {
		return
	}
	if err := repo.Fail(ctx, record.ID, cause.Error()); err != nil {
		log.Printf("payment idempotency fail record_id=%s error=%v", record.ID, err)
	}
}

func paymentCreateError(c fiber.Ctx, mode paymentCreateMode, status int, msg string) error {
	if mode == paymentCreateModeV1 {
		return v1Err(c, status, msg)
	}
	return c.Status(status).JSON(fiber.Map{
		"success": false,
		"error":   msg,
	})
}

func preparePaymentCreateAssetSelection(ctx context.Context, registry *asset.Registry, oracle pricing.PriceOracle, params types.PaymentCreateParams, sessionExpiresAt time.Time, now time.Time) (*paymentCreateAssetSelection, error) {
	if params.ChainID == nil && params.Symbol == nil && params.Token == nil {
		return nil, nil
	}
	if registry == nil {
		return nil, errors.New("asset registry is not configured")
	}
	if params.ChainID == nil || params.Symbol == nil || strings.TrimSpace(*params.Symbol) == "" {
		return nil, errors.New("chain_id and symbol are required when selecting an asset")
	}
	chainID := constants.ChainID(*params.ChainID)
	if !constants.IsSupportedChainID(chainID) {
		return nil, errors.New("unsupported chain_id")
	}
	token := ""
	if params.Token != nil {
		token = strings.TrimSpace(*params.Token)
	}
	assetInfo, err := findCheckoutAsset(registry, chainID, *params.Symbol, token)
	if err != nil {
		return nil, err
	}
	tempSession := models.PaymentSession{
		Amount:   valueOrDefault(params.Amount, ""),
		Currency: valueOrDefault(params.Currency, "USD"),
		LinkType: models.PaymentLinkTypeFixed,
	}
	if strings.TrimSpace(tempSession.Amount) == "0" && strings.TrimSpace(valueOrDefault(params.Currency, "")) == "" {
		tempSession.LinkType = models.PaymentLinkTypeDonation
	}
	amountRaw := ""
	quotePrice := ""
	quoteSource := ""
	if !models.IsDonationLinkType(tempSession.LinkType) {
		var err error
		amountRaw, quotePrice, quoteSource, err = checkoutExpectedQuote(ctx, oracle, tempSession, assetInfo)
		if err != nil {
			return nil, err
		}
	}
	var selectedToken *string
	if !assetInfo.IsNative() {
		identifier := assetInfo.GetIdentifier()
		selectedToken = &identifier
	}
	quoteExpiresAt := now.Add(15 * time.Minute)
	if !sessionExpiresAt.IsZero() && sessionExpiresAt.Before(quoteExpiresAt) {
		quoteExpiresAt = sessionExpiresAt
	}
	return &paymentCreateAssetSelection{
		chainID:           assetInfo.GetChainID(),
		symbol:            strings.ToUpper(strings.TrimSpace(assetInfo.GetSymbol())),
		token:             selectedToken,
		decimals:          assetInfo.GetDecimals(),
		expectedAmountRaw: amountRaw,
		priceSource:       quoteSource,
		price:             quotePrice,
		quotedAt:          now,
		quoteExpiresAt:    quoteExpiresAt,
	}, nil
}

func applyPaymentCreateAssetSelection(ctx context.Context, repo paymentSessionRepository, session *models.PaymentSession, wallet models.Wallet, selection *paymentCreateAssetSelection) (*models.PaymentSession, error) {
	if repo == nil || session == nil || selection == nil {
		return session, errors.New("payment asset selection is not configured")
	}
	depositAddress := paymentDepositAddressForChain(wallet, selection.chainID)
	if depositAddress == "" {
		return session, errors.New("deposit address is not available for this network")
	}
	var quote *models.PriceQuote
	if !models.IsDonationLinkType(session.LinkType) {
		quote = &models.PriceQuote{
			ChainID:           selection.chainID,
			Token:             selection.token,
			Symbol:            selection.symbol,
			Decimals:          selection.decimals,
			FiatCurrency:      strings.ToUpper(strings.TrimSpace(session.Currency)),
			FiatAmount:        strings.TrimSpace(session.Amount),
			ExpectedAmountRaw: selection.expectedAmountRaw,
			PriceSource:       selection.priceSource,
			Price:             selection.price,
			QuotedAt:          selection.quotedAt,
			ExpiresAt:         selection.quoteExpiresAt,
			CreatedAt:         selection.quotedAt,
			UpdatedAt:         selection.quotedAt,
		}
		if quote.FiatCurrency == "" {
			quote.FiatCurrency = "USD"
		}
	}
	return repo.SelectAsset(ctx, session.SessionToken, selection.chainID, selection.symbol, selection.token, selection.decimals, selection.expectedAmountRaw, depositAddress, quote)
}

func paymentCreateResponseBody(mode paymentCreateMode, session models.PaymentSession, checkoutURL string, walletID uuid.UUID, now time.Time) fiber.Map {
	data := paymentCreateResponseData(session, checkoutURL, walletID, now)
	if mode == paymentCreateModeV1 {
		return fiber.Map{
			"result": "ok",
			"data":   data,
		}
	}
	response := fiber.Map{
		"success":        true,
		"payment_id":     session.ID,
		"session_token":  session.SessionToken,
		"checkout_url":   checkoutURL,
		"status":         data["status"],
		"expires_at":     data["expires_at"],
		"deposit_wallet": walletID,
		"link_type":      data["link_type"],
	}
	copySelectedPaymentFields(response, data)
	return response
}

func paymentCreateResponseData(session models.PaymentSession, checkoutURL string, walletID uuid.UUID, now time.Time) fiber.Map {
	var expiresAt string
	if !models.IsDonationLinkType(session.LinkType) && session.ExpiresAt != nil {
		expiresAt = session.ExpiresAt.UTC().Format(time.RFC3339)
	}
	data := fiber.Map{
		"payment_id":    session.ID.String(),
		"track_id":      session.SessionToken,
		"session_token": session.SessionToken,
		"checkout_url":  checkoutURL,
		"status":        paymentSessionResponseStatus(session, now),
		"expires_at":    expiresAt,
		"order_id":      session.OrderID,
		"link_type":     models.NormalizePaymentLinkType(session.LinkType),
		"amount":        session.Amount,
		"currency":      session.Currency,
		"wallet_id":     walletID.String(),
	}
	if session.SelectedChainID != nil {
		data["chain_id"] = int64(*session.SelectedChainID)
	}
	copySessionSelectedFields(data, session)
	return data
}

func copySelectedPaymentFields(dst fiber.Map, src fiber.Map) {
	for _, key := range []string{"chain_id", "symbol", "token", "decimals", "expected_amount_raw", "deposit_address"} {
		if value, ok := src[key]; ok {
			dst[key] = value
		}
	}
}

func copySessionSelectedFields(dst fiber.Map, session models.PaymentSession) {
	if session.SelectedSymbol == "" && session.ExpectedAmountRaw == "" && session.DepositAddress == "" {
		return
	}
	if session.SelectedSymbol != "" {
		dst["symbol"] = session.SelectedSymbol
	}
	if session.SelectedToken != nil {
		dst["token"] = *session.SelectedToken
	}
	if session.SelectedDecimals != 0 {
		dst["decimals"] = session.SelectedDecimals
	}
	if session.ExpectedAmountRaw != "" {
		dst["expected_amount_raw"] = session.ExpectedAmountRaw
	}
	if session.DepositAddress != "" {
		dst["deposit_address"] = session.DepositAddress
	}
}

func paymentSessionResponseStatus(session models.PaymentSession, now time.Time) string {
	if models.IsDonationLinkType(session.LinkType) && session.Status == models.PaymentStatusExpired {
		if session.SelectedChainID != nil && strings.TrimSpace(session.SelectedSymbol) != "" && strings.TrimSpace(session.DepositAddress) != "" {
			return models.PaymentStatusAwaitingPayment
		}
		return models.PaymentStatusPending
	}
	if paymentStatusTerminal(session.Status) && !paymentSessionAggregatePartial(session) {
		return session.Status
	}
	if !models.IsDonationLinkType(session.LinkType) && !now.IsZero() && session.ExpiresAt != nil && now.After(*session.ExpiresAt) {
		return models.PaymentStatusExpired
	}
	return session.Status
}

func paymentSessionTerminal(session models.PaymentSession) bool {
	if models.IsDonationLinkType(session.LinkType) && session.Status == models.PaymentStatusExpired {
		return false
	}
	if paymentSessionAggregatePartial(session) {
		return false
	}
	return paymentStatusTerminal(session.Status)
}

func paymentSessionAggregatePartial(session models.PaymentSession) bool {
	return session.Status == models.PaymentStatusPartialPaid && session.PaymentOutcome == models.PaymentOutcomePartialAggregating
}

func paymentCreateSettlementPolicy(params types.PaymentCreateParams) string {
	if params.SettlementPolicy != nil && strings.EqualFold(strings.TrimSpace(*params.SettlementPolicy), models.PaymentSettlementPolicyAggregate) {
		return models.PaymentSettlementPolicyAggregate
	}
	return models.PaymentSettlementPolicySingle
}

func normalizePaymentMemoForHandler(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func paymentStatusTerminal(status string) bool {
	switch status {
	case models.PaymentStatusPaid,
		models.PaymentStatusCanceled,
		models.PaymentStatusExpired,
		models.PaymentStatusFailed,
		models.PaymentStatusUnderpaid,
		models.PaymentStatusOverpaid,
		models.PaymentStatusPartialPaid:
		return true
	default:
		return false
	}
}

const (
	checkoutStateActive      = "active"
	checkoutStatePending     = "pending"
	checkoutStateConfirming  = "confirming"
	checkoutStatePaid        = "paid"
	checkoutStateExpired     = "expired"
	checkoutStateCanceled    = "canceled"
	checkoutStateFailed      = "failed"
	checkoutStateUnderpaid   = "underpaid"
	checkoutStateOverpaid    = "overpaid"
	checkoutStatePartialPaid = "partial_paid"
)

type checkoutPaymentState struct {
	Status      string
	Mode        string
	TitleEN     string
	TitleTR     string
	BodyEN      string
	BodyTR      string
	Paid        bool
	Payable     bool
	Terminal    bool
	PaymentID   string
	TxHash      string
	SuccessPath string
	ResultPath  string
	CancelPath  string
}

func checkoutPayerState(session models.PaymentSession, now time.Time) checkoutPaymentState {
	state := checkoutPaymentState{
		PaymentID:   uuidString(session.ID),
		TxHash:      valueOrDefault(session.TxHash, ""),
		SuccessPath: "/checkout/" + session.SessionToken + "/return/success",
		ResultPath:  "/checkout/" + session.SessionToken + "/pay",
		CancelPath:  "/checkout/" + session.SessionToken + "/cancel",
	}
	status := paymentSessionResponseStatus(session, now)
	switch status {
	case models.PaymentStatusPaid:
		state.Status = checkoutStatePaid
		state.Mode = "paid"
		state.Paid = true
		state.Terminal = true
		state.TitleEN = "Paid"
		state.TitleTR = "Ödendi"
		state.BodyEN = "Payment received successfully."
		state.BodyTR = "Ödeme başarıyla alındı."
	case models.PaymentStatusExpired:
		state.Status = checkoutStateExpired
		state.Mode = "expired"
		state.Terminal = true
		state.TitleEN = "Expired"
		state.TitleTR = "Suresi doldu"
		state.BodyEN = "This checkout can no longer receive a payment."
		state.BodyTR = "Bu checkout artik odeme alamaz."
	case models.PaymentStatusCanceled:
		state.Status = checkoutStateCanceled
		state.Mode = "canceled"
		state.Terminal = true
		state.TitleEN = "Canceled"
		state.TitleTR = "Iptal edildi"
		state.BodyEN = "This checkout was canceled."
		state.BodyTR = "Bu checkout iptal edildi."
	case models.PaymentStatusFailed:
		state.Status = checkoutStateFailed
		state.Mode = "failed"
		state.Terminal = true
		state.TitleEN = "Failed"
		state.TitleTR = "Basarisiz"
		state.BodyEN = "The payment could not be completed."
		state.BodyTR = "Odeme tamamlanamadi."
	case models.PaymentStatusUnderpaid:
		state.Status = checkoutStateUnderpaid
		state.Mode = "underpaid"
		state.Terminal = true
		state.TitleEN = "Underpaid"
		state.TitleTR = "Eksik odeme"
		state.BodyEN = "The received amount is below the expected amount."
		state.BodyTR = "Alinan tutar beklenen tutarin altinda."
	case models.PaymentStatusOverpaid:
		state.Status = checkoutStateOverpaid
		state.Mode = "overpaid"
		state.Terminal = true
		state.TitleEN = "Overpaid"
		state.TitleTR = "Fazla odeme"
		state.BodyEN = "The received amount exceeds the expected amount."
		state.BodyTR = "Alinan tutar beklenen tutarin uzerinde."
	case models.PaymentStatusPartialPaid:
		state.Status = checkoutStatePartialPaid
		state.Mode = "partial_paid"
		state.TitleEN = "Partial payment received"
		state.TitleTR = "Kismi odeme alindi"
		if session.PaymentOutcome == models.PaymentOutcomePartialAggregating {
			state.Payable = true
			state.BodyEN = "The received amount is being aggregated toward this checkout."
			state.BodyTR = "Alinan tutar bu checkout icin birikimli olarak takip ediliyor."
		} else {
			state.Terminal = true
			state.BodyEN = "The received amount partially covers this checkout and needs follow-up."
			state.BodyTR = "Alinan tutar checkout'u kismen karsiliyor ve takip gerektiriyor."
		}
	case models.PaymentStatusAwaitingPayment:
		if strings.TrimSpace(state.TxHash) != "" || session.ConfirmedAt != nil {
			state.Status = checkoutStateConfirming
			state.Mode = "confirming"
			state.Payable = true
			state.TitleEN = "Payment confirming"
			state.TitleTR = "Odeme onaylaniyor"
			state.BodyEN = "A transaction was detected and is waiting for final settlement."
			state.BodyTR = "Islem algilandi ve final onay bekleniyor."
			return state
		}
		state.Status = checkoutStateActive
		state.Mode = "detecting"
		state.Payable = true
		state.TitleEN = "Waiting for payment"
		state.TitleTR = "Odeme bekleniyor"
		if models.IsDonationLinkType(session.LinkType) {
			state.BodyEN = "Choose the amount in your wallet and send it to the address below."
			state.BodyTR = "Tutari wallet uygulamanda belirleyip asagidaki adrese gonder."
		} else {
			state.BodyEN = "Send the exact amount to the address below."
			state.BodyTR = "Tam tutari asagidaki adrese gonder."
		}
	default:
		state.Status = checkoutStatePending
		state.Mode = "detecting"
		state.TitleEN = "Waiting for asset selection"
		state.TitleTR = "Asset secimi bekleniyor"
		state.BodyEN = "Choose an asset and network to continue."
		state.BodyTR = "Devam etmek icin asset ve network sec."
	}
	return state
}

func checkoutStatusPayload(session models.PaymentSession, now time.Time) fiber.Map {
	state := checkoutPayerState(session, now)
	payload := fiber.Map{
		"success":      true,
		"status":       state.Status,
		"paid":         state.Paid,
		"payment_id":   state.PaymentID,
		"success_path": state.SuccessPath,
		"cancel_path":  state.CancelPath,
		"payable":      state.Payable,
		"terminal":     state.Terminal,
	}
	if state.TxHash != "" {
		payload["tx_hash"] = state.TxHash
	}
	if state.Terminal && !state.Paid {
		payload["result_path"] = state.ResultPath
	}
	copyPaymentOutcomeFields(payload, session)
	return payload
}

func copyPaymentOutcomeFields(dst fiber.Map, session models.PaymentSession) {
	if session.PaymentOutcome != "" {
		dst["payment_outcome"] = session.PaymentOutcome
	}
	if session.PaymentOutcomeReason != "" {
		dst["payment_outcome_reason"] = session.PaymentOutcomeReason
	}
	if session.MatchedAmountRaw != "" {
		dst["matched_amount_raw"] = session.MatchedAmountRaw
	}
	if session.ShortfallAmountRaw != "" {
		dst["shortfall_amount_raw"] = session.ShortfallAmountRaw
	}
	if session.ExcessAmountRaw != "" {
		dst["excess_amount_raw"] = session.ExcessAmountRaw
	}
}

func checkoutRealtimeEvent(session models.PaymentSession, now time.Time) realtime.PaymentEvent {
	state := checkoutPayerState(session, now)
	event := realtime.PaymentEvent{
		Status:      state.Status,
		Paid:        state.Paid,
		PaymentID:   state.PaymentID,
		TxHash:      state.TxHash,
		SuccessPath: state.SuccessPath,
		CancelPath:  state.CancelPath,
		Payable:     state.Payable,
		Terminal:    state.Terminal,
	}
	if state.Terminal && !state.Paid {
		event.ResultPath = state.ResultPath
	}
	return event
}

// HandleCheckout renders the asset selection page for a checkout session.
// @Summary Show checkout
// @Description Renders the hosted checkout page where the payer selects a network and asset.
// @Tags Payments
// @Produce html
// @Param token path string true "Payment session token"
// @Success 200 {string} string "HTML checkout page"
// @Failure 404 {string} string "HTML error page"
// @Failure 410 {string} string "HTML error page"
// @Router /checkout/{token} [get]
func HandleCheckout(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return renderPaymentError(c, fiber.StatusNotFound, "Payment session was not found.")
		}
		lang := checkoutLanguage(c)
		checkoutState := checkoutPayerState(*session, time.Now())
		if checkoutState.Terminal {
			return renderCheckoutStateResultWithDeps(c, fiber.StatusOK, checkoutState, deps, session)
		}
		effectiveStatus := paymentSessionResponseStatus(*session, time.Now())
		if effectiveStatus == models.PaymentStatusPaid {
			return c.Redirect().To(checkoutLocalizedURL(session.SessionToken, "/pay", lang, ""))
		}
		if effectiveStatus == models.PaymentStatusAwaitingPayment || paymentSessionAggregatePartial(*session) {
			if session.SelectedChainID != nil {
				available, reason, availabilityErr := paymentNetworkDepositAvailability(c.Context(), deps.NetworkOperationalStateRepo, *session.SelectedChainID)
				if availabilityErr != nil {
					return renderPaymentError(c, fiber.StatusServiceUnavailable, checkoutNetworkAvailabilityError)
				}
				if !available {
					return renderPaymentError(c, fiber.StatusServiceUnavailable, reason)
				}
			}
			return c.Redirect().To(checkoutLocalizedURL(session.SessionToken, "/pay", lang, ""))
		}
		if isSessionExpired(session) {
			if err := markPaymentCanceledOrExpired(c.Context(), deps, session, models.PaymentStatusExpired); err != nil {
				return renderPaymentError(c, fiber.StatusInternalServerError, "Could not expire this checkout session.")
			}
			statusCode := fiber.StatusOK
			if session.Status == models.PaymentStatusExpired {
				statusCode = fiber.StatusGone
			}
			return renderCheckoutStateResultWithDeps(c, statusCode, checkoutPayerState(*session, time.Now()), deps, session)
		}

		selectedSymbol := strings.ToUpper(strings.TrimSpace(c.Query("asset")))
		session = ensureCheckoutPickerWalletAddresses(c.Context(), deps, session)
		options := checkoutAssetOptions(c.Context(), deps, *session, "")
		productName, productDesc, productLogo := lookupCheckoutProduct(c.Context(), deps, session.ProductID)
		return c.Render("gateway/checkout", fiber.Map{
			"Session":            session,
			"Lang":               lang,
			"IsEnglish":          lang == "en",
			"AssetGroups":        checkoutAssetGroups(c.Context(), deps, *session),
			"SelectedSymbol":     selectedSymbol,
			"Assets":             options,
			"IsDonation":         models.IsDonationLinkType(session.LinkType),
			"ExpiresAtUnix":      checkoutExpiresAtUnix(session),
			"Error":              "",
			"ProductName":        productName,
			"ProductDescription": productDesc,
			"ProductLogoURL":     productLogo,
			"CheckoutURL":        checkoutLocalizedURL(session.SessionToken, "", lang, ""),
			"SelectURL":          checkoutLocalizedURL(session.SessionToken, "/select", lang, ""),
			"LangTRURL":          checkoutLocalizedURL(session.SessionToken, "", "tr", selectedSymbol),
			"LangENURL":          checkoutLocalizedURL(session.SessionToken, "", "en", selectedSymbol),
		})
	}
}

func checkoutMutationPostRequired(c fiber.Ctx) error {
	c.Set("Allow", fiber.MethodPost)
	return c.Status(fiber.StatusMethodNotAllowed).SendString("method not allowed")
}

// HandleCheckoutSelectAsset selects the chain and asset for a checkout session.
// @Summary Select checkout asset
// @Description Stores the selected network and asset, computes the raw expected amount, and redirects to the payment page.
// @Tags Payments
// @Accept mpfd
// @Produce html
// @Param token path string true "Payment session token"
// @Param chain_id formData integer true "Chain ID"
// @Param symbol formData string true "Asset symbol"
// @Param token formData string false "Token contract address or asset identifier"
// @Success 303 {string} string "Redirect to payment page"
// @Failure 400 {string} string "HTML checkout page with validation error"
// @Failure 404 {string} string "HTML error page"
// @Failure 410 {string} string "HTML error page"
// @Router /checkout/{token}/select [post]
func HandleCheckoutSelectAsset(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodPost {
			return checkoutMutationPostRequired(c)
		}
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return renderPaymentError(c, fiber.StatusNotFound, "Payment session was not found.")
		}
		lang := checkoutLanguage(c)
		if paymentSessionTerminal(*session) {
			if session.Status == models.PaymentStatusPaid {
				return c.Redirect().To(checkoutLocalizedURL(session.SessionToken, "/return/success", lang, ""))
			}
			return renderCheckoutStateResultWithDeps(c, fiber.StatusOK, checkoutPayerState(*session, time.Now()), deps, session)
		}
		if paymentSessionAggregatePartial(*session) {
			return renderCheckoutStateResultWithDeps(c, fiber.StatusOK, checkoutPayerState(*session, time.Now()), deps, session)
		}
		if isSessionExpired(session) {
			if err := markPaymentCanceledOrExpired(c.Context(), deps, session, models.PaymentStatusExpired); err != nil {
				return renderPaymentError(c, fiber.StatusInternalServerError, "Could not expire this checkout session.")
			}
			if session.Status == models.PaymentStatusPaid {
				return c.Redirect().To(checkoutLocalizedURL(session.SessionToken, "/return/success", lang, ""))
			}
			return renderPaymentError(c, fiber.StatusGone, "This payment session has expired.")
		}

		chainID, err := strconv.ParseInt(c.FormValue("chain_id"), 10, 64)
		if err != nil {
			return renderCheckoutWithError(c, deps, session, "Select a valid network.")
		}
		symbol := strings.ToUpper(strings.TrimSpace(c.FormValue("symbol")))
		token := strings.TrimSpace(c.FormValue("token"))

		assetInfo, err := findCheckoutAsset(deps.AssetRegistry, constants.ChainID(chainID), symbol, token)
		if err != nil {
			return renderCheckoutWithError(c, deps, session, err.Error())
		}
		if !checkoutAssetAllowedForSession(*session, assetInfo) {
			return renderCheckoutWithError(c, deps, session, "Selected asset is not supported by x402.")
		}
		available, reason, availabilityErr := paymentNetworkDepositAvailability(c.Context(), deps.NetworkOperationalStateRepo, assetInfo.GetChainID())
		if availabilityErr != nil {
			return renderCheckoutWithErrorStatus(c, deps, session, fiber.StatusServiceUnavailable, checkoutNetworkAvailabilityError)
		}
		if !available {
			return renderCheckoutWithErrorStatus(c, deps, session, fiber.StatusServiceUnavailable, reason)
		}

		if paymentDepositAddressForChain(session.Wallet, assetInfo.GetChainID()) == "" {
			session = ensureCheckoutWalletAddresses(c.Context(), deps, session)
		}

		depositAddress := paymentDepositAddressForChain(session.Wallet, assetInfo.GetChainID())
		if depositAddress == "" {
			return renderCheckoutWithError(c, deps, session, "Deposit address is not available for this network.")
		}

		var selectedToken *string
		if !assetInfo.IsNative() {
			identifier := assetInfo.GetIdentifier()
			selectedToken = &identifier
		}

		amountRaw := ""
		var quote *models.PriceQuote
		if !models.IsDonationLinkType(session.LinkType) {
			var quotePrice, quoteSource string
			amountRaw, quotePrice, quoteSource, err = checkoutExpectedQuote(c.Context(), deps.PriceOracle, *session, assetInfo)
			if err != nil {
				return renderCheckoutWithError(c, deps, session, err.Error())
			}
			quotedAt := time.Now()
			quoteExpiresAt := quotedAt.Add(15 * time.Minute)
			if session.ExpiresAt != nil && session.ExpiresAt.Before(quoteExpiresAt) {
				quoteExpiresAt = *session.ExpiresAt
			}
			quote = &models.PriceQuote{
				ChainID:           assetInfo.GetChainID(),
				Token:             selectedToken,
				Symbol:            assetInfo.GetSymbol(),
				Decimals:          assetInfo.GetDecimals(),
				FiatCurrency:      strings.ToUpper(strings.TrimSpace(session.Currency)),
				FiatAmount:        strings.TrimSpace(session.Amount),
				ExpectedAmountRaw: amountRaw,
				PriceSource:       quoteSource,
				Price:             quotePrice,
				QuotedAt:          quotedAt,
				ExpiresAt:         quoteExpiresAt,
				CreatedAt:         quotedAt,
				UpdatedAt:         quotedAt,
			}
			if quote.FiatCurrency == "" {
				quote.FiatCurrency = "USD"
			}
		}
		updatedSession, err := deps.PaymentRepo.SelectAsset(
			c.Context(),
			session.SessionToken,
			assetInfo.GetChainID(),
			assetInfo.GetSymbol(),
			selectedToken,
			assetInfo.GetDecimals(),
			amountRaw,
			depositAddress,
			quote,
		)
		if err != nil {
			return renderCheckoutWithError(c, deps, session, "Could not select this asset.")
		}

		return c.Redirect().To(checkoutLocalizedURL(updatedSession.SessionToken, "/pay", lang, ""))
	}
}

// HandleCheckoutChangeAsset clears the selected asset so the payer can choose again.
// @Summary Change checkout asset
// @Description Resets a pending or awaiting payment session back to asset selection.
// @Tags Payments
// @Produce html
// @Param token path string true "Payment session token"
// @Success 303 {string} string "Redirect to checkout page"
// @Failure 404 {string} string "HTML error page"
// @Failure 410 {string} string "HTML error page"
// @Router /checkout/{token}/change [post]
func HandleCheckoutChangeAsset(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodPost {
			return checkoutMutationPostRequired(c)
		}
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return renderPaymentError(c, fiber.StatusNotFound, "Payment session was not found.")
		}
		lang := checkoutLanguage(c)
		if paymentSessionTerminal(*session) {
			if session.Status == models.PaymentStatusPaid {
				return c.Redirect().To(checkoutLocalizedURL(session.SessionToken, "/return/success", lang, ""))
			}
			return renderCheckoutStateResultWithDeps(c, fiber.StatusOK, checkoutPayerState(*session, time.Now()), deps, session)
		}
		if paymentSessionAggregatePartial(*session) {
			return renderCheckoutStateResultWithDeps(c, fiber.StatusOK, checkoutPayerState(*session, time.Now()), deps, session)
		}
		if isSessionExpired(session) {
			if err := markPaymentCanceledOrExpired(c.Context(), deps, session, models.PaymentStatusExpired); err != nil {
				return renderPaymentError(c, fiber.StatusInternalServerError, "Could not expire this checkout session.")
			}
			if session.Status == models.PaymentStatusPaid {
				return c.Redirect().To(checkoutLocalizedURL(session.SessionToken, "/return/success", lang, ""))
			}
			return renderPaymentError(c, fiber.StatusGone, "This payment session has expired.")
		}
		updatedSession, err := deps.PaymentRepo.ResetSelection(c.Context(), session.SessionToken)
		if err != nil {
			return renderPaymentError(c, fiber.StatusInternalServerError, "Could not change this checkout asset.")
		}
		return c.Redirect().To(checkoutLocalizedURL(updatedSession.SessionToken, "", lang, ""))
	}
}

// HandleCheckoutPay renders the payment instruction page.
// @Summary Show payment instructions
// @Description Renders the QR code, payment address, selected chain, asset, and amount for a checkout session.
// @Tags Payments
// @Produce html
// @Param token path string true "Payment session token"
// @Success 200 {string} string "HTML payment page"
// @Failure 404 {string} string "HTML error page"
// @Failure 410 {string} string "HTML error page"
// @Router /checkout/{token}/pay [get]
func HandleCheckoutPay(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return renderPaymentError(c, fiber.StatusNotFound, "Payment session was not found.")
		}
		lang := checkoutLanguage(c)
		if isSessionExpired(session) && !paymentSessionTerminal(*session) {
			if err := markPaymentCanceledOrExpired(c.Context(), deps, session, models.PaymentStatusExpired); err != nil {
				return renderPaymentError(c, fiber.StatusInternalServerError, "Could not expire this checkout session.")
			}
			statusCode := fiber.StatusOK
			if session.Status == models.PaymentStatusExpired {
				statusCode = fiber.StatusGone
			}
			return renderCheckoutStateResultWithDeps(c, statusCode, checkoutPayerState(*session, time.Now()), deps, session)
		}
		if session.Status == models.PaymentStatusPaid {
			return c.Redirect().To(checkoutLocalizedURL(session.SessionToken, "/return/success", lang, ""))
		}
		checkoutState := checkoutPayerState(*session, time.Now())
		if checkoutState.Terminal {
			return renderCheckoutStateResultWithDeps(c, fiber.StatusOK, checkoutState, deps, session)
		}
		if session.SelectedChainID != nil {
			available, reason, availabilityErr := paymentNetworkDepositAvailability(c.Context(), deps.NetworkOperationalStateRepo, *session.SelectedChainID)
			if availabilityErr != nil {
				return renderPaymentError(c, fiber.StatusServiceUnavailable, checkoutNetworkAvailabilityError)
			}
			if !available {
				return renderPaymentError(c, fiber.StatusServiceUnavailable, reason)
			}
		}
		if session.Status == models.PaymentStatusPending {
			return c.Redirect().To(checkoutLocalizedURL(session.SessionToken, "", lang, ""))
		}

		if session.SelectedChainID == nil || session.SelectedSymbol == "" || session.DepositAddress == "" {
			return c.Redirect().To(checkoutLocalizedURL(session.SessionToken, "", lang, ""))
		}
		amountDisplay := formatPaymentAmount(session.ExpectedAmountRaw, session.SelectedDecimals, session.SelectedSymbol)
		if models.IsDonationLinkType(session.LinkType) {
			amountDisplay = strings.ToUpper(strings.TrimSpace(session.SelectedSymbol))
			if session.MatchedAmountRaw != "" {
				amountDisplay = formatPaymentAmount(session.MatchedAmountRaw, session.SelectedDecimals, session.SelectedSymbol)
			}
		}
		statusTitle := checkoutState.TitleTR
		statusBody := checkoutState.BodyTR
		if lang == "en" {
			statusTitle = checkoutState.TitleEN
			statusBody = checkoutState.BodyEN
		}
		qrURL := "/checkout/" + session.SessionToken + "/qr.png"
		productName, productDesc, productLogo := lookupCheckoutProduct(c.Context(), deps, session.ProductID)
		selectedLogoURL := ""
		if deps.AssetRegistry != nil {
			selectedLogoURL = deps.AssetRegistry.LogoURL(session.SelectedSymbol)
		}
		return c.Render("gateway/pay", fiber.Map{
			"Session":              session,
			"Lang":                 lang,
			"IsEnglish":            lang == "en",
			"QRCodeURL":            qrURL,
			"PaymentURI":           paymentURI(*session),
			"ChainName":            checkoutChainName(*session.SelectedChainID),
			"ChainLogoURL":         asset.ChainLogoURL(*session.SelectedChainID),
			"AmountDisplay":        amountDisplay,
			"IsDonation":           models.IsDonationLinkType(session.LinkType),
			"ExpiresAtUnix":        checkoutExpiresAtUnix(session),
			"ProductName":          productName,
			"ProductDescription":   productDesc,
			"ProductLogoURL":       productLogo,
			"SelectedAssetLogoURL": selectedLogoURL,
			"CheckoutState":        checkoutState,
			"StatusMode":           checkoutState.Mode,
			"StatusTitle":          statusTitle,
			"StatusBody":           statusBody,
			"CheckoutURL":          checkoutLocalizedURL(session.SessionToken, "", lang, ""),
			"ChangeAssetURL":       checkoutLocalizedURL(session.SessionToken, "/change", lang, ""),
			"CancelURL":            checkoutLocalizedURL(session.SessionToken, "/cancel", lang, ""),
			"LangTRURL":            checkoutLocalizedURL(session.SessionToken, "/pay", "tr", ""),
			"LangENURL":            checkoutLocalizedURL(session.SessionToken, "/pay", "en", ""),
		})
	}
}

// HandleCheckoutSocket streams live checkout status updates over WebSocket.
// @Summary Subscribe checkout status
// @Description Opens a WebSocket connection that emits payment status changes for the checkout session.
// @Tags Payments
// @Param token path string true "Payment session token"
// @Router /checkout/{token}/ws [get]
func HandleCheckoutSocket(deps PaymentHandlerDeps) fiber.Handler {
	upgrader := websocket.FastHTTPUpgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
	return func(c fiber.Ctx) error {
		if deps.PaymentHub == nil {
			return c.SendStatus(fiber.StatusServiceUnavailable)
		}
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return c.SendStatus(fiber.StatusNotFound)
		}
		if isSessionExpired(session) && !paymentSessionTerminal(*session) {
			if err := markPaymentCanceledOrExpired(c.Context(), deps, session, models.PaymentStatusExpired); err != nil {
				return c.SendStatus(fiber.StatusInternalServerError)
			}
			if session.Status == models.PaymentStatusExpired {
				return c.SendStatus(fiber.StatusGone)
			}
		}
		return upgrader.Upgrade(c.RequestCtx(), func(conn *websocket.Conn) {
			deps.PaymentHub.Subscribe(session.SessionToken, conn, checkoutRealtimeEvent(*session, time.Now()))
		})
	}
}

// HandleCheckoutQRCode returns the QR code PNG for a checkout session.
// @Summary Get checkout QR code
// @Description Returns a PNG QR code containing the payment URI or deposit address.
// @Tags Payments
// @Produce png
// @Param token path string true "Payment session token"
// @Success 200 {file} binary "PNG QR code"
// @Failure 404 {string} string "Not found"
// @Failure 500 {string} string "QR generation failed"
// @Router /checkout/{token}/qr.png [get]
func HandleCheckoutQRCode(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return c.SendStatus(fiber.StatusNotFound)
		}
		if !paymentSessionTerminal(*session) && session.SelectedChainID != nil {
			available, reason, availabilityErr := paymentNetworkDepositAvailability(c.Context(), deps.NetworkOperationalStateRepo, *session.SelectedChainID)
			if availabilityErr != nil {
				return c.Status(fiber.StatusServiceUnavailable).SendString(checkoutNetworkAvailabilityError)
			}
			if !available {
				return c.Status(fiber.StatusServiceUnavailable).SendString(reason)
			}
		}
		payload := paymentURI(*session)
		if payload == "" {
			payload = session.DepositAddress
		}
		png, err := qrcode.Encode(payload, qrcode.Medium, 300)
		if err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		c.Set("Content-Type", "image/png")
		return c.Send(png)
	}
}

// HandleCheckoutStatus returns the current checkout payment status.
// @Summary Get checkout status
// @Description Returns whether the checkout session is paid and the next hosted checkout paths.
// @Tags Payments
// @Produce json
// @Param token path string true "Payment session token"
// @Success 200 {object} types.PaymentStatusResponse
// @Failure 404 {object} types.ErrorResponse
// @Router /checkout/{token}/status.json [get]
func HandleCheckoutStatus(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"success": false})
		}
		if isSessionExpired(session) && !paymentSessionTerminal(*session) {
			if err := markPaymentCanceledOrExpired(c.Context(), deps, session, models.PaymentStatusExpired); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"success": false,
					"message": "could not expire checkout session",
				})
			}
		}
		return c.JSON(checkoutStatusPayload(*session, time.Now()))
	}
}

// HandleCheckoutCancel cancels a checkout session.
// @Summary Cancel checkout
// @Description Cancels the hosted checkout session, sends a payment_failed webhook when applicable, and redirects to the merchant cancel URL.
// @Tags Payments
// @Produce html
// @Param token path string true "Payment session token"
// @Success 200 {string} string "Rendered canceled result page"
// @Success 303 {string} string "Redirect to merchant cancel URL"
// @Failure 404 {string} string "HTML error page"
// @Router /checkout/{token}/cancel [post]
func HandleCheckoutCancel(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodPost {
			return checkoutMutationPostRequired(c)
		}
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return renderPaymentError(c, fiber.StatusNotFound, "Payment session was not found.")
		}
		lang := checkoutLanguage(c)
		if paymentSessionAggregatePartial(*session) {
			return renderCheckoutStateResultWithDeps(c, fiber.StatusOK, checkoutPayerState(*session, time.Now()), deps, session)
		}
		if !paymentSessionTerminal(*session) {
			session, _, err = deps.PaymentRepo.Cancel(c.Context(), session.SessionToken)
			if err != nil {
				return renderPaymentError(c, fiber.StatusInternalServerError, "Could not cancel this checkout.")
			}
			deliverPaymentWebhook(c.Context(), deps, session)
		}
		if session.CancelURL != "" {
			return c.Redirect().To(session.CancelURL)
		}
		title := "Payment canceled"
		message := "The checkout session was canceled."
		if lang != "en" {
			title = "Ödeme iptal edildi"
			message = "Checkout oturumu iptal edildi."
		}
		return c.Render("gateway/payment_result", checkoutPaymentResultViewData(c, lang, deps.AssetRegistry, checkoutResultBrandForSession(c.Context(), deps, session), session, title, message, session.Status, "canceled"))
	}
}

// HandleCheckoutSuccessReturn redirects successful checkout sessions back to the merchant.
// @Summary Return after successful payment
// @Description Redirects a paid checkout session to the merchant success URL or renders the built-in success page.
// @Tags Payments
// @Produce html
// @Param token path string true "Payment session token"
// @Success 302 {string} string "Redirect to merchant success URL or rendered result page"
// @Failure 404 {string} string "HTML error page"
// @Router /checkout/{token}/return/success [get]
func HandleCheckoutSuccessReturn(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return renderPaymentError(c, fiber.StatusNotFound, "Payment session was not found.")
		}
		lang := checkoutLanguage(c)
		if session.Status != models.PaymentStatusPaid {
			return c.Redirect().To(checkoutLocalizedURL(session.SessionToken, "/pay", lang, ""))
		}
		if session.SuccessURL != "" {
			return c.Redirect().To(session.SuccessURL)
		}
		title := "Payment complete"
		message := "Payment received successfully."
		if lang != "en" {
			title = "Ödeme tamamlandı"
			message = "Ödeme başarıyla alındı."
		}
		return c.Render("gateway/payment_result", checkoutPaymentResultViewData(c, lang, deps.AssetRegistry, checkoutResultBrandForSession(c.Context(), deps, session), session, title, message, session.Status, "success"))
	}
}

func resolvePaymentDomain(c fiber.Ctx, repo paymentDomainLookup, params types.PaymentCreateParams) (*models.Domain, error) {
	apiKey := strings.TrimSpace(c.Get("X-API-Key"))
	if apiKey == "" {
		auth := strings.TrimSpace(c.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			apiKey = strings.TrimSpace(auth[7:])
		}
	}
	if apiKey != "" {
		if repo == nil {
			return nil, errors.New("domain repository is not configured")
		}
		return repo.FindByAPIKey(types.DomainParams{
			Context: params.Context,
			APIKey:  &apiKey,
		})
	}
	return nil, errors.New("X-API-Key header or Authorization: Bearer <key> is required")
}

func paymentIdempotencyKey(c fiber.Ctx, params types.PaymentCreateParams) string {
	key := strings.TrimSpace(c.Get("Idempotency-Key"))
	if key != "" {
		return key
	}
	if params.OrderID == nil {
		return ""
	}
	return "order:" + strings.TrimSpace(*params.OrderID)
}

func ensureCheckoutWalletAddresses(ctx context.Context, deps PaymentHandlerDeps, session *models.PaymentSession) *models.PaymentSession {
	if session == nil || deps.WalletRepo == nil || deps.PaymentRepo == nil || deps.Blockchains == nil {
		return session
	}
	if err := deps.WalletRepo.EnsureAllAddresses(ctx, session.WalletID, deps.Blockchains); err != nil {
		return session
	}
	refreshed, err := deps.PaymentRepo.FindByToken(ctx, session.SessionToken)
	if err != nil {
		return session
	}
	return refreshed
}

func ensureCheckoutPickerWalletAddresses(ctx context.Context, deps PaymentHandlerDeps, session *models.PaymentSession) *models.PaymentSession {
	if session == nil || deps.AssetRegistry == nil {
		return session
	}
	for _, assetInfo := range deps.AssetRegistry.ListAll() {
		if !checkoutAssetAllowedForSession(*session, assetInfo) {
			continue
		}
		if paymentDepositAddressForChain(session.Wallet, assetInfo.GetChainID()) == "" {
			return ensureCheckoutWalletAddresses(ctx, deps, session)
		}
	}
	return session
}

func checkoutAssetGroups(ctx context.Context, deps PaymentHandlerDeps, session models.PaymentSession) []CheckoutAssetGroup {
	if deps.AssetRegistry == nil {
		return nil
	}
	assets := deps.AssetRegistry.ListAll()
	bySymbol := make(map[string]CheckoutAssetGroup)
	seenChains := make(map[string]map[constants.ChainID]struct{})
	availabilityByChain := make(map[constants.ChainID]paymentNetworkDepositAvailabilityResult)
	for _, assetInfo := range assets {
		if !checkoutAssetAllowedForSession(session, assetInfo) {
			continue
		}
		if paymentDepositAddressForChain(session.Wallet, assetInfo.GetChainID()) == "" {
			continue
		}
		available, _, err := cachedPaymentNetworkDepositAvailability(ctx, deps.NetworkOperationalStateRepo, availabilityByChain, assetInfo.GetChainID())
		if err != nil || !available {
			continue
		}
		symbol := canonicalSymbol(deps.AssetRegistry, assetInfo.GetSymbol())
		group, ok := bySymbol[symbol]
		if !ok {
			group = CheckoutAssetGroup{
				Symbol:        symbol,
				Name:          checkoutGroupName(symbol),
				AmountDisplay: "",
				URL:           "/checkout/" + session.SessionToken + "?asset=" + url.QueryEscape(symbol),
				LogoURL:       deps.AssetRegistry.LogoURL(symbol),
			}
			seenChains[symbol] = make(map[constants.ChainID]struct{})
		}
		seenChains[symbol][assetInfo.GetChainID()] = struct{}{}
		group.ChainCount = len(seenChains[symbol])
		bySymbol[symbol] = group
	}
	groups := make([]CheckoutAssetGroup, 0, len(bySymbol))
	for _, group := range bySymbol {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Symbol < groups[j].Symbol
	})
	return groups
}

func checkoutAssetOptions(ctx context.Context, deps PaymentHandlerDeps, session models.PaymentSession, selectedSymbol string) []CheckoutAssetOption {
	if deps.AssetRegistry == nil {
		return nil
	}
	selectedSymbol = canonicalSymbol(deps.AssetRegistry, selectedSymbol)
	assets := deps.AssetRegistry.ListAll()
	options := make([]CheckoutAssetOption, 0, len(assets))
	availabilityByChain := make(map[constants.ChainID]paymentNetworkDepositAvailabilityResult)
	for _, assetInfo := range assets {
		if !checkoutAssetAllowedForSession(session, assetInfo) {
			continue
		}
		groupSymbol := canonicalSymbol(deps.AssetRegistry, assetInfo.GetSymbol())
		if selectedSymbol != "" && !strings.EqualFold(groupSymbol, selectedSymbol) {
			continue
		}
		addressAvailable := paymentDepositAddressForChain(session.Wallet, assetInfo.GetChainID()) != ""
		networkAvailable, networkUnavailableReason, availabilityErr := cachedPaymentNetworkDepositAvailability(ctx, deps.NetworkOperationalStateRepo, availabilityByChain, assetInfo.GetChainID())
		unavailableReason := ""
		if availabilityErr != nil {
			unavailableReason = checkoutNetworkAvailabilityError
		} else if !networkAvailable {
			unavailableReason = networkUnavailableReason
		} else if !addressAvailable {
			unavailableReason = "address"
		}
		token := ""
		if !assetInfo.IsNative() {
			token = assetInfo.GetIdentifier()
		}
		options = append(options, CheckoutAssetOption{
			ChainID:           int64(assetInfo.GetChainID()),
			ChainName:         checkoutChainName(assetInfo.GetChainID()),
			ChainLogoURL:      asset.ChainLogoURL(assetInfo.GetChainID()),
			GroupSymbol:       groupSymbol,
			Symbol:            assetInfo.GetSymbol(),
			Name:              assetInfo.GetName(),
			Token:             token,
			Decimals:          assetInfo.GetDecimals(),
			AmountRaw:         "",
			AmountDisplay:     "",
			Native:            assetInfo.IsNative(),
			Available:         addressAvailable && networkAvailable && availabilityErr == nil,
			QuoteAvailable:    false,
			UnavailableReason: unavailableReason,
			LogoURL:           deps.AssetRegistry.LogoURL(assetInfo.GetSymbol()),
		})
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].ChainName == options[j].ChainName {
			return options[i].Token < options[j].Token
		}
		return options[i].ChainName < options[j].ChainName
	})
	return options
}

// checkoutAssetAllowedForSession keeps the hosted picker and the selection
// mutation on the same x402 exact compatibility boundary. Normal checkout
// sessions retain the complete asset catalog. x402 sessions require a token
// contract/mint on a network supported by the exact EVM or SVM mechanisms;
// native assets cannot be represented by the current session-driven seller
// payment requirement.
func checkoutAssetAllowedForSession(session models.PaymentSession, assetInfo asset.Asset) bool {
	if !session.X402Enabled {
		return true
	}
	if assetInfo == nil || assetInfo.IsNative() || strings.TrimSpace(assetInfo.GetIdentifier()) == "" {
		return false
	}
	if assetInfo.GetChainType() != asset.ChainEVM && assetInfo.GetChainType() != asset.ChainSolana {
		return false
	}
	_, supported := x402svc.CheckoutNetworkForChain(assetInfo.GetChainID())
	return supported
}

func findCheckoutAsset(registry *asset.Registry, chainID constants.ChainID, symbol string, token string) (asset.Asset, error) {
	if registry == nil {
		return nil, errors.New("Selected asset is not available.")
	}
	if token != "" {
		if assetInfo, ok := registry.Get(chainID, token); ok && strings.EqualFold(assetInfo.GetSymbol(), symbol) {
			return assetInfo, nil
		}
		return nil, errors.New("Selected asset is not available.")
	}
	if assetInfo, ok := registry.GetNative(chainID); ok && strings.EqualFold(assetInfo.GetSymbol(), symbol) {
		return assetInfo, nil
	}
	return nil, errors.New("Selected asset is not available.")
}

// checkoutCanonicalSymbol is the registry-unaware fallback used only in contexts without a Registry.
// Prefer registry.CanonicalSymbol() wherever deps.AssetRegistry is available.
func checkoutCanonicalSymbol(symbol string) string {
	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "WBTC":
		return "BTC"
	case "WETH":
		return "ETH"
	case "WCHZ":
		return "CHZ"
	case "WBNB":
		return "BNB"
	default:
		return strings.ToUpper(strings.TrimSpace(symbol))
	}
}

// canonicalSymbol resolves via the registry when available, falling back to the static map.
func canonicalSymbol(registry interface{ CanonicalSymbol(string) string }, symbol string) string {
	if registry != nil {
		return registry.CanonicalSymbol(symbol)
	}
	return checkoutCanonicalSymbol(symbol)
}

func checkoutGroupName(symbol string) string {
	switch checkoutCanonicalSymbol(symbol) {
	case "BTC":
		return "Bitcoin"
	case "ETH":
		return "Ether"
	case "CHZ":
		return "Chiliz"
	default:
		return checkoutCanonicalSymbol(symbol)
	}
}

func checkoutExpectedAmountRaw(ctx context.Context, oracle pricing.PriceOracle, session models.PaymentSession, assetInfo asset.Asset) (string, error) {
	amountRaw, _, _, err := checkoutExpectedQuote(ctx, oracle, session, assetInfo)
	return amountRaw, err
}

func checkoutExpectedQuote(ctx context.Context, oracle pricing.PriceOracle, session models.PaymentSession, assetInfo asset.Asset) (string, string, string, error) {
	amount := strings.TrimSpace(session.Amount)
	currency := strings.ToUpper(strings.TrimSpace(session.Currency))
	symbol := strings.ToUpper(strings.TrimSpace(assetInfo.GetSymbol()))
	if currency == "" {
		currency = "USD"
	}
	if strings.EqualFold(currency, symbol) || strings.EqualFold(currency, checkoutCanonicalSymbol(symbol)) {
		raw, err := types.DecimalToRaw(amount, assetInfo.GetDecimals())
		return raw, "1", "fixed", err
	}
	if oracle == nil {
		return "", "", "", errors.New("price provider is not configured")
	}
	fiatAmount, ok := new(big.Rat).SetString(amount)
	if !ok || fiatAmount.Sign() <= 0 {
		return "", "", "", errors.New("invalid payment amount")
	}
	price, err := oracle.Price(ctx, symbol, currency)
	if err != nil {
		return "", "", "", err
	}
	if price == nil || price.Sign() <= 0 {
		return "", "", "", errors.New("price provider returned an invalid price")
	}
	tokenAmount := new(big.Rat).Quo(fiatAmount, price)
	raw, err := ratToRawCeil(tokenAmount, assetInfo.GetDecimals())
	if err != nil {
		return "", "", "", err
	}
	return raw, formatRatDecimal(price, 18), "price_oracle", nil
}

func ratToRawCeil(value *big.Rat, decimals uint8) (string, error) {
	if value == nil || value.Sign() <= 0 {
		return "", errors.New("amount must be greater than zero")
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	scaled := new(big.Rat).Mul(value, new(big.Rat).SetInt(scale))
	numerator := new(big.Int).Set(scaled.Num())
	denominator := scaled.Denom()
	raw := new(big.Int).Quo(numerator, denominator)
	if new(big.Int).Rem(numerator, denominator).Sign() > 0 {
		raw.Add(raw, big.NewInt(1))
	}
	if raw.Sign() <= 0 {
		return "", errors.New("amount must be greater than zero")
	}
	return raw.String(), nil
}

func formatRatDecimal(value *big.Rat, precision int) string {
	if value == nil {
		return ""
	}
	if precision < 0 {
		precision = 18
	}
	formatted := value.FloatString(precision)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "" {
		return "0"
	}
	return formatted
}

func formatPaymentAmount(raw string, decimals uint8, symbol string) string {
	return formatRawDecimal(raw, decimals) + " " + strings.ToUpper(strings.TrimSpace(symbol))
}

func checkoutChainName(chainID constants.ChainID) string {
	if chainID == constants.TRONTestnet {
		return "tron-nile"
	}
	return constants.ChainName(chainID)
}

func formatRawDecimal(raw string, decimals uint8) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "0"
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return raw
		}
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0"
	}
	if decimals == 0 {
		return value
	}
	precision := int(decimals)
	var whole string
	var fraction string
	if len(value) <= precision {
		whole = "0"
		fraction = strings.Repeat("0", precision-len(value)) + value
	} else {
		split := len(value) - precision
		whole = value[:split]
		fraction = value[split:]
	}
	fraction = strings.TrimRight(fraction, "0")
	if fraction == "" {
		return whole
	}
	return whole + "." + fraction
}

func paymentDepositAddressForChain(wallet models.Wallet, chainID constants.ChainID) string {
	address := walletAddressForChain(wallet, chainID)
	if address == "" {
		return ""
	}
	switch chainID {
	case constants.Solana:
		if strings.HasPrefix(strings.ToLower(address), "0x") {
			return ""
		}
	case constants.Bitcoin:
		if strings.HasPrefix(strings.ToLower(address), "0x") {
			return ""
		}
	case constants.TRON, constants.TRONTestnet:
		if strings.HasPrefix(strings.ToLower(address), "0x") {
			return ""
		}
	default:
		if !strings.HasPrefix(strings.ToLower(address), "0x") {
			return ""
		}
	}
	return address
}

func renderCheckoutWithError(c fiber.Ctx, deps PaymentHandlerDeps, session *models.PaymentSession, message string) error {
	return renderCheckoutWithErrorStatus(c, deps, session, fiber.StatusBadRequest, message)
}

func renderCheckoutWithErrorStatus(c fiber.Ctx, deps PaymentHandlerDeps, session *models.PaymentSession, status int, message string) error {
	session = ensureCheckoutPickerWalletAddresses(c.Context(), deps, session)
	lang := checkoutLanguage(c)
	selectedSymbol := strings.ToUpper(strings.TrimSpace(c.FormValue("symbol")))
	productName, productDesc, productLogo := lookupCheckoutProduct(c.Context(), deps, session.ProductID)
	return c.Status(status).Render("gateway/checkout", fiber.Map{
		"Session":            session,
		"Lang":               lang,
		"IsEnglish":          lang == "en",
		"AssetGroups":        checkoutAssetGroups(c.Context(), deps, *session),
		"SelectedSymbol":     selectedSymbol,
		"Assets":             checkoutAssetOptions(c.Context(), deps, *session, selectedSymbol),
		"IsDonation":         models.IsDonationLinkType(session.LinkType),
		"ExpiresAtUnix":      checkoutExpiresAtUnix(session),
		"Error":              message,
		"ProductName":        productName,
		"ProductDescription": productDesc,
		"ProductLogoURL":     productLogo,
		"CheckoutURL":        checkoutLocalizedURL(session.SessionToken, "", lang, ""),
		"SelectURL":          checkoutLocalizedURL(session.SessionToken, "/select", lang, ""),
		"LangTRURL":          checkoutLocalizedURL(session.SessionToken, "", "tr", selectedSymbol),
		"LangENURL":          checkoutLocalizedURL(session.SessionToken, "", "en", selectedSymbol),
	})
}

func renderPaymentError(c fiber.Ctx, status int, message string) error {
	lang := checkoutLanguage(c)
	return c.Status(status).Render("gateway/payment_result", checkoutPaymentResultViewData(c, lang, nil, checkoutResultBrand{}, nil, "Payment unavailable", message, "error", "error"))
}

func renderCheckoutStateResult(c fiber.Ctx, status int, state checkoutPaymentState, sessions ...*models.PaymentSession) error {
	return renderCheckoutStateResultWithRegistry(c, status, state, nil, sessions...)
}

func renderCheckoutStateResultWithDeps(c fiber.Ctx, status int, state checkoutPaymentState, deps PaymentHandlerDeps, sessions ...*models.PaymentSession) error {
	var session *models.PaymentSession
	if len(sessions) > 0 {
		session = sessions[0]
	}
	return renderCheckoutStateResultWithBrand(c, status, state, deps.AssetRegistry, checkoutResultBrandForSession(c.Context(), deps, session), session)
}

func renderCheckoutStateResultWithRegistry(c fiber.Ctx, status int, state checkoutPaymentState, registry *asset.Registry, sessions ...*models.PaymentSession) error {
	var session *models.PaymentSession
	if len(sessions) > 0 {
		session = sessions[0]
	}
	return renderCheckoutStateResultWithBrand(c, status, state, registry, checkoutResultBrand{}, session)
}

func renderCheckoutStateResultWithBrand(c fiber.Ctx, status int, state checkoutPaymentState, registry *asset.Registry, brand checkoutResultBrand, session *models.PaymentSession) error {
	lang := checkoutLanguage(c)
	title := state.TitleTR
	body := state.BodyTR
	if lang == "en" {
		title = state.TitleEN
		body = state.BodyEN
	}
	resultKind := "error"
	if state.Status == checkoutStateCanceled {
		resultKind = "canceled"
	}
	if state.Paid {
		resultKind = "success"
	}
	return c.Status(status).Render("gateway/payment_result", checkoutPaymentResultViewData(c, lang, registry, brand, session, title, body, state.Status, resultKind))
}

func checkoutPaymentResultViewData(c fiber.Ctx, lang string, registry *asset.Registry, brand checkoutResultBrand, session *models.PaymentSession, title string, message string, status string, resultKind string) fiber.Map {
	data := fiber.Map{
		"Lang":        lang,
		"Title":       title,
		"Message":     message,
		"Status":      status,
		"StatusLabel": checkoutResultStatusLabel(status, resultKind, lang),
		"ResultKind":  resultKind,
		"IsEnglish":   lang == "en",
		"LangTRURL":   checkoutCurrentPathLanguageURL(c, "tr"),
		"LangENURL":   checkoutCurrentPathLanguageURL(c, "en"),
	}
	if session == nil {
		data["CheckoutURL"] = "/"
		return data
	}
	if brand.Name != "" {
		data["ProductName"] = brand.Name
	}
	if brand.LogoURL != "" {
		data["ProductLogoURL"] = brand.LogoURL
	}
	data["Session"] = session
	data["CheckoutURL"] = checkoutLocalizedURL(session.SessionToken, "", lang, "")
	data["ReceiptRefs"] = checkoutReceiptRefs(session)
	data["OrderID"] = strings.TrimSpace(session.OrderID)
	data["Amount"] = strings.TrimSpace(session.Amount)
	data["Currency"] = strings.TrimSpace(session.Currency)
	data["AmountDisplay"] = checkoutResultAmountDisplay(session)
	data["IsDonation"] = models.IsDonationLinkType(session.LinkType)
	if selected := strings.TrimSpace(session.SelectedSymbol); selected != "" {
		selected = strings.ToUpper(selected)
		data["SelectedSymbol"] = selected
		data["SelectedAssetLogoURL"] = checkoutResultAssetLogoURL(registry, selected)
	}
	if session.SelectedChainID != nil {
		data["ChainName"] = chainLabel(*session.SelectedChainID)
		data["ChainLogoURL"] = asset.ChainLogoURL(*session.SelectedChainID)
	}
	return data
}

func checkoutResultStatusLabel(status string, resultKind string, lang string) string {
	isEnglish := normalizeLanguage(lang) == "en"
	switch strings.TrimSpace(status) {
	case models.PaymentStatusPaid:
		if isEnglish {
			return "Completed"
		}
		return "Tamamlandı"
	case models.PaymentStatusCanceled:
		if isEnglish {
			return "Canceled"
		}
		return "İptal edildi"
	case models.PaymentStatusExpired:
		if isEnglish {
			return "Expired"
		}
		return "Süresi doldu"
	case models.PaymentStatusFailed:
		if isEnglish {
			return "Failed"
		}
		return "Başarısız"
	case models.PaymentStatusUnderpaid:
		if isEnglish {
			return "Underpaid"
		}
		return "Eksik ödeme"
	case models.PaymentStatusOverpaid:
		if isEnglish {
			return "Overpaid"
		}
		return "Fazla ödeme"
	case models.PaymentStatusPartialPaid:
		if isEnglish {
			return "Partial payment"
		}
		return "Kısmi ödeme"
	}
	switch strings.TrimSpace(resultKind) {
	case "success":
		if isEnglish {
			return "Completed"
		}
		return "Tamamlandı"
	case "canceled":
		if isEnglish {
			return "Canceled"
		}
		return "İptal edildi"
	default:
		if isEnglish {
			return "Action required"
		}
		return "İşlem tamamlanamadı"
	}
}

func checkoutResultBrandForSession(ctx context.Context, deps PaymentHandlerDeps, session *models.PaymentSession) checkoutResultBrand {
	if session == nil {
		return checkoutResultBrand{}
	}
	name, _, logoURL := lookupCheckoutProduct(ctx, deps, session.ProductID)
	return checkoutResultBrand{
		Name:    strings.TrimSpace(name),
		LogoURL: strings.TrimSpace(logoURL),
	}
}

func checkoutResultAmountDisplay(session *models.PaymentSession) string {
	if session == nil {
		return ""
	}
	switch session.Status {
	case models.PaymentStatusPaid,
		models.PaymentStatusExpired,
		models.PaymentStatusUnderpaid,
		models.PaymentStatusOverpaid,
		models.PaymentStatusPartialPaid:
		if positiveTokenAmountRaw(session.MatchedAmountRaw) && strings.TrimSpace(session.SelectedSymbol) != "" {
			return formatPaymentAmount(session.MatchedAmountRaw, session.SelectedDecimals, session.SelectedSymbol)
		}
	}
	// Expected amounts describe the checkout request, not money that was
	// received. They are safe to present as paid only after the session itself
	// reached the paid state. Failed, canceled, expired, and exception outcomes
	// without a matched amount must not claim that the requested amount was paid.
	if session.Status != models.PaymentStatusPaid {
		return ""
	}
	if positiveTokenAmountRaw(session.ExpectedAmountRaw) && strings.TrimSpace(session.SelectedSymbol) != "" {
		return formatPaymentAmount(session.ExpectedAmountRaw, session.SelectedDecimals, session.SelectedSymbol)
	}
	amount := strings.TrimSpace(session.Amount)
	if amount == "" {
		return ""
	}
	currency := strings.ToUpper(strings.TrimSpace(session.Currency))
	if currency == "" {
		return amount
	}
	return amount + " " + currency
}

func checkoutResultAssetLogoURL(registry *asset.Registry, symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ""
	}
	if registry != nil {
		if logoURL := strings.TrimSpace(registry.LogoURL(symbol)); logoURL != "" {
			return logoURL
		}
	}
	return asset.CoinLogoURLFromSlug(symbol)
}

func checkoutCurrentPathLanguageURL(c fiber.Ctx, lang string) string {
	path := strings.TrimSpace(c.Path())
	if path == "" {
		path = "/"
	}
	values := url.Values{}
	values.Set("lang", normalizeLanguage(lang))
	return path + "?" + values.Encode()
}

func checkoutReceiptRefs(session *models.PaymentSession) []fiber.Map {
	if session == nil {
		return nil
	}
	refs := []fiber.Map{
		{"ID": "receiptPaymentID", "Label": "Payment ID", "Value": session.ID.String()},
		{"ID": "receiptSessionToken", "Label": "Track ID", "Value": strings.TrimSpace(session.SessionToken)},
	}
	if orderID := strings.TrimSpace(session.OrderID); orderID != "" {
		refs = append(refs, fiber.Map{"ID": "receiptOrderID", "Label": "Order ID", "Value": orderID})
	}
	if session.TxHash != nil {
		if txHash := strings.TrimSpace(*session.TxHash); txHash != "" {
			refs = append(refs, fiber.Map{"ID": "receiptTxHash", "Label": "Transaction", "Value": txHash})
		}
	}
	return refs
}

func checkoutLanguage(c fiber.Ctx) string {
	lang := normalizeLanguage(c.Query("lang"))
	if strings.TrimSpace(c.Query("lang")) == "" {
		lang = normalizeLanguage(c.Cookies("gateway_lang"))
	}
	c.Cookie(&fiber.Cookie{
		Name:     "gateway_lang",
		Value:    lang,
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   60 * 60 * 24 * 365,
	})
	return lang
}

func checkoutLocalizedURL(sessionToken string, suffix string, lang string, selectedSymbol string) string {
	path := "/checkout/" + strings.TrimSpace(sessionToken) + suffix
	values := url.Values{}
	if selectedSymbol = strings.ToUpper(strings.TrimSpace(selectedSymbol)); selectedSymbol != "" && suffix == "" {
		values.Set("asset", selectedSymbol)
	}
	if lang = normalizeLanguage(lang); lang != "" {
		values.Set("lang", lang)
	}
	encoded := values.Encode()
	if encoded == "" {
		return path
	}
	return path + "?" + encoded
}

func markPaymentCanceledOrExpired(ctx context.Context, deps PaymentHandlerDeps, session *models.PaymentSession, status string) error {
	if session == nil || paymentSessionTerminal(*session) {
		return nil
	}
	if deps.PaymentRepo == nil {
		err := errors.New("payment repository is not configured")
		log.Printf("checkout status update payment_id=%s error=%v", session.ID, err)
		return err
	}
	if status == models.PaymentStatusExpired {
		updatedSession, changed, err := deps.PaymentRepo.Expire(ctx, session.SessionToken)
		if err != nil {
			log.Printf("checkout expiration payment_id=%s error=%v", session.ID, err)
			return err
		}
		if updatedSession != nil {
			*session = *updatedSession
		}
		if changed {
			deliverPaymentWebhook(ctx, deps, session)
		}
		return nil
	}
	_, _, err := deps.PaymentRepo.Cancel(ctx, session.SessionToken)
	return err
}

func deliverPaymentWebhook(ctx context.Context, deps PaymentHandlerDeps, session *models.PaymentSession) {
	if session == nil || session.WebhookEvent == "" || session.WebhookSentAt != nil {
		return
	}
	if deps.MoneyEventOutboxRepo != nil {
		ownedByOutbox, err := deps.MoneyEventOutboxRepo.HasAggregateEvent(ctx, "payment", session.ID.String(), session.WebhookEvent)
		if err != nil {
			log.Printf("payment canonical outbox lookup payment_id=%s error=%v", session.ID, err)
			return
		}
		if ownedByOutbox {
			return
		}
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if deps.WebhookDeliveryRepo != nil {
		if _, _, err := deps.WebhookDeliveryRepo.EnqueuePayment(deliveryCtx, session.Domain, *session); err != nil {
			log.Printf("payment webhook enqueue payment_id=%s error=%v", session.ID, err)
			if deps.PaymentRepo != nil {
				if markErr := deps.PaymentRepo.MarkWebhookAttempt(ctx, session.ID, false, err); markErr != nil {
					log.Printf("payment webhook attempt update payment_id=%s error=%v", session.ID, markErr)
				}
			}
		}
	}
}

func paymentURI(session models.PaymentSession) string {
	if session.DepositAddress == "" || session.SelectedChainID == nil {
		return ""
	}
	if session.SelectedToken != nil && *session.SelectedToken != "" {
		return session.DepositAddress
	}
	if models.IsDonationLinkType(session.LinkType) || strings.TrimSpace(session.ExpectedAmountRaw) == "" {
		switch *session.SelectedChainID {
		case constants.Bitcoin:
			return "bitcoin:" + session.DepositAddress
		case constants.Solana:
			return "solana:" + session.DepositAddress
		case constants.TRON, constants.TRONTestnet:
			return session.DepositAddress
		default:
			return fmt.Sprintf("ethereum:%s@%d", session.DepositAddress, *session.SelectedChainID)
		}
	}
	switch *session.SelectedChainID {
	case constants.Bitcoin:
		return "bitcoin:" + session.DepositAddress + "?amount=" + url.QueryEscape(formatRawDecimal(session.ExpectedAmountRaw, session.SelectedDecimals))
	case constants.Solana:
		return "solana:" + session.DepositAddress + "?amount=" + url.QueryEscape(formatRawDecimal(session.ExpectedAmountRaw, session.SelectedDecimals))
	case constants.TRON, constants.TRONTestnet:
		return session.DepositAddress
	default:
		return fmt.Sprintf("ethereum:%s@%d?value=%s", session.DepositAddress, *session.SelectedChainID, session.ExpectedAmountRaw)
	}
}

func isSessionExpired(session *models.PaymentSession) bool {
	return session != nil && !models.IsDonationLinkType(session.LinkType) && session.ExpiresAt != nil && time.Now().After(*session.ExpiresAt)
}

func checkoutExpiresAtUnix(session *models.PaymentSession) int64 {
	if session == nil || models.IsDonationLinkType(session.LinkType) || session.ExpiresAt == nil {
		return 0
	}
	return session.ExpiresAt.UnixMilli()
}

func paymentSessionTTL() time.Duration {
	return 30 * time.Minute
}

func baseURL(c fiber.Ctx) string {
	if configured := configuredPublicBaseURL(); configured != "" {
		return configured
	}
	return requestScheme(c) + "://" + sanitizedRequestHost(c.Host())
}

func configuredPublicBaseURL() string {
	for _, key := range []string{"PUBLIC_BASE_URL", "GATEWAY_BASE_URL", "APP_BASE_URL"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			continue
		}
		if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
			continue
		}
		return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/")
	}
	return ""
}

func requestScheme(c fiber.Ctx) string {
	proto := strings.ToLower(strings.TrimSpace(c.Get("X-Forwarded-Proto")))
	if proto == "http" || proto == "https" {
		return proto
	}
	if strings.EqualFold(c.Protocol(), "https") {
		return "https"
	}
	return "http"
}

func sanitizedRequestHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "\r\n\t /\\") {
		return "localhost"
	}
	parsed, err := url.Parse("//" + host)
	if err != nil || parsed.Host == "" || parsed.Host != host || parsed.User != nil {
		return "localhost"
	}
	return parsed.Host
}

func valueOrDefault(value *string, fallback string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fallback
	}
	return strings.TrimSpace(*value)
}

func uuidString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func isRecordNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

type InvoicePageData struct {
	Title                string
	OrderID              string
	MerchantName         string
	DomainURL            string
	ProductName          string
	ProductDescription   string
	ProductLogoURL       string
	Amount               string
	Currency             string
	AmountDisplay        string
	Status               string
	StatusLabel          string
	StatusClass          string
	IsPaid               bool
	Symbol               string
	SelectedAssetLogoURL string
	ChainName            string
	TxHash               string
	CreatedAt            string
	PaidAt               string
	CheckoutURL          string
}

// HandlePaymentInvoice renders a print-friendly invoice/receipt for a payment session.
func HandlePaymentInvoice(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		token := c.Params("token")
		session, err := deps.PaymentRepo.FindByToken(c.Context(), token)
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("Ödeme bulunamadı")
		}
		if !paymentSessionTerminal(*session) && session.SelectedChainID != nil {
			available, reason, availabilityErr := paymentNetworkDepositAvailability(c.Context(), deps.NetworkOperationalStateRepo, *session.SelectedChainID)
			if availabilityErr != nil {
				return renderPaymentError(c, fiber.StatusServiceUnavailable, checkoutNetworkAvailabilityError)
			}
			if !available {
				return renderPaymentError(c, fiber.StatusServiceUnavailable, reason)
			}
		}

		isPaid := session.Status == models.PaymentStatusPaid
		productName, productDescription, productLogoURL := lookupCheckoutProduct(c.Context(), deps, session.ProductID)
		if productName == "" {
			productName = "Gateway Pay"
		}
		statusClass := invoiceStatusClass(session.Status)
		data := InvoicePageData{
			Title:              "Fatura #" + session.OrderID,
			OrderID:            session.OrderID,
			DomainURL:          session.Domain.DomainURL,
			ProductName:        productName,
			ProductDescription: productDescription,
			ProductLogoURL:     productLogoURL,
			Amount:             session.Amount,
			Currency:           strings.ToUpper(strings.TrimSpace(session.Currency)),
			AmountDisplay:      invoiceAmountDisplay(session),
			Status:             session.Status,
			StatusLabel:        invoiceStatusLabel(session.Status),
			StatusClass:        statusClass,
			IsPaid:             isPaid,
			Symbol:             strings.ToUpper(strings.TrimSpace(session.SelectedSymbol)),
			CreatedAt:          session.CreatedAt.Format("02.01.2006 15:04"),
			CheckoutURL:        "/checkout/" + session.SessionToken,
		}
		if data.AmountDisplay == " " {
			data.AmountDisplay = ""
		}
		if data.Symbol != "" {
			data.SelectedAssetLogoURL = checkoutResultAssetLogoURL(deps.AssetRegistry, data.Symbol)
		}
		if session.SelectedChainID != nil {
			data.ChainName = chainLabel(*session.SelectedChainID)
		}
		if session.TxHash != nil {
			data.TxHash = *session.TxHash
		}
		if session.PaidAt != nil {
			data.PaidAt = session.PaidAt.Format("02.01.2006 15:04")
		}

		return c.Render("invoice", data)
	}
}

func invoiceStatusLabel(status string) string {
	switch strings.TrimSpace(status) {
	case models.PaymentStatusPaid:
		return "Tamamlandı"
	case models.PaymentStatusCanceled:
		return "İptal edildi"
	case models.PaymentStatusExpired:
		return "Süresi doldu"
	case models.PaymentStatusFailed:
		return "Başarısız"
	case models.PaymentStatusUnderpaid:
		return "Eksik ödeme"
	case models.PaymentStatusOverpaid:
		return "Fazla ödeme"
	case models.PaymentStatusPartialPaid:
		return "Kısmi ödeme"
	default:
		return "Ödeme bekleniyor"
	}
}

func invoiceAmountDisplay(session *models.PaymentSession) string {
	if session == nil {
		return ""
	}
	amount := strings.TrimSpace(session.Amount)
	currency := strings.ToUpper(strings.TrimSpace(session.Currency))
	if amount == "" {
		return currency
	}
	if currency == "" {
		return amount
	}
	return amount + " " + currency
}

func invoiceStatusClass(status string) string {
	switch strings.TrimSpace(status) {
	case models.PaymentStatusPaid:
		return "paid"
	case models.PaymentStatusCanceled, models.PaymentStatusExpired:
		return "canceled"
	case models.PaymentStatusFailed, models.PaymentStatusUnderpaid, models.PaymentStatusOverpaid:
		return "failed"
	default:
		return "pending"
	}
}
