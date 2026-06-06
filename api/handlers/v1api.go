package handlers

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"core/asset"
	"core/constants"
	"core/models"
	"core/repositories"
	"core/services/pricing"
	"core/services/realtime"
	"core/services/txrescan"
	webhooksvc "core/services/webhook"
	"core/types"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// V1APIDeps holds all dependencies used by the v1 REST API endpoints.
type V1APIDeps struct {
	DomainRepo      *repositories.DomainRepo
	WalletRepo      *repositories.WalletRepo
	PaymentRepo     *repositories.PaymentRepo
	WithdrawalRepo  *repositories.WithdrawalRequestRepo
	RefundRepo      *repositories.RefundRepo
	LedgerRepo      *repositories.LedgerRepo
	TransactionRepo *repositories.TransactionRepo
	AssetRegistry   *asset.Registry
	PriceOracle     pricing.PriceOracle
	Notifier        *webhooksvc.Notifier
	PaymentHub      *realtime.PaymentHub
	IdempotencyRepo *repositories.IdempotencyRepo
	TxRescanService func() *txrescan.Service
}

// ────────────────────────────────────────────────────────────────────────────
// Auth helper
// ────────────────────────────────────────────────────────────────────────────

func v1ResolveDomain(c fiber.Ctx, domainRepo *repositories.DomainRepo) (*models.Domain, error) {
	key := strings.TrimSpace(c.Get("X-API-Key"))
	if key == "" {
		auth := strings.TrimSpace(c.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			key = strings.TrimSpace(auth[7:])
		}
	}
	if key == "" {
		return nil, fmt.Errorf("X-API-Key header or Authorization: Bearer <key> is required")
	}
	return domainRepo.FindByAPIKey(types.DomainParams{Context: c.Context(), APIKey: &key})
}

func v1OK(c fiber.Ctx, data interface{}) error {
	return c.JSON(fiber.Map{"result": "ok", "data": data})
}

func v1Err(c fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(fiber.Map{"result": "error", "message": msg})
}

