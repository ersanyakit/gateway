package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"core/asset"
	"core/constants"
	"core/models"
	"core/repositories"
	webhooksvc "core/services/webhook"
	"core/types"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
	"gorm.io/gorm"
)

type PaymentHandlerDeps struct {
	DomainRepo    *repositories.DomainRepo
	WalletRepo    *repositories.WalletRepo
	PaymentRepo   *repositories.PaymentRepo
	AssetRegistry *asset.Registry
	Notifier      *webhooksvc.Notifier
}

type CheckoutAssetOption struct {
	ChainID       int64
	ChainName     string
	Symbol        string
	Name          string
	Token         string
	Decimals      uint8
	AmountRaw     string
	AmountDisplay string
	Native        bool
	Available     bool
}

// HandlePaymentCreate creates a merchant payment checkout session.
// @Summary Create payment session
// @Description Creates a checkout session for a merchant order and returns a hosted checkout URL.
// @Tags Payments
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param payload body types.PaymentCreateParams true "Payment create payload"
// @Success 201 {object} types.PaymentCreateResponse
// @Failure 400 {object} types.ErrorResponse
// @Failure 401 {object} types.ErrorResponse
// @Failure 500 {object} types.ErrorResponse
// @Router /payments/create [post]
func HandlePaymentCreate(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		var params types.PaymentCreateParams
		if err := c.Bind().Body(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid JSON body: " + err.Error(),
			})
		}
		params.Context = c.Context()
		if err := params.Validate(); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   err.Error(),
			})
		}

		domain, err := resolvePaymentDomain(c, deps.DomainRepo, params)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false,
				"error":   err.Error(),
			})
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
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"success": false,
				"error":   "wallet create failed: " + err.Error(),
			})
		}

		expiresAt := time.Now().Add(paymentSessionTTL())
		session := &models.PaymentSession{
			MerchantID: domain.MerchantID,
			DomainID:   domain.ID,
			WalletID:   wallet.ID,
			OrderID:    *params.OrderID,
			ProductID:  productID,
			UserID:     userID,
			Amount:     *params.Amount,
			Currency:   *params.Currency,
			SuccessURL: valueOrDefault(params.SuccessURL, ""),
			CancelURL:  valueOrDefault(params.CancelURL, ""),
			Status:     models.PaymentStatusPending,
			ExpiresAt:  &expiresAt,
		}
		if err := deps.PaymentRepo.Create(params.Context, session); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"success": false,
				"error":   "payment session create failed: " + err.Error(),
			})
		}

		checkoutURL := baseURL(c) + "/checkout/" + session.SessionToken
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"success":        true,
			"payment_id":     session.ID,
			"session_token":  session.SessionToken,
			"checkout_url":   checkoutURL,
			"status":         session.Status,
			"expires_at":     session.ExpiresAt,
			"deposit_wallet": wallet.ID,
		})
	}
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
		if session.Status == models.PaymentStatusAwaitingPayment || session.Status == models.PaymentStatusPaid {
			return c.Redirect().To("/checkout/" + session.SessionToken + "/pay")
		}
		if isSessionExpired(session) {
			_ = markPaymentCanceledOrExpired(c.Context(), deps, session, models.PaymentStatusExpired)
			return renderPaymentError(c, fiber.StatusGone, "This payment session has expired.")
		}

		options := checkoutAssetOptions(deps.AssetRegistry, *session)
		return c.Render("gateway/checkout", fiber.Map{
			"Session": session,
			"Assets":  options,
			"Error":   "",
		})
	}
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
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return renderPaymentError(c, fiber.StatusNotFound, "Payment session was not found.")
		}
		if isSessionExpired(session) {
			_ = markPaymentCanceledOrExpired(c.Context(), deps, session, models.PaymentStatusExpired)
			return renderPaymentError(c, fiber.StatusGone, "This payment session has expired.")
		}
		if session.Status == models.PaymentStatusPaid {
			return c.Redirect().To("/checkout/" + session.SessionToken + "/return/success")
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

		amountRaw, err := types.DecimalToRaw(session.Amount, assetInfo.GetDecimals())
		if err != nil {
			return renderCheckoutWithError(c, deps, session, err.Error())
		}
		depositAddress := walletAddressForChain(session.Wallet, assetInfo.GetChainID())
		if depositAddress == "" {
			return renderCheckoutWithError(c, deps, session, "Deposit address is not available for this network.")
		}

		var selectedToken *string
		if !assetInfo.IsNative() {
			identifier := assetInfo.GetIdentifier()
			selectedToken = &identifier
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
		)
		if err != nil {
			return renderCheckoutWithError(c, deps, session, "Could not select this asset.")
		}

		return c.Redirect().To("/checkout/" + updatedSession.SessionToken + "/pay")
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
		if isSessionExpired(session) && session.Status != models.PaymentStatusPaid {
			_ = markPaymentCanceledOrExpired(c.Context(), deps, session, models.PaymentStatusExpired)
			return renderPaymentError(c, fiber.StatusGone, "This payment session has expired.")
		}
		if session.Status == models.PaymentStatusPending {
			return c.Redirect().To("/checkout/" + session.SessionToken)
		}

		qrURL := "/checkout/" + session.SessionToken + "/qr.png"
		return c.Render("gateway/pay", fiber.Map{
			"Session":       session,
			"QRCodeURL":     qrURL,
			"PaymentURI":    paymentURI(*session),
			"ChainName":     constants.ChainName(*session.SelectedChainID),
			"AmountDisplay": session.Amount + " " + session.SelectedSymbol,
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
		if isSessionExpired(session) && session.Status != models.PaymentStatusPaid {
			_ = markPaymentCanceledOrExpired(c.Context(), deps, session, models.PaymentStatusExpired)
			session.Status = models.PaymentStatusExpired
		}
		return c.JSON(fiber.Map{
			"success":      true,
			"status":       session.Status,
			"paid":         session.Status == models.PaymentStatusPaid,
			"success_path": "/checkout/" + session.SessionToken + "/return/success",
			"cancel_path":  "/checkout/" + session.SessionToken + "/cancel",
		})
	}
}

// HandleCheckoutCancel cancels a checkout session.
// @Summary Cancel checkout
// @Description Cancels the hosted checkout session, sends a payment_failed webhook when applicable, and redirects to the merchant cancel URL.
// @Tags Payments
// @Produce html
// @Param token path string true "Payment session token"
// @Success 302 {string} string "Redirect to merchant cancel URL or rendered result page"
// @Failure 404 {string} string "HTML error page"
// @Router /checkout/{token}/cancel [get]
func HandleCheckoutCancel(deps PaymentHandlerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		session, err := deps.PaymentRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return renderPaymentError(c, fiber.StatusNotFound, "Payment session was not found.")
		}
		if session.Status != models.PaymentStatusPaid {
			session, _, err = deps.PaymentRepo.Cancel(c.Context(), session.SessionToken)
			if err == nil {
				deliverPaymentWebhook(c.Context(), deps, session)
			}
		}
		if session.CancelURL != "" {
			return c.Redirect().To(session.CancelURL)
		}
		return c.Render("gateway/payment_result", fiber.Map{
			"Title":   "Payment canceled",
			"Message": "The checkout session was canceled.",
			"Status":  session.Status,
		})
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
		if session.Status != models.PaymentStatusPaid {
			return c.Redirect().To("/checkout/" + session.SessionToken + "/pay")
		}
		if session.SuccessURL != "" {
			return c.Redirect().To(session.SuccessURL)
		}
		return c.Render("gateway/payment_result", fiber.Map{
			"Title":   "Payment complete",
			"Message": "Payment received successfully.",
			"Status":  session.Status,
		})
	}
}