func v1QueryInt(c fiber.Ctx, key string, def int) int {
	v := strings.TrimSpace(c.Query(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// ────────────────────────────────────────────────────────────────────────────
// Common API
// ────────────────────────────────────────────────────────────────────────────

// HandleV1CommonStatus godoc
// @Summary System status
// @Description Returns current operational status of the gateway.
// @Tags Common
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} types.V1StatusResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/common/status [get]
func HandleV1CommonStatus(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, err := v1ResolveDomain(c, deps.DomainRepo); err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		return v1OK(c, fiber.Map{
			"status":    "operational",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// HandleV1CommonBalance godoc
// @Summary Account balance
// @Description Returns the ledger balance for each asset held by the authenticated merchant.
// @Tags Common
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} types.V1BalanceResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/common/balance [get]
func HandleV1CommonBalance(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		type balanceItem struct {
			Symbol   string `json:"symbol"`
			Chain    string `json:"chain"`
			ChainID  int64  `json:"chain_id"`
			Balance  string `json:"balance"`
			Decimals uint8  `json:"decimals"`
			LogoURL  string `json:"logo_url,omitempty"`
		}

		var items []balanceItem
		if deps.LedgerRepo != nil {
			rows, err := deps.LedgerRepo.MerchantBalances(c.Context(), domain.MerchantID)
			if err == nil {
				for _, row := range rows {
					items = append(items, balanceItem{
						Symbol:   row.Symbol,
						Chain:    chainLabel(constants.ChainID(row.ChainID)),
						ChainID:  row.ChainID,
						Balance:  formatV1Amount(row.BalanceRaw, row.Decimals),
						Decimals: row.Decimals,
						LogoURL:  asset.CoinLogoURL(row.Symbol),
					})
				}
			}
		}
		if items == nil {
			items = []balanceItem{}
		}
		return v1OK(c, fiber.Map{"balances": items})
	}
}

// HandleV1CommonPrices godoc
// @Summary Asset prices
// @Description Returns current prices for all supported crypto assets in the requested fiat currency.
// @Tags Common
// @Produce json
// @Security ApiKeyAuth
// @Param currency query string false "Fiat currency code" Enums(USD,EUR,TRY,GBP) default(USD)
// @Success 200 {object} types.V1PricesResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/common/prices [get]
func HandleV1CommonPrices(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, err := v1ResolveDomain(c, deps.DomainRepo); err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		currency := strings.ToUpper(strings.TrimSpace(c.Query("currency")))
		if currency == "" {
			currency = "USD"
		}

		type priceItem struct {
			Symbol   string `json:"symbol"`
			Name     string `json:"name"`
			Price    string `json:"price"`
			Currency string `json:"currency"`
			LogoURL  string `json:"logo_url,omitempty"`
		}

		seen := make(map[string]struct{})
		var items []priceItem
		if deps.AssetRegistry != nil && deps.PriceOracle != nil {
			for _, a := range deps.AssetRegistry.ListAll() {
				sym := a.GetSymbol()
				canonical := deps.AssetRegistry.CanonicalSymbol(sym)
				if _, ok := seen[canonical]; ok {
					continue
				}
				seen[canonical] = struct{}{}
				price, err := deps.PriceOracle.Price(c.Context(), canonical, currency)
				priceStr := "0"
				if err == nil && price != nil {
					f, _ := price.Float64()
					priceStr = strconv.FormatFloat(f, 'f', 8, 64)
				}
				items = append(items, priceItem{
					Symbol:   canonical,
					Name:     a.GetName(),
					Price:    priceStr,
					Currency: currency,
					LogoURL:  asset.CoinLogoURL(canonical),
				})
			}
		}
		return v1OK(c, fiber.Map{"prices": items, "currency": currency})
	}
}

// HandleV1CommonCurrencies godoc
// @Summary Supported crypto currencies
// @Description Lists all supported crypto assets with chain, decimals, and logo URLs.
// @Tags Common
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} types.V1CurrenciesResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/common/currencies [get]
func HandleV1CommonCurrencies(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, err := v1ResolveDomain(c, deps.DomainRepo); err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		type currencyItem struct {
			Symbol       string `json:"symbol"`
			Name         string `json:"name"`
			Network      string `json:"network"`
			ChainID      int64  `json:"chain_id"`
			Decimals     uint8  `json:"decimals"`
			LogoURL      string `json:"logo_url,omitempty"`
			ChainLogoURL string `json:"chain_logo_url,omitempty"`
		}

		var items []currencyItem
		if deps.AssetRegistry != nil {
			for _, a := range deps.AssetRegistry.ListAll() {
				items = append(items, currencyItem{
					Symbol:       a.GetSymbol(),
					Name:         a.GetName(),
					Network:      chainLabel(a.GetChainID()),
					ChainID:      int64(a.GetChainID()),
					Decimals:     a.GetDecimals(),
					LogoURL:      deps.AssetRegistry.LogoURL(a.GetSymbol()),
					ChainLogoURL: asset.ChainLogoURL(a.GetChainID()),
				})
			}
		}
		return v1OK(c, fiber.Map{"currencies": items})
	}
}

// HandleV1CommonFiatCurrencies godoc
// @Summary Supported fiat currencies
// @Description Lists all fiat currencies accepted as payment amount denomination.
// @Tags Common
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} types.V1FiatCurrenciesResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/common/fiat-currencies [get]
func HandleV1CommonFiatCurrencies(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, err := v1ResolveDomain(c, deps.DomainRepo); err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		fiats := []fiber.Map{
			{"symbol": "USD", "name": "US Dollar", "sign": "$"},
			{"symbol": "EUR", "name": "Euro", "sign": "€"},
			{"symbol": "TRY", "name": "Turkish Lira", "sign": "₺"},
			{"symbol": "GBP", "name": "British Pound", "sign": "£"},
			{"symbol": "JPY", "name": "Japanese Yen", "sign": "¥"},
			{"symbol": "CAD", "name": "Canadian Dollar", "sign": "CA$"},
			{"symbol": "AUD", "name": "Australian Dollar", "sign": "A$"},
			{"symbol": "CHF", "name": "Swiss Franc", "sign": "Fr"},
			{"symbol": "SGD", "name": "Singapore Dollar", "sign": "S$"},
			{"symbol": "AED", "name": "UAE Dirham", "sign": "د.إ"},
			{"symbol": "SAR", "name": "Saudi Riyal", "sign": "﷼"},
			{"symbol": "INR", "name": "Indian Rupee", "sign": "₹"},
			{"symbol": "BRL", "name": "Brazilian Real", "sign": "R$"},
			{"symbol": "RUB", "name": "Russian Ruble", "sign": "₽"},
		}
		return v1OK(c, fiber.Map{"fiat_currencies": fiats})
	}
}

// HandleV1CommonNetworks godoc
// @Summary Supported blockchain networks
// @Description Lists all supported blockchain networks with chain IDs and logo URLs.
// @Tags Common
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} types.V1NetworksResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/common/networks [get]
func HandleV1CommonNetworks(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, err := v1ResolveDomain(c, deps.DomainRepo); err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		allChains := []constants.ChainID{
			constants.Bitcoin,
			constants.Ethereum,
			constants.Binance,
			constants.Avalanche,
			constants.Base,
			constants.Arbitrum,
			constants.Unichain,
			constants.Solana,
			constants.TRON,
			constants.Chiliz,
			constants.ChilizSpicy,
		}

		type networkItem struct {
			ChainID int64  `json:"chain_id"`
			Name    string `json:"name"`
			LogoURL string `json:"logo_url,omitempty"`
			IsEVM   bool   `json:"is_evm"`
		}

		var networks []networkItem
		for _, id := range allChains {
			networks = append(networks, networkItem{
				ChainID: int64(id),
				Name:    chainLabel(id),
				LogoURL: asset.ChainLogoURL(id),
				IsEVM:   id != constants.Bitcoin && id != constants.Solana && id != constants.TRON,
			})
		}
		return v1OK(c, fiber.Map{"networks": networks})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Payment API
// ────────────────────────────────────────────────────────────────────────────

// HandleV1PaymentCreate godoc
// @Summary Generate invoice
// @Description Creates a hosted checkout payment session. Returns a checkout URL the customer visits to complete payment.
// @Tags Payment
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param payload body types.V1InvoiceRequest true "Invoice parameters"
// @Success 201 {object} types.V1PaymentCreateResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payment/create [post]
func HandleV1PaymentCreate(deps V1APIDeps) fiber.Handler {
	// Delegate to the existing payment create handler logic
	inner := HandlePaymentCreate(PaymentHandlerDeps{
		DomainRepo:      deps.DomainRepo,
		WalletRepo:      deps.WalletRepo,
		PaymentRepo:     deps.PaymentRepo,
		AssetRegistry:   deps.AssetRegistry,
		PriceOracle:     deps.PriceOracle,
		Notifier:        deps.Notifier,
		PaymentHub:      deps.PaymentHub,
		IdempotencyRepo: deps.IdempotencyRepo,
	})
	return inner
}

// HandleV1PaymentWhiteLabel godoc
// @Summary Generate white label payment
// @Description Creates a white label hosted checkout session. Identical to Generate Invoice but returns a branded URL.
// @Tags Payment
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param payload body types.V1InvoiceRequest true "Invoice parameters"
// @Success 201 {object} types.V1PaymentCreateResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payment/white-label [post]
func HandleV1PaymentWhiteLabel(deps V1APIDeps) fiber.Handler {
	return HandleV1PaymentCreate(deps)
}

// HandleV1PaymentStaticAddressCreate godoc
// @Summary Generate static address
// @Description Creates a permanent deposit wallet for a user. Subsequent calls with the same user_id and chain return the existing address.
// @Tags Payment
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param payload body types.V1StaticAddressRequest true "Static address parameters"
// @Success 200 {object} types.V1StaticAddressResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payment/static-address [post]
func HandleV1PaymentStaticAddressCreate(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		var body struct {
			UserID    string `json:"user_id"`
			ProductID string `json:"product_id"`
		}
		if err := c.Bind().Body(&body); err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid request body")
		}
		userID := strings.TrimSpace(body.UserID)
		if userID == "" {
			return v1Err(c, fiber.StatusBadRequest, "user_id is required")
		}
		productID := strings.TrimSpace(body.ProductID)

		merchantIDStr := domain.MerchantID.String()
		domainIDStr := domain.ID.String()

		wallet, err := deps.WalletRepo.Create(types.WalletParams{
			Context:    c.Context(),
			MerchantId: &merchantIDStr,
			DomainId:   &domainIDStr,
			ProductId:  &productID,
			UserId:     &userID,
		})
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "wallet creation failed: "+err.Error())
		}

		return v1OK(c, v1WalletResponse(wallet))
	}
}