func resolvePaymentDomain(c fiber.Ctx, repo *repositories.DomainRepo, params types.PaymentCreateParams) (*models.Domain, error) {
	apiKey := strings.TrimSpace(c.Get("X-API-Key"))
	if apiKey == "" {
		auth := strings.TrimSpace(c.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			apiKey = strings.TrimSpace(auth[7:])
		}
	}
	if apiKey != "" {
		return repo.FindByAPIKey(types.DomainParams{
			Context: params.Context,
			APIKey:  &apiKey,
		})
	}
	if params.DomainID != nil {
		domain, err := repo.FindByID(types.DomainParams{
			Context:  params.Context,
			DomainID: params.DomainID,
		})
		if err != nil {
			return nil, err
		}
		if params.MerchantID != nil && domain.MerchantID.String() != *params.MerchantID {
			return nil, errors.New("domain does not belong to merchant")
		}
		return domain, nil
	}
	return nil, errors.New("X-API-Key or DomainID is required")
}

func checkoutAssetOptions(registry *asset.Registry, session models.PaymentSession) []CheckoutAssetOption {
	assets := registry.ListAll()
	options := make([]CheckoutAssetOption, 0, len(assets))
	for _, assetInfo := range assets {
		amountRaw, err := types.DecimalToRaw(session.Amount, assetInfo.GetDecimals())
		if err != nil {
			continue
		}
		token := ""
		if !assetInfo.IsNative() {
			token = assetInfo.GetIdentifier()
		}
		options = append(options, CheckoutAssetOption{
			ChainID:       int64(assetInfo.GetChainID()),
			ChainName:     constants.ChainName(assetInfo.GetChainID()),
			Symbol:        assetInfo.GetSymbol(),
			Name:          assetInfo.GetName(),
			Token:         token,
			Decimals:      assetInfo.GetDecimals(),
			AmountRaw:     amountRaw,
			AmountDisplay: session.Amount + " " + assetInfo.GetSymbol(),
			Native:        assetInfo.IsNative(),
			Available:     walletAddressForChain(session.Wallet, assetInfo.GetChainID()) != "",
		})
	}
	return options
}