// HandleV1PaymentStaticAddressList godoc
// @Summary Static address list
// @Description Lists all static deposit wallets for the merchant, optionally filtered by user_id.
// @Tags Payment
// @Produce json
// @Security ApiKeyAuth
// @Param user_id query string false "Filter by user ID" example(customer_42)
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page (max 100)" default(20)
// @Success 200 {object} types.V1StaticAddressListResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payment/static-addresses [get]
func HandleV1PaymentStaticAddressList(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		page := v1QueryInt(c, "page", 1)
		limit := v1QueryInt(c, "limit", 20)
		if limit > 100 {
			limit = 100
		}
		search := strings.TrimSpace(c.Query("user_id"))
		offset := (page - 1) * limit

		wallets, total, err := deps.WalletRepo.SearchByMerchantPage(c.Context(), domain.MerchantID, search, limit, offset)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "failed to list wallets")
		}

		var items []v1WalletItemType
		for _, w := range wallets {
			items = append(items, v1WalletItem(w))
		}
		if items == nil {
			items = []v1WalletItemType{}
		}
		return v1OK(c, fiber.Map{
			"wallets": items,
			"total":   total,
			"page":    page,
			"limit":   limit,
		})
	}
}

// HandleV1PaymentInfo godoc
// @Summary Payment information
// @Description Retrieves detailed payment session info by track_id (session token) or order_id.
// @Tags Payment
// @Produce json
// @Security ApiKeyAuth
// @Param track_id query string false "Session token (UUID)" example(550e8400-e29b-41d4-a716-446655440000)
// @Param order_id query string false "Merchant order ID" example(ORD-2024-001)
// @Success 200 {object} types.V1PaymentInfoResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Failure 404 {object} types.V1ErrorResponse
// @Router /api/v1/payment/info [get]
func HandleV1PaymentInfo(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		trackID := strings.TrimSpace(c.Query("track_id"))
		orderID := strings.TrimSpace(c.Query("order_id"))

		var session *models.PaymentSession
		if trackID != "" {
			session, err = deps.PaymentRepo.FindByToken(c.Context(), trackID)
		} else if orderID != "" {
			session, err = deps.PaymentRepo.FindByOrderID(c.Context(), domain.MerchantID, orderID)
		} else {
			return v1Err(c, fiber.StatusBadRequest, "track_id or order_id query param is required")
		}

		if err != nil {
			return v1Err(c, fiber.StatusNotFound, "payment not found")
		}
		if session.MerchantID != domain.MerchantID {
			return v1Err(c, fiber.StatusNotFound, "payment not found")
		}
		return v1OK(c, v1PaymentResponse(*session))
	}
}

// HandleV1PaymentHistory godoc
// @Summary Payment history
// @Description Returns paginated payment session history for the authenticated merchant.
// @Tags Payment
// @Produce json
// @Security ApiKeyAuth
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page (max 100)" default(20)
// @Param status query string false "Filter by status" Enums(pending,awaiting_payment,paid,expired,canceled,failed)
// @Success 200 {object} types.V1PaymentHistoryResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payment/history [get]
func HandleV1PaymentHistory(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		page := v1QueryInt(c, "page", 1)
		limit := v1QueryInt(c, "limit", 20)
		if limit > 100 {
			limit = 100
		}
		status := strings.TrimSpace(c.Query("status"))

		sessions, total, err := deps.PaymentRepo.ListByMerchantPage(c.Context(), domain.MerchantID, status, page, limit)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "failed to fetch payment history")
		}

		var items []fiber.Map
		for _, s := range sessions {
			items = append(items, v1PaymentResponse(s))
		}
		if items == nil {
			items = []fiber.Map{}
		}
		return v1OK(c, fiber.Map{
			"payments": items,
			"total":    total,
			"page":     page,
			"limit":    limit,
		})
	}
}

// HandleV1PaymentStatistics godoc
// @Summary Payment statistics
// @Description Returns count of payments grouped by status for the authenticated merchant.
// @Tags Payment
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} types.V1PaymentStatisticsResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payment/statistics [get]
func HandleV1PaymentStatistics(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		stats, err := deps.PaymentRepo.StatsByMerchant(c.Context(), domain.MerchantID)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "failed to fetch statistics")
		}

		statuses := []string{
			models.PaymentStatusPending,
			models.PaymentStatusAwaitingPayment,
			models.PaymentStatusPaid,
			models.PaymentStatusCanceled,
			models.PaymentStatusExpired,
			models.PaymentStatusFailed,
		}
		var total int64
		for _, s := range statuses {
			total += stats[s]
		}

		statusCounts := make([]fiber.Map, 0, len(statuses))
		for _, s := range statuses {
			statusCounts = append(statusCounts, fiber.Map{"status": s, "count": stats[s]})
		}
		return v1OK(c, fiber.Map{
			"total":    total,
			"statuses": statusCounts,
		})
	}
}

// HandleV1PaymentCurrencies godoc
// @Summary Accepted currencies
// @Description Lists all crypto currencies that can be accepted as payment.
// @Tags Payment
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} types.V1CurrenciesResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payment/currencies [get]
func HandleV1PaymentCurrencies(deps V1APIDeps) fiber.Handler {
	return HandleV1CommonCurrencies(deps)
}

// HandleV1PaymentStatusTable godoc
// @Summary Payment status table
// @Description Returns all possible payment statuses with descriptions and whether each is a terminal state.
// @Tags Payment
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} types.V1PaymentStatusTableResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payment/status-table [get]
func HandleV1PaymentStatusTable(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, err := v1ResolveDomain(c, deps.DomainRepo); err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		table := []fiber.Map{
			{"status": "pending", "description": "Payment session created, waiting for asset selection."},
			{"status": "awaiting_payment", "description": "Asset selected, waiting for blockchain deposit."},
			{"status": "paid", "description": "Deposit confirmed on-chain. Payment complete."},
			{"status": "expired", "description": "Payment window elapsed without deposit."},
			{"status": "canceled", "description": "Manually canceled by the customer or merchant."},
			{"status": "failed", "description": "Deposit detected but amount or confirmations did not match."},
		}
		return v1OK(c, fiber.Map{"statuses": table})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Payout API
// ────────────────────────────────────────────────────────────────────────────

// HandleV1PayoutCreate godoc
// @Summary Generate payout
// @Description Creates a withdrawal (payout) request from the merchant's wallet to the specified address. Requires admin approval before execution.
// @Tags Payout
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param payload body types.V1PayoutRequest true "Payout parameters"
// @Success 201 {object} types.V1PayoutCreateResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payout/create [post]
func HandleV1PayoutCreate(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		var body types.V1PayoutRequest
		if err := c.Bind().Body(&body); err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid request body")
		}

		chain := strings.TrimSpace(body.Chain)
		symbol := strings.TrimSpace(body.Symbol)
		tokenAddress := strings.TrimSpace(body.TokenAddress)
		toAddress := strings.TrimSpace(body.ToAddress)
		amount := strings.TrimSpace(body.Amount)

		if chain == "" {
			return v1Err(c, fiber.StatusBadRequest, "chain is required")
		}
		if toAddress == "" {
			return v1Err(c, fiber.StatusBadRequest, "to_address is required")
		}
		if amount == "" {
			return v1Err(c, fiber.StatusBadRequest, "amount is required")
		}
		if err := types.ValidatePositiveDecimal(amount); err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid amount: "+err.Error())
		}
		chain, token, symbol, decimals, err := resolveWithdrawalAsset(deps.AssetRegistry, chain, symbol, tokenAddress)
		if err != nil {
			return v1Err(c, fiber.StatusBadRequest, err.Error())
		}

		wallets, err := deps.WalletRepo.ListReserveByMerchant(c.Context(), domain.MerchantID)
		if err != nil || len(wallets) == 0 {
			return v1Err(c, fiber.StatusBadRequest, "no reserve wallet found — create a static address first to initialize the reserve wallet")
		}
		wallet := wallets[0]

		amountRaw, err := types.DecimalToRaw(amount, decimals)
		if err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid amount: "+err.Error())
		}

		req := &models.WithdrawalRequest{
			MerchantID:  domain.MerchantID,
			WalletID:    wallet.ID,
			Chain:       chain,
			Token:       token,
			Symbol:      symbol,
			Decimals:    decimals,
			ToAddress:   toAddress,
			AmountRaw:   amountRaw,
			Note:        strings.TrimSpace(body.Note),
			Status:      models.WithdrawalStatusPending,
			RequestedBy: "api",
		}
		if err := deps.WithdrawalRepo.CreateWithHold(c.Context(), req, deps.LedgerRepo); err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "payout creation failed: "+err.Error())
		}

		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"result": "ok",
			"data":   v1PayoutResponse(*req),
		})
	}
}