func findCheckoutAsset(registry *asset.Registry, chainID constants.ChainID, symbol string, token string) (asset.Asset, error) {
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

func renderCheckoutWithError(c fiber.Ctx, deps PaymentHandlerDeps, session *models.PaymentSession, message string) error {
	return c.Status(fiber.StatusBadRequest).Render("gateway/checkout", fiber.Map{
		"Session": session,
		"Assets":  checkoutAssetOptions(deps.AssetRegistry, *session),
		"Error":   message,
	})
}

func renderPaymentError(c fiber.Ctx, status int, message string) error {
	return c.Status(status).Render("gateway/payment_result", fiber.Map{
		"Title":   "Payment unavailable",
		"Message": message,
		"Status":  "error",
	})
}

func markPaymentCanceledOrExpired(ctx context.Context, deps PaymentHandlerDeps, session *models.PaymentSession, status string) error {
	if session == nil || session.Status == models.PaymentStatusPaid {
		return nil
	}
	if status == models.PaymentStatusExpired {
		session.Status = models.PaymentStatusExpired
		session.WebhookEvent = "payment_failed"
		session.UpdatedAt = time.Now()
		if err := deps.PaymentRepo.DB().WithContext(ctx).Save(session).Error; err != nil {
			return err
		}
		deliverPaymentWebhook(ctx, deps, session)
		return nil
	}
	_, _, err := deps.PaymentRepo.Cancel(ctx, session.SessionToken)
	return err
}

func deliverPaymentWebhook(ctx context.Context, deps PaymentHandlerDeps, session *models.PaymentSession) {
	if session == nil || session.WebhookEvent == "" || session.WebhookSentAt != nil {
		return
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := deps.Notifier.DeliverPayment(deliveryCtx, session.Domain, *session)
	if err != nil {
		_ = deps.PaymentRepo.MarkWebhookAttempt(ctx, session.ID, false, err)
		return
	}
	_ = deps.PaymentRepo.MarkWebhookAttempt(ctx, session.ID, true, nil)
}

func paymentURI(session models.PaymentSession) string {
	if session.DepositAddress == "" || session.SelectedChainID == nil {
		return ""
	}
	if session.SelectedToken != nil && *session.SelectedToken != "" {
		return session.DepositAddress
	}
	switch *session.SelectedChainID {
	case constants.Bitcoin:
		return "bitcoin:" + session.DepositAddress + "?amount=" + url.QueryEscape(session.Amount)
	case constants.Solana:
		return "solana:" + session.DepositAddress + "?amount=" + url.QueryEscape(session.Amount)
	case constants.TRON:
		return session.DepositAddress
	default:
		return fmt.Sprintf("ethereum:%s@%d?value=%s", session.DepositAddress, *session.SelectedChainID, session.ExpectedAmountRaw)
	}
}

func isSessionExpired(session *models.PaymentSession) bool {
	return session != nil && session.ExpiresAt != nil && time.Now().After(*session.ExpiresAt)
}

func paymentSessionTTL() time.Duration {
	return 30 * time.Minute
}

func baseURL(c fiber.Ctx) string {
	proto := c.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
		if strings.EqualFold(c.Protocol(), "https") {
			proto = "https"
		}
	}
	return proto + "://" + c.Host()
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