// HandleV1PayoutInfo godoc
// @Summary Payout information
// @Description Returns details of a specific payout request by payout_id.
// @Tags Payout
// @Produce json
// @Security ApiKeyAuth
// @Param payout_id query string true "Payout UUID" example(550e8400-e29b-41d4-a716-446655440000)
// @Success 200 {object} types.V1PayoutInfoResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Failure 404 {object} types.V1ErrorResponse
// @Router /api/v1/payout/info [get]
func HandleV1PayoutInfo(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		payoutIDStr := strings.TrimSpace(c.Query("payout_id"))
		if payoutIDStr == "" {
			return v1Err(c, fiber.StatusBadRequest, "payout_id is required")
		}
		payoutID, err := uuid.Parse(payoutIDStr)
		if err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid payout_id")
		}

		req, err := deps.WithdrawalRepo.Find(c.Context(), payoutID)
		if err != nil {
			return v1Err(c, fiber.StatusNotFound, "payout not found")
		}
		if req.MerchantID != domain.MerchantID {
			return v1Err(c, fiber.StatusNotFound, "payout not found")
		}
		return v1OK(c, v1PayoutResponse(*req))
	}
}

// HandleV1PayoutHistory godoc
// @Summary Payout history
// @Description Returns paginated payout (withdrawal) history for the authenticated merchant.
// @Tags Payout
// @Produce json
// @Security ApiKeyAuth
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page (max 100)" default(20)
// @Success 200 {object} types.V1PayoutHistoryResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payout/history [get]
func HandleV1PayoutHistory(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		page := v1QueryInt(c, "page", 1)
		limit := v1QueryInt(c, "limit", 20)
		if limit > 100 {
			limit = 100
		}

		requests, total, err := deps.WithdrawalRepo.ListByMerchantPage(c.Context(), domain.MerchantID, page, limit)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "failed to fetch payout history")
		}

		var items []fiber.Map
		for _, r := range requests {
			items = append(items, v1PayoutResponse(r))
		}
		if items == nil {
			items = []fiber.Map{}
		}
		return v1OK(c, fiber.Map{
			"payouts": items,
			"total":   total,
			"page":    page,
			"limit":   limit,
		})
	}
}

// HandleV1PayoutStatusTable godoc
// @Summary Payout status table
// @Description Returns all possible payout statuses with descriptions and whether each is a terminal state.
// @Tags Payout
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} types.V1PayoutStatusTableResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payout/status-table [get]
func HandleV1PayoutStatusTable(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, err := v1ResolveDomain(c, deps.DomainRepo); err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		table := []fiber.Map{
			{"status": "pending", "description": "Payout request received, awaiting admin approval."},
			{"status": "approved", "description": "Approved by admin. On-chain broadcast initiated."},
			{"status": "rejected", "description": "Rejected by admin. Funds not moved."},
			{"status": "failed", "description": "Broadcast failed due to on-chain error."},
		}
		return v1OK(c, fiber.Map{"statuses": table})
	}
}

// HandleV1RefundCreate godoc
// @Summary Create refund request
// @Description Creates a refund request for a paid payment. Provide payment_id OR order_id to identify the payment. Admin approval broadcasts the on-chain refund.
// @Tags Refund
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param payload body types.V1RefundRequest true "Refund parameters"
// @Success 201 {object} types.V1RefundCreateResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/refund/create [post]
func HandleV1RefundCreate(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		var body struct {
			PaymentID string `json:"payment_id"`
			OrderID   string `json:"order_id"`
			TrackID   string `json:"track_id"`
			AmountRaw string `json:"amount_raw"`
			Reason    string `json:"reason"`
		}
		if err := c.Bind().Body(&body); err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid request body")
		}
		session, err := v1ResolvePayment(c, deps, domain.MerchantID, body.PaymentID, body.TrackID, body.OrderID)
		if err != nil {
			return v1Err(c, fiber.StatusBadRequest, err.Error())
		}
		if session.Status != models.PaymentStatusPaid {
			return v1Err(c, fiber.StatusBadRequest, "only paid payments can be refunded")
		}
		amountRaw := strings.TrimSpace(body.AmountRaw)
		if amountRaw == "" {
			amountRaw = session.ExpectedAmountRaw
		}
		if _, ok := stringsToPositiveBigInt(amountRaw); !ok {
			return v1Err(c, fiber.StatusBadRequest, "amount_raw must be a positive integer")
		}
		refund := &models.Refund{
			MerchantID:  domain.MerchantID,
			DomainID:    domain.ID,
			PaymentID:   session.ID,
			AmountRaw:   amountRaw,
			Reason:      strings.TrimSpace(body.Reason),
			Status:      models.RefundStatusPending,
			RequestedBy: "api",
		}
		if err := deps.RefundRepo.Create(c.Context(), refund); err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "refund creation failed: "+err.Error())
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"result": "ok", "data": v1RefundResponse(*refund)})
	}
}

// HandleV1RefundInfo godoc
// @Summary Get refund info
// @Description Returns a refund request owned by the authenticated merchant.
// @Tags Refund
// @Produce json
// @Security ApiKeyAuth
// @Param refund_id query string true "Refund UUID" example(550e8400-e29b-41d4-a716-446655440000)
// @Success 200 {object} types.V1RefundInfoResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Failure 404 {object} types.V1ErrorResponse
// @Router /api/v1/refund/info [get]
func HandleV1RefundInfo(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		id, err := uuid.Parse(strings.TrimSpace(c.Query("refund_id")))
		if err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid refund_id")
		}
		refund, err := deps.RefundRepo.Find(c.Context(), id)
		if err != nil || refund.MerchantID != domain.MerchantID {
			return v1Err(c, fiber.StatusNotFound, "refund not found")
		}
		return v1OK(c, v1RefundResponse(*refund))
	}
}

// HandleV1RefundHistory godoc
// @Summary Get refund history
// @Description Returns paginated refund requests for the authenticated merchant.
// @Tags Refund
// @Produce json
// @Security ApiKeyAuth
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page (max 100)" default(20)
// @Success 200 {object} types.V1RefundHistoryResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/refund/history [get]
func HandleV1RefundHistory(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		page := v1QueryInt(c, "page", 1)
		limit := v1QueryInt(c, "limit", 20)
		refunds, total, err := deps.RefundRepo.ListByMerchantPage(c.Context(), domain.MerchantID, page, limit)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "failed to fetch refund history")
		}
		items := make([]fiber.Map, 0, len(refunds))
		for _, refund := range refunds {
			items = append(items, v1RefundResponse(refund))
		}
		return v1OK(c, fiber.Map{"refunds": items, "total": total, "page": page, "limit": limit})
	}
}

func v1ResolvePayment(c fiber.Ctx, deps V1APIDeps, merchantID uuid.UUID, paymentID, trackID, orderID string) (*models.PaymentSession, error) {
	var session *models.PaymentSession
	var err error
	switch {
	case strings.TrimSpace(paymentID) != "":
		id, parseErr := uuid.Parse(strings.TrimSpace(paymentID))
		if parseErr != nil {
			return nil, fmt.Errorf("invalid payment_id")
		}
		session, err = deps.PaymentRepo.FindByID(c.Context(), id)
	case strings.TrimSpace(trackID) != "":
		session, err = deps.PaymentRepo.FindByToken(c.Context(), strings.TrimSpace(trackID))
	case strings.TrimSpace(orderID) != "":
		session, err = deps.PaymentRepo.FindByOrderID(c.Context(), merchantID, strings.TrimSpace(orderID))
	default:
		return nil, fmt.Errorf("payment_id, track_id or order_id is required")
	}
	if err != nil {
		return nil, fmt.Errorf("payment not found")
	}
	if session.MerchantID != merchantID {
		return nil, fmt.Errorf("payment not found")
	}
	return session, nil
}

func v1RefundResponse(r models.Refund) fiber.Map {
	return fiber.Map{
		"refund_id":    r.ID.String(),
		"payment_id":   r.PaymentID.String(),
		"merchant_id":  r.MerchantID.String(),
		"domain_id":    r.DomainID.String(),
		"amount_raw":   r.AmountRaw,
		"status":       r.Status,
		"reason":       r.Reason,
		"tx_hash":      r.TxHash,
		"error":        r.Error,
		"requested_by": r.RequestedBy,
		"reviewed_by":  r.ReviewedBy,
		"created_at":   r.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func stringsToPositiveBigInt(value string) (*big.Int, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, ".") || strings.HasPrefix(value, "-") {
		return nil, false
	}
	amount, ok := new(big.Int).SetString(value, 10)
	return amount, ok && amount.Sign() > 0
}

// ────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ────────────────────────────────────────────────────────────────────────────

func formatV1Amount(raw string, decimals uint8) string {
	if raw == "" || raw == "0" || decimals == 0 {
		return raw
	}
	for len(raw) <= int(decimals) {
		raw = "0" + raw
	}
	pos := len(raw) - int(decimals)
	whole := raw[:pos]
	frac := strings.TrimRight(raw[pos:], "0")
	if whole == "" {
		whole = "0"
	}
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}

func v1PaymentResponse(s models.PaymentSession) fiber.Map {
	var paidAt, expiresAt string
	if s.PaidAt != nil {
		paidAt = s.PaidAt.UTC().Format(time.RFC3339)
	}
	if s.ExpiresAt != nil {
		expiresAt = s.ExpiresAt.UTC().Format(time.RFC3339)
	}
	var chainID *int64
	if s.SelectedChainID != nil {
		v := int64(*s.SelectedChainID)
		chainID = &v
	}
	return fiber.Map{
		"payment_id":          s.ID.String(),
		"track_id":            s.SessionToken,
		"order_id":            s.OrderID,
		"product_id":          s.ProductID,
		"user_id":             s.UserID,
		"status":              s.Status,
		"amount":              s.Amount,
		"currency":            s.Currency,
		"chain_id":            chainID,
		"symbol":              s.SelectedSymbol,
		"deposit_address":     s.DepositAddress,
		"expected_amount_raw": s.ExpectedAmountRaw,
		"tx_hash":             s.TxHash,
		"paid_at":             paidAt,
		"expires_at":          expiresAt,
		"created_at":          s.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func v1PayoutResponse(r models.WithdrawalRequest) fiber.Map {
	var reviewedAt string
	if r.ReviewedAt != nil {
		reviewedAt = r.ReviewedAt.UTC().Format(time.RFC3339)
	}
	token := ""
	if r.Token != nil {
		token = *r.Token
	}
	return fiber.Map{
		"payout_id":     r.ID.String(),
		"chain":         r.Chain,
		"symbol":        r.Symbol,
		"token_address": token,
		"to_address":    r.ToAddress,
		"amount_raw":    r.AmountRaw,
		"decimals":      r.Decimals,
		"note":          r.Note,
		"status":        r.Status,
		"tx_hash":       r.TxHash,
		"error":         r.Error,
		"reviewed_at":   reviewedAt,
		"created_at":    r.CreatedAt.UTC().Format(time.RFC3339),
	}
}

type v1WalletItemType struct {
	WalletID  string            `json:"wallet_id"`
	UserID    string            `json:"user_id"`
	ProductID string            `json:"product_id"`
	Addresses map[string]string `json:"addresses"`
	CreatedAt string            `json:"created_at"`
}

func v1WalletItem(w models.Wallet) v1WalletItemType {
	addrs := make(map[string]string)
	if w.EthereumAddress != "" {
		addrs["ethereum"] = w.EthereumAddress
	}
	if w.BitcoinAddress != "" {
		addrs["bitcoin"] = w.BitcoinAddress
	}
	if w.BinanceAddress != "" {
		addrs["bnbchain"] = w.BinanceAddress
	}
	if w.AvalancheAddress != "" {
		addrs["avalanche"] = w.AvalancheAddress
	}
	if w.BaseAddress != "" {
		addrs["base"] = w.BaseAddress
	}
	if w.ArbitrumAddress != "" {
		addrs["arbitrum"] = w.ArbitrumAddress
	}
	if w.UnichainAddress != "" {
		addrs["unichain"] = w.UnichainAddress
	}
	if w.TronAddress != "" {
		addrs["tron"] = w.TronAddress
	}
	if w.SolanaAddress != "" {
		addrs["solana"] = w.SolanaAddress
	}
	if w.ChilizAddress != "" {
		addrs["chiliz"] = w.ChilizAddress
	}
	if w.ChilizSpicyAddress != "" {
		addrs["chiliz_spicy"] = w.ChilizSpicyAddress
	}
	return v1WalletItemType{
		WalletID:  w.ID.String(),
		UserID:    w.UserID,
		ProductID: w.ProductID,
		Addresses: addrs,
		CreatedAt: w.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func v1WalletResponse(w *models.Wallet) fiber.Map {
	item := v1WalletItem(*w)
	return fiber.Map{
		"wallet_id":  item.WalletID,
		"user_id":    item.UserID,
		"product_id": item.ProductID,
		"addresses":  item.Addresses,
		"created_at": item.CreatedAt,
	}
}

func findAssetByChain(registry *asset.Registry, chainName string) asset.Asset {
	if registry == nil {
		return nil
	}
	chainLower := strings.ToLower(chainName)
	for _, a := range registry.ListAll() {
		name := strings.ToLower(chainLabel(a.GetChainID()))
		if strings.Contains(name, chainLower) || strings.Contains(chainLower, strings.ToLower(a.GetSymbol())) {
			return a
		}
	}
	return nil
}
