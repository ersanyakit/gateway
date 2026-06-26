package handlers

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/helpers"
	"core/models"
	"core/repositories"
	"core/services/pricing"
	"core/services/realtime"
	"core/services/txrescan"
	webhooksvc "core/services/webhook"
	"core/types"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
	"gorm.io/gorm"
)

// V1APIDeps holds all dependencies used by the v1 REST API endpoints.
type V1APIDeps struct {
	DomainRepo          *repositories.DomainRepo
	WalletRepo          *repositories.WalletRepo
	PaymentRepo         *repositories.PaymentRepo
	WithdrawalRepo      *repositories.WithdrawalRequestRepo
	RefundRepo          *repositories.RefundRepo
	LedgerRepo          *repositories.LedgerRepo
	TransactionRepo     *repositories.TransactionRepo
	WebhookDeliveryRepo *repositories.WebhookDeliveryRepo
	SweepJobRepo        *repositories.SweepJobRepo
	ReconciliationRepo  *repositories.ReconciliationRepo
	AssetRegistry       *asset.Registry
	Blockchains         *blockchain.ChainFactory
	PriceOracle         pricing.PriceOracle
	Notifier            *webhooksvc.Notifier
	PaymentHub          *realtime.PaymentHub
	IdempotencyRepo     *repositories.IdempotencyRepo
	TxRescanService     func() *txrescan.Service
}

// ────────────────────────────────────────────────────────────────────────────
// Auth helper
// ────────────────────────────────────────────────────────────────────────────

func v1ResolveDomain(c fiber.Ctx, domainRepo v1DomainLookup) (*models.Domain, error) {
	key := strings.TrimSpace(c.Get("X-API-Key"))
	if key == "" {
		auth := strings.TrimSpace(c.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			key = strings.TrimSpace(auth[7:])
		}
	}
	if key == "" {
		err := fmt.Errorf("X-API-Key header or Authorization: Bearer <key> is required")
		v1LogAuthFailure(c, nil, "missing_api_key", err)
		return nil, err
	}
	domain, err := domainRepo.FindByAPIKey(types.DomainParams{Context: c.Context(), APIKey: &key})
	if err != nil {
		v1LogAuthFailure(c, nil, "invalid_api_key", err)
		return nil, err
	}
	return domain, nil
}

func v1ResolveSignedDomain(c fiber.Ctx, domainRepo v1DomainLookup) (*models.Domain, error) {
	domain, err := v1ResolveDomain(c, domainRepo)
	if err != nil {
		return nil, err
	}
	apiSecret := strings.TrimSpace(c.Get("X-API-Secret"))
	if apiSecret == "" {
		err := fmt.Errorf("X-API-Secret header is required")
		v1LogAuthFailure(c, domain, "missing_api_secret", err)
		return nil, err
	}
	secretDomain, err := domainRepo.FindByAPISecret(types.DomainParams{Context: c.Context(), APISecret: &apiSecret})
	if err != nil {
		err := fmt.Errorf("invalid API credentials")
		v1LogAuthFailure(c, domain, "invalid_api_secret", err)
		return nil, err
	}
	if secretDomain.ID != domain.ID {
		err := fmt.Errorf("invalid API credentials")
		v1LogAuthFailure(c, domain, "api_key_secret_mismatch", err)
		return nil, err
	}
	timestamp := strings.TrimSpace(c.Get("X-Gateway-Timestamp"))
	if timestamp == "" {
		err := fmt.Errorf("X-Gateway-Timestamp header is required")
		v1LogAuthFailure(c, domain, "missing_timestamp", err)
		return nil, err
	}
	if err := helpers.ValidateTimestamp(timestamp); err != nil {
		v1LogAuthFailure(c, domain, "invalid_timestamp", err)
		return nil, err
	}
	signature := strings.TrimSpace(c.Get("X-Gateway-Signature"))
	signature = strings.TrimPrefix(signature, "sha256=")
	if signature == "" {
		err := fmt.Errorf("X-Gateway-Signature header is required")
		v1LogAuthFailure(c, domain, "missing_signature", err)
		return nil, err
	}
	if !helpers.VerifyRequestSignature(apiSecret, c.Method(), v1CanonicalRequestTarget(c), timestamp, c.Body(), signature) {
		err := fmt.Errorf("invalid request signature")
		v1LogAuthFailure(c, domain, "invalid_signature", err)
		return nil, err
	}
	if !v1RequestReplayAccepted(domain, c, timestamp, signature) {
		err := v1ReplayError()
		v1LogAuthFailure(c, domain, "signature_replay", err)
		return nil, err
	}
	return domain, nil
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
// @Description Returns the ledger balance for each asset held under the authenticated API domain.
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
			rows, err := deps.LedgerRepo.DomainBalances(c.Context(), domain.MerchantID, domain.ID)
			if err != nil {
				return v1Err(c, fiber.StatusInternalServerError, "failed to fetch ledger balances")
			}
			for _, row := range rows {
				if row.Account != models.LedgerAccountMerchantAvailable {
					continue
				}
				logoURL := ""
				if deps.AssetRegistry != nil {
					logoURL = deps.AssetRegistry.LogoURL(row.Symbol)
				}
				items = append(items, balanceItem{
					Symbol:   row.Symbol,
					Chain:    chainLabel(constants.ChainID(row.ChainID)),
					ChainID:  row.ChainID,
					Balance:  formatV1Amount(row.BalanceRaw, row.Decimals),
					Decimals: row.Decimals,
					LogoURL:  logoURL,
				})
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
					LogoURL:  deps.AssetRegistry.LogoURL(canonical),
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
			Type         string `json:"type"`
			Network      string `json:"network"`
			ChainID      int64  `json:"chain_id"`
			Decimals     uint8  `json:"decimals"`
			Native       bool   `json:"native"`
			Identifier   string `json:"identifier"`
			TokenAddress string `json:"token_address,omitempty"`
			MintAddress  string `json:"mint_address,omitempty"`
			LogoURL      string `json:"logo_url,omitempty"`
			ChainLogoURL string `json:"chain_logo_url,omitempty"`
		}

		var items []currencyItem
		if deps.AssetRegistry != nil {
			for _, a := range deps.AssetRegistry.ListAll() {
				items = append(items, currencyItem{
					Symbol:       a.GetSymbol(),
					Name:         a.GetName(),
					Type:         asset.AssetTypeName(a.GetType()),
					Network:      chainLabel(a.GetChainID()),
					ChainID:      int64(a.GetChainID()),
					Decimals:     a.GetDecimals(),
					Native:       a.IsNative(),
					Identifier:   a.GetIdentifier(),
					TokenAddress: asset.TokenAddress(a),
					MintAddress:  asset.MintAddress(a),
					LogoURL:      deps.AssetRegistry.LogoURL(a.GetSymbol()),
					ChainLogoURL: asset.ChainLogoURL(a.GetChainID()),
				})
			}
		}
		return v1OK(c, fiber.Map{"currencies": items})
	}
}

// HandleV1CommonAssets godoc
// @Summary Supported assets
// @Description Lists all supported crypto assets grouped by logical asset with their chain deployments.
// @Tags Common
// @Produce json
// @Success 200 {object} types.V1AssetsResponse
// @Router /api/v1/common/assets [get]
func HandleV1CommonAssets(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		return v1OK(c, fiber.Map{"assets": v1AssetCatalog(deps.AssetRegistry)})
	}
}

// HandleV1CommonAddressQRCode godoc
// @Summary Address QR code
// @Description Returns a PNG QR code containing the supplied wallet address or payment URI payload.
// @Tags Common
// @Produce png
// @Param address query string true "Wallet address or payment URI payload"
// @Param size query int false "PNG image size in pixels" minimum(128) maximum(1024) default(300)
// @Success 200 {file} binary "PNG QR code"
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 500 {object} types.V1ErrorResponse
// @Router /api/v1/common/qrcode [get]
func HandleV1CommonAddressQRCode() fiber.Handler {
	return func(c fiber.Ctx) error {
		address := strings.TrimSpace(c.Query("address"))
		if address == "" {
			return v1Err(c, fiber.StatusBadRequest, "address is required")
		}
		if len(address) > 2048 {
			return v1Err(c, fiber.StatusBadRequest, "address is too long")
		}

		size := 300
		sizeRaw := strings.TrimSpace(c.Query("size"))
		if sizeRaw != "" {
			parsed, err := strconv.Atoi(sizeRaw)
			if err != nil || parsed < 128 || parsed > 1024 {
				return v1Err(c, fiber.StatusBadRequest, "size must be between 128 and 1024")
			}
			size = parsed
		}

		png, err := qrcode.Encode(address, qrcode.Medium, size)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "QR generation failed")
		}
		c.Set("Content-Type", "image/png")
		return c.Send(png)
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
// Wallet provider API
// ────────────────────────────────────────────────────────────────────────────

// HandleV1WalletCreate godoc
// @Summary Create wallet
// @Description Creates or returns a reusable multi-chain wallet for a merchant user. This endpoint is for wallet-provider integrations.
// @Tags Wallet
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param X-API-Secret header string true "API secret returned when the domain was created or rotated"
// @Param X-Gateway-Timestamp header string true "Unix timestamp used in HMAC signature"
// @Param X-Gateway-Signature header string true "HMAC-SHA256 over method + path/query + timestamp + raw body, optionally prefixed with sha256="
// @Param payload body types.V1WalletCreateRequest true "Wallet create parameters"
// @Success 201 {object} types.V1WalletCreateResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/wallet/create [post]
func HandleV1WalletCreate(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveSignedDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		if deps.WalletRepo == nil {
			return v1Err(c, fiber.StatusInternalServerError, "wallet repository is not ready")
		}

		var body types.V1WalletCreateRequest
		if err := c.Bind().Body(&body); err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid request body")
		}
		userID := strings.TrimSpace(body.UserID)
		if userID == "" {
			return v1Err(c, fiber.StatusBadRequest, "user_id is required")
		}
		if len(userID) > 128 {
			return v1Err(c, fiber.StatusBadRequest, "user_id must be at most 128 characters")
		}
		productID := v1WalletProductID(body.ProductID)
		if len(productID) > 128 {
			return v1Err(c, fiber.StatusBadRequest, "product_id must be at most 128 characters")
		}
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
		if deps.Blockchains != nil {
			if err := deps.WalletRepo.EnsureAllAddresses(c.Context(), wallet.ID, deps.Blockchains); err != nil {
				return v1Err(c, fiber.StatusInternalServerError, "wallet address backfill failed: "+err.Error())
			}
			if refreshed, err := deps.WalletRepo.FindByID(c.Context(), wallet.ID); err == nil {
				wallet = refreshed
			}
		}

		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"result": "ok",
			"data":   v1WalletResponse(wallet),
		})
	}
}

// HandleV1WalletInfo godoc
// @Summary Wallet information
// @Description Returns a wallet by wallet_id, or by user_id plus optional product_id under the authenticated API domain.
// @Tags Wallet
// @Produce json
// @Security ApiKeyAuth
// @Param wallet_id query string false "Wallet UUID"
// @Param user_id query string false "Merchant user ID"
// @Param product_id query string false "Wallet product scope. Defaults to wallet"
// @Success 200 {object} types.V1WalletInfoResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Failure 404 {object} types.V1ErrorResponse
// @Router /api/v1/wallet/info [get]
func HandleV1WalletInfo(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		wallet, err := v1FindDomainWallet(c.Context(), deps, domain, c)
		if err != nil {
			status := fiber.StatusNotFound
			if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "invalid") {
				status = fiber.StatusBadRequest
			}
			return v1Err(c, status, err.Error())
		}
		return v1OK(c, v1WalletResponse(wallet))
	}
}

// HandleV1WalletList godoc
// @Summary Wallet list
// @Description Lists wallet-provider wallets under the authenticated API domain, optionally filtered by user_id.
// @Tags Wallet
// @Produce json
// @Security ApiKeyAuth
// @Param user_id query string false "Filter by user ID"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page (max 100)" default(20)
// @Success 200 {object} types.V1WalletListResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/wallet/list [get]
func HandleV1WalletList(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		if deps.WalletRepo == nil {
			return v1Err(c, fiber.StatusInternalServerError, "wallet repository is not ready")
		}

		page := v1QueryInt(c, "page", 1)
		limit := v1QueryInt(c, "limit", 20)
		if limit > 100 {
			limit = 100
		}
		search := strings.TrimSpace(c.Query("user_id"))
		offset := (page - 1) * limit
		wallets, total, err := deps.WalletRepo.ListProviderByDomainPage(c.Context(), domain.MerchantID, domain.ID, search, limit, offset)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "failed to list wallets")
		}
		items := make([]v1WalletItemType, 0, len(wallets))
		for _, wallet := range wallets {
			items = append(items, v1WalletItem(wallet))
		}
		return v1OK(c, fiber.Map{
			"wallets": items,
			"total":   total,
			"page":    page,
			"limit":   limit,
		})
	}
}

// HandleV1WalletBalance godoc
// @Summary Wallet balance
// @Description Returns ledger balances scoped to one wallet-provider wallet.
// @Tags Wallet
// @Produce json
// @Security ApiKeyAuth
// @Param wallet_id query string false "Wallet UUID"
// @Param user_id query string false "Merchant user ID"
// @Param product_id query string false "Wallet product scope. Defaults to wallet"
// @Success 200 {object} types.V1WalletBalanceResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Failure 404 {object} types.V1ErrorResponse
// @Router /api/v1/wallet/balance [get]
func HandleV1WalletBalance(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		wallet, err := v1FindDomainWallet(c.Context(), deps, domain, c)
		if err != nil {
			status := fiber.StatusNotFound
			if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "invalid") {
				status = fiber.StatusBadRequest
			}
			return v1Err(c, status, err.Error())
		}
		balances, err := v1WalletBalances(c.Context(), deps, domain, wallet)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "failed to fetch wallet balances")
		}
		return v1OK(c, fiber.Map{
			"wallet":   v1WalletResponse(wallet),
			"balances": balances,
		})
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
// @Param X-API-Secret header string true "API secret returned when the domain was created or rotated"
// @Param X-Gateway-Timestamp header string true "Unix timestamp used in HMAC signature"
// @Param X-Gateway-Signature header string true "HMAC-SHA256 over method + path/query + timestamp + raw body, optionally prefixed with sha256="
// @Param payload body types.V1InvoiceRequest true "Invoice parameters"
// @Success 201 {object} types.V1PaymentCreateResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Failure 409 {object} types.V1ErrorResponse
// @Router /api/v1/payment/create [post]
func HandleV1PaymentCreate(deps V1APIDeps) fiber.Handler {
	return handlePaymentCreate(PaymentHandlerDeps{
		DomainRepo:       deps.DomainRepo,
		WalletRepo:       deps.WalletRepo,
		PaymentRepo:      deps.PaymentRepo,
		AssetRegistry:    deps.AssetRegistry,
		Blockchains:      deps.Blockchains,
		PriceOracle:      deps.PriceOracle,
		Notifier:         deps.Notifier,
		PaymentHub:       deps.PaymentHub,
		IdempotencyRepo:  deps.IdempotencyRepo,
		RequireSignature: true,
	}, paymentCreateModeV1)
}

// HandleV1PaymentWhiteLabel godoc
// @Summary Generate white label payment
// @Description Creates a white label hosted checkout session. Identical to Generate Invoice but returns a branded URL.
// @Tags Payment
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param X-API-Secret header string true "API secret returned when the domain was created or rotated"
// @Param X-Gateway-Timestamp header string true "Unix timestamp used in HMAC signature"
// @Param X-Gateway-Signature header string true "HMAC-SHA256 over method + path/query + timestamp + raw body, optionally prefixed with sha256="
// @Param payload body types.V1InvoiceRequest true "Invoice parameters"
// @Success 201 {object} types.V1PaymentCreateResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Failure 409 {object} types.V1ErrorResponse
// @Router /api/v1/payment/white-label [post]
func HandleV1PaymentWhiteLabel(deps V1APIDeps) fiber.Handler {
	return HandleV1PaymentCreate(deps)
}

// HandleV1PaymentStaticAddressCreate godoc
// @Summary Generate static address
// @Description Creates a permanent deposit wallet for a user and asset scope. Subsequent calls with the same domain, product_id, user_id, chain, symbol, and token return the existing address.
// @Tags Payment
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param X-API-Secret header string true "API secret returned when the domain was created or rotated"
// @Param X-Gateway-Timestamp header string true "Unix timestamp used in HMAC signature"
// @Param X-Gateway-Signature header string true "HMAC-SHA256 over method + path/query + timestamp + raw body, optionally prefixed with sha256="
// @Param payload body types.V1StaticAddressRequest true "Static address parameters"
// @Success 200 {object} types.V1StaticAddressResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payment/static-address [post]
func HandleV1PaymentStaticAddressCreate(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveSignedDomain(c, deps.DomainRepo)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		if deps.WalletRepo == nil {
			return v1Err(c, fiber.StatusInternalServerError, "wallet repository is not ready")
		}

		var body types.V1StaticAddressRequest
		if err := c.Bind().Body(&body); err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid request body")
		}
		userID := strings.TrimSpace(body.UserID)
		if userID == "" {
			return v1Err(c, fiber.StatusBadRequest, "user_id is required")
		}
		if len(userID) > 128 {
			return v1Err(c, fiber.StatusBadRequest, "user_id must be at most 128 characters")
		}
		scope, err := v1ResolveStaticAddressScope(deps.AssetRegistry, body)
		if err != nil {
			return v1Err(c, fiber.StatusBadRequest, err.Error())
		}
		if err := v1ValidateStaticAddressChainReady(deps.Blockchains, scope.ChainID); err != nil {
			status := fiber.StatusBadRequest
			if strings.Contains(err.Error(), "not ready") {
				status = fiber.StatusInternalServerError
			}
			return v1Err(c, status, err.Error())
		}
		if len(scope.ProductID) > 128 {
			return v1Err(c, fiber.StatusBadRequest, "product_id scope must be at most 128 characters")
		}

		wallet, err := v1CreateStaticAddressWallet(c.Context(), deps.WalletRepo, deps.Blockchains, domain, userID, scope)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, err.Error())
		}

		resp, err := v1StaticAddressResponseForScope(wallet, scope, body.Label)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, err.Error())
		}
		return v1OK(c, resp)
	}
}

// HandleV1PaymentStaticAddressList godoc
// @Summary Static address list
// @Description Lists static deposit wallets under the authenticated API domain, optionally filtered by user_id.
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

		wallets, total, err := deps.WalletRepo.ListStaticByDomainPage(c.Context(), domain.MerchantID, domain.ID, search, limit, offset)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "failed to list wallets")
		}

		items := make([]fiber.Map, 0, len(wallets))
		for _, w := range wallets {
			items = append(items, v1StaticWalletListItem(w))
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
			session, err = deps.PaymentRepo.FindByTokenForDomain(c.Context(), domain.MerchantID, domain.ID, trackID)
		} else if orderID != "" {
			session, err = deps.PaymentRepo.FindByOrderIDForDomain(c.Context(), domain.MerchantID, domain.ID, orderID)
		} else {
			return v1Err(c, fiber.StatusBadRequest, "track_id or order_id query param is required")
		}

		if err != nil {
			return v1Err(c, fiber.StatusNotFound, "payment not found")
		}
		return v1OK(c, v1PaymentResponse(*session))
	}
}

// HandleV1PaymentHistory godoc
// @Summary Payment history
// @Description Returns paginated payment session history for the authenticated API domain.
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

		sessions, total, err := deps.PaymentRepo.ListByDomainPage(c.Context(), domain.MerchantID, domain.ID, status, page, limit)
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
// @Description Returns count of payments grouped by status for the authenticated API domain.
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

		stats, err := deps.PaymentRepo.StatsByDomain(c.Context(), domain.MerchantID, domain.ID)
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

// HandleV1PaymentAssets godoc
// @Summary Accepted assets
// @Description Lists all crypto assets that can be accepted as payment grouped by logical asset with their chain deployments.
// @Tags Payment
// @Produce json
// @Success 200 {object} types.V1AssetsResponse
// @Router /api/v1/payment/assets [get]
func HandleV1PaymentAssets(deps V1APIDeps) fiber.Handler {
	return HandleV1CommonAssets(deps)
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
// @Param X-API-Secret header string true "API secret returned when the domain was created or rotated"
// @Param X-Gateway-Timestamp header string true "Unix timestamp used in HMAC signature"
// @Param X-Gateway-Signature header string true "HMAC-SHA256 over method + path/query + timestamp + raw body, optionally prefixed with sha256="
// @Param payload body types.V1PayoutRequest true "Payout parameters"
// @Success 201 {object} types.V1PayoutCreateResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/payout/create [post]
func HandleV1PayoutCreate(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveSignedDomain(c, deps.DomainRepo)
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

		wallet, err := ensureV1ReserveWallet(c.Context(), deps, domain.MerchantID)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "reserve wallet initialization failed: "+err.Error())
		}

		amountRaw, err := types.DecimalToRaw(amount, decimals)
		if err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid amount: "+err.Error())
		}

		req := &models.WithdrawalRequest{
			MerchantID:  domain.MerchantID,
			DomainID:    &domain.ID,
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
		if deps.WebhookDeliveryRepo != nil {
			payload := webhooksvc.NewPayoutPayload(constants.WebhookEventPayoutRequestedV1, *req)
			if _, _, err := deps.WebhookDeliveryRepo.EnqueueLifecycle(c.Context(), *domain, payload); err != nil {
				fmt.Println("payout lifecycle webhook enqueue error:", err)
			}
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

		req, err := deps.WithdrawalRepo.FindByDomain(c.Context(), domain.MerchantID, domain.ID, payoutID)
		if err != nil {
			return v1Err(c, fiber.StatusNotFound, "payout not found")
		}
		return v1OK(c, v1PayoutResponse(*req))
	}
}

// HandleV1PayoutHistory godoc
// @Summary Payout history
// @Description Returns paginated payout (withdrawal) history for the authenticated API domain.
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

		requests, total, err := deps.WithdrawalRepo.ListByDomainPage(c.Context(), domain.MerchantID, domain.ID, page, limit)
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
			{"status": "processing", "description": "Approved by admin. On-chain broadcast or ledger finalization is in progress."},
			{"status": "approved", "description": "Approved by admin and on-chain broadcast completed."},
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
// @Param X-API-Secret header string true "API secret returned when the domain was created or rotated"
// @Param X-Gateway-Timestamp header string true "Unix timestamp used in HMAC signature"
// @Param X-Gateway-Signature header string true "HMAC-SHA256 over method + path/query + timestamp + raw body, optionally prefixed with sha256="
// @Param payload body types.V1RefundRequest true "Refund parameters"
// @Success 201 {object} types.V1RefundCreateResponse
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Router /api/v1/refund/create [post]
func HandleV1RefundCreate(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveSignedDomain(c, deps.DomainRepo)
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
		session, err := v1ResolvePayment(c, deps, domain.MerchantID, domain.ID, body.PaymentID, body.TrackID, body.OrderID)
		if err != nil {
			return v1Err(c, fiber.StatusBadRequest, err.Error())
		}
		if session.Status != models.PaymentStatusPaid {
			return v1Err(c, fiber.StatusBadRequest, "only paid payments can be refunded")
		}
		if session.SelectedChainID == nil || !constants.IsSupportedChainID(*session.SelectedChainID) {
			return v1Err(c, fiber.StatusBadRequest, "payment chain is missing or unsupported")
		}
		amountRaw := strings.TrimSpace(body.AmountRaw)
		if amountRaw == "" {
			amountRaw = session.ExpectedAmountRaw
		}
		requestedRaw, ok := stringsToPositiveBigInt(amountRaw)
		if !ok {
			return v1Err(c, fiber.StatusBadRequest, "amount_raw must be a positive integer")
		}
		limitRaw, err := v1RefundLimitRaw(c.Context(), deps, session)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "refund limit check failed: "+err.Error())
		}
		activeTotal, err := deps.RefundRepo.ActiveTotalRawByPayment(c.Context(), session.ID)
		if err != nil {
			return v1Err(c, fiber.StatusInternalServerError, "refund total check failed: "+err.Error())
		}
		if new(big.Int).Add(activeTotal, requestedRaw).Cmp(limitRaw) > 0 {
			return v1Err(c, fiber.StatusBadRequest, "refund amount exceeds refundable payment amount")
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
		if err := deps.RefundRepo.CreateWithHold(c.Context(), refund, *session, deps.LedgerRepo); err != nil {
			status := fiber.StatusInternalServerError
			if errors.Is(err, repositories.ErrInsufficientAvailableBalance) {
				status = fiber.StatusBadRequest
			}
			return v1Err(c, status, "refund creation failed: "+err.Error())
		}
		if deps.WebhookDeliveryRepo != nil {
			payload := webhooksvc.NewRefundPayload(constants.WebhookEventRefundRequestedV1, *refund)
			if _, _, err := deps.WebhookDeliveryRepo.EnqueueLifecycle(c.Context(), *domain, payload); err != nil {
				fmt.Println("refund lifecycle webhook enqueue error:", err)
			}
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"result": "ok", "data": v1RefundResponse(*refund)})
	}
}

// HandleV1RefundInfo godoc
// @Summary Get refund info
// @Description Returns a refund request owned by the authenticated API domain.
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
		refund, err := deps.RefundRepo.FindByDomain(c.Context(), domain.MerchantID, domain.ID, id)
		if err != nil {
			return v1Err(c, fiber.StatusNotFound, "refund not found")
		}
		return v1OK(c, v1RefundResponse(*refund))
	}
}

// HandleV1RefundHistory godoc
// @Summary Get refund history
// @Description Returns paginated refund requests for the authenticated API domain.
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
		refunds, total, err := deps.RefundRepo.ListByDomainPage(c.Context(), domain.MerchantID, domain.ID, page, limit)
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

func v1ResolvePayment(c fiber.Ctx, deps V1APIDeps, merchantID, domainID uuid.UUID, paymentID, trackID, orderID string) (*models.PaymentSession, error) {
	var session *models.PaymentSession
	var err error
	switch {
	case strings.TrimSpace(paymentID) != "":
		id, parseErr := uuid.Parse(strings.TrimSpace(paymentID))
		if parseErr != nil {
			return nil, fmt.Errorf("invalid payment_id")
		}
		session, err = deps.PaymentRepo.FindByIDForDomain(c.Context(), merchantID, domainID, id)
	case strings.TrimSpace(trackID) != "":
		session, err = deps.PaymentRepo.FindByTokenForDomain(c.Context(), merchantID, domainID, strings.TrimSpace(trackID))
	case strings.TrimSpace(orderID) != "":
		session, err = deps.PaymentRepo.FindByOrderIDForDomain(c.Context(), merchantID, domainID, strings.TrimSpace(orderID))
	default:
		return nil, fmt.Errorf("payment_id, track_id or order_id is required")
	}
	if err != nil {
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

func v1RefundLimitRaw(ctx context.Context, deps V1APIDeps, session *models.PaymentSession) (*big.Int, error) {
	expected, ok := stringsToPositiveBigInt(session.ExpectedAmountRaw)
	if !ok {
		return nil, fmt.Errorf("payment expected amount is invalid")
	}
	limit := new(big.Int).Set(expected)
	if deps.TransactionRepo == nil || session.TxUniqueHash == nil || strings.TrimSpace(*session.TxUniqueHash) == "" {
		return limit, nil
	}
	txModel, err := deps.TransactionRepo.FindByUniqueHash(ctx, strings.TrimSpace(*session.TxUniqueHash))
	if err != nil {
		return limit, nil
	}
	actual, ok := stringsToPositiveBigInt(txModel.Amount)
	if ok && actual.Cmp(limit) > 0 {
		limit = actual
	}
	return limit, nil
}

func ensureV1ReserveWallet(ctx context.Context, deps V1APIDeps, merchantID uuid.UUID) (*models.Wallet, error) {
	if deps.DomainRepo == nil || deps.WalletRepo == nil {
		return nil, fmt.Errorf("reserve wallet repositories are not ready")
	}
	wallet, err := deps.WalletRepo.FindReserveWallet(ctx, merchantID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		domain, createErr := deps.DomainRepo.CreateReserveDomain(ctx, merchantID)
		if createErr != nil {
			return nil, fmt.Errorf("reserve domain: %w", createErr)
		}
		wallet, err = deps.WalletRepo.CreateReserveWallet(ctx, merchantID, domain.ID, domain.HDAccountID)
	}
	if err != nil {
		return nil, err
	}
	if deps.Blockchains != nil {
		if err := deps.WalletRepo.EnsureAllAddresses(ctx, wallet.ID, deps.Blockchains); err != nil {
			return nil, err
		}
	}
	return deps.WalletRepo.FindByID(ctx, wallet.ID)
}

// ────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ────────────────────────────────────────────────────────────────────────────

func v1WalletProductID(productID string) string {
	productID = strings.TrimSpace(productID)
	if productID == "" || productID == "wallet" {
		return "wallet:default"
	}
	if strings.HasPrefix(productID, "wallet:") {
		return productID
	}
	return "wallet:" + productID
}

func v1WalletDisplayProductID(productID string) string {
	productID = strings.TrimSpace(productID)
	if productID == "wallet:default" {
		return "wallet"
	}
	if strings.HasPrefix(productID, "wallet:") {
		return strings.TrimPrefix(productID, "wallet:")
	}
	return productID
}

type v1StaticAddressScope struct {
	ChainID   constants.ChainID
	Symbol    string
	Token     string
	ProductID string
}

type v1StaticWalletProductScope struct {
	ChainID constants.ChainID
	Symbol  string
	Token   string
	Product string
}

type v1StaticWalletRepository interface {
	Create(types.WalletParams) (*models.Wallet, error)
	EnsureAllAddresses(context.Context, uuid.UUID, *blockchain.ChainFactory) error
	FindByID(context.Context, uuid.UUID) (*models.Wallet, error)
}

func v1ResolveStaticAddressScope(registry *asset.Registry, body types.V1StaticAddressRequest) (v1StaticAddressScope, error) {
	if body.ChainID == nil {
		return v1StaticAddressScope{}, fmt.Errorf("chain_id is required")
	}
	chainID := constants.ChainID(*body.ChainID)
	if !constants.IsSupportedChainID(chainID) {
		return v1StaticAddressScope{}, fmt.Errorf("unsupported chain_id")
	}
	if registry == nil {
		return v1StaticAddressScope{}, fmt.Errorf("asset registry is not configured")
	}

	symbol := strings.TrimSpace(body.Symbol)
	token := strings.TrimSpace(body.Token)
	resolved, normalizedToken, err := v1ResolveStaticAddressAsset(registry, chainID, symbol, token)
	if err != nil {
		return v1StaticAddressScope{}, err
	}
	symbol = resolved.GetSymbol()
	token = normalizedToken

	return v1StaticAddressScope{
		ChainID:   chainID,
		Symbol:    strings.ToUpper(strings.TrimSpace(symbol)),
		Token:     token,
		ProductID: v1StaticWalletProductID(body.ProductID, chainID, symbol, token),
	}, nil
}

func v1ResolveStaticAddressAsset(registry *asset.Registry, chainID constants.ChainID, symbol string, token string) (asset.Asset, string, error) {
	symbol = strings.TrimSpace(symbol)
	token = strings.TrimSpace(token)
	if token != "" {
		resolved, ok := registry.Get(chainID, token)
		if !ok {
			return nil, "", fmt.Errorf("unsupported asset for chain_id")
		}
		if symbol != "" && !strings.EqualFold(resolved.GetSymbol(), symbol) {
			return nil, "", fmt.Errorf("unsupported asset for chain_id")
		}
		if resolved.IsNative() {
			return resolved, "", nil
		}
		return resolved, registry.Normalize(resolved.GetIdentifier()), nil
	}
	if symbol == "" {
		native, ok := registry.GetNative(chainID)
		if !ok {
			return nil, "", fmt.Errorf("symbol is required")
		}
		return native, "", nil
	}
	resolved, ok := registry.GetBySymbol(chainID, symbol)
	if !ok {
		return nil, "", fmt.Errorf("unsupported asset for chain_id")
	}
	if !resolved.IsNative() {
		return nil, "", fmt.Errorf("token is required for non-native asset")
	}
	return resolved, "", nil
}

func v1StaticWalletProductID(productID string, chainID constants.ChainID, symbol string, token string) string {
	parts := []string{
		"static",
		strconv.FormatInt(int64(chainID), 10),
		strings.ToUpper(strings.TrimSpace(symbol)),
	}
	if token = v1StaticScopePart(token); token != "" {
		parts = append(parts, "token", token)
	}
	if productID = v1StaticScopePart(productID); productID != "" && productID != "static" {
		parts = append(parts, "product", productID)
	}
	return strings.Join(parts, ":")
}

func v1ValidateStaticAddressChainReady(blockchains *blockchain.ChainFactory, chainID constants.ChainID) error {
	if blockchains == nil {
		return fmt.Errorf("chain factory is not ready")
	}
	if _, err := blockchains.GetChainByID(chainID); err != nil {
		return fmt.Errorf("unsupported chain_id")
	}
	return nil
}

func v1CreateStaticAddressWallet(ctx context.Context, repo v1StaticWalletRepository, blockchains *blockchain.ChainFactory, domain *models.Domain, userID string, scope v1StaticAddressScope) (*models.Wallet, error) {
	if repo == nil {
		return nil, fmt.Errorf("wallet repository is not ready")
	}
	if domain == nil {
		return nil, fmt.Errorf("domain is required")
	}
	merchantIDStr := domain.MerchantID.String()
	domainIDStr := domain.ID.String()
	wallet, err := repo.Create(types.WalletParams{
		Context:    ctx,
		MerchantId: &merchantIDStr,
		DomainId:   &domainIDStr,
		ProductId:  &scope.ProductID,
		UserId:     &userID,
	})
	if err != nil {
		return nil, fmt.Errorf("wallet creation failed: %w", err)
	}
	if strings.TrimSpace(repositories.WalletAddressForChainID(*wallet, scope.ChainID)) == "" && blockchains != nil {
		if err := repo.EnsureAllAddresses(ctx, wallet.ID, blockchains); err != nil {
			return nil, fmt.Errorf("wallet address backfill failed: %w", err)
		}
		if refreshed, err := repo.FindByID(ctx, wallet.ID); err == nil {
			wallet = refreshed
		}
	}
	if strings.TrimSpace(repositories.WalletAddressForChainID(*wallet, scope.ChainID)) == "" {
		return nil, fmt.Errorf("wallet address unavailable for chain_id")
	}
	return wallet, nil
}

func v1StaticScopePart(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, ":", "_")
	return value
}

func v1FindDomainWallet(ctx context.Context, deps V1APIDeps, domain *models.Domain, c fiber.Ctx) (*models.Wallet, error) {
	if deps.WalletRepo == nil {
		return nil, fmt.Errorf("wallet repository is not ready")
	}
	walletIDRaw := strings.TrimSpace(c.Query("wallet_id"))
	var wallet *models.Wallet
	var err error
	if walletIDRaw != "" {
		walletID, parseErr := uuid.Parse(walletIDRaw)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid wallet_id")
		}
		wallet, err = deps.WalletRepo.FindByDomain(ctx, domain.MerchantID, domain.ID, walletID)
	} else {
		userID := strings.TrimSpace(c.Query("user_id"))
		if userID == "" {
			return nil, fmt.Errorf("wallet_id or user_id is required")
		}
		productID := v1WalletProductID(c.Query("product_id"))
		wallet, err = deps.WalletRepo.FindByDomainOwner(ctx, domain.MerchantID, domain.ID, productID, userID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			legacyProductID := strings.TrimSpace(c.Query("product_id"))
			if legacyProductID == "" {
				legacyProductID = "wallet"
			}
			wallet, err = deps.WalletRepo.FindByDomainOwner(ctx, domain.MerchantID, domain.ID, legacyProductID, userID)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("wallet not found")
	}
	if deps.Blockchains != nil {
		if err := deps.WalletRepo.EnsureAllAddresses(ctx, wallet.ID, deps.Blockchains); err != nil {
			return nil, err
		}
		if refreshed, err := deps.WalletRepo.FindByID(ctx, wallet.ID); err == nil {
			wallet = refreshed
		}
	}
	return wallet, nil
}

func v1WalletBalances(ctx context.Context, deps V1APIDeps, domain *models.Domain, wallet *models.Wallet) ([]fiber.Map, error) {
	if deps.LedgerRepo == nil {
		return []fiber.Map{}, nil
	}
	rows, err := deps.LedgerRepo.WalletBalances(ctx, domain.MerchantID, domain.ID, wallet.ID)
	if err != nil {
		return nil, err
	}
	items := make([]fiber.Map, 0, len(rows))
	for _, row := range rows {
		logoURL := ""
		if deps.AssetRegistry != nil {
			logoURL = deps.AssetRegistry.LogoURL(row.Symbol)
		}
		items = append(items, fiber.Map{
			"symbol":      row.Symbol,
			"chain":       chainLabel(constants.ChainID(row.ChainID)),
			"chain_id":    row.ChainID,
			"account":     row.Account,
			"balance":     formatV1Amount(row.BalanceRaw, row.Decimals),
			"balance_raw": row.BalanceRaw,
			"decimals":    row.Decimals,
			"logo_url":    logoURL,
		})
	}
	return items, nil
}

func v1StaticWalletListItem(w models.Wallet) fiber.Map {
	scope, ok := parseStaticWalletProductScope(w.ProductID)
	address := ""
	chain := ""
	var chainIDValue any
	if ok {
		chain = constants.ChainName(scope.ChainID)
		address = repositories.WalletAddressForChainID(w, scope.ChainID)
		chainIDValue = int64(scope.ChainID)
	}
	item := fiber.Map{
		"wallet_id":  w.ID.String(),
		"user_id":    w.UserID,
		"product_id": w.ProductID,
		"chain":      chain,
		"chain_id":   chainIDValue,
		"symbol":     scope.Symbol,
		"address":    address,
		"created_at": w.CreatedAt.UTC().Format(time.RFC3339),
	}
	if scope.Token != "" {
		item["token"] = scope.Token
	}
	return item
}

func parseStaticWalletProductID(productID string) (constants.ChainID, string, bool) {
	scope, ok := parseStaticWalletProductScope(productID)
	return scope.ChainID, scope.Symbol, ok
}

func parseStaticWalletProductScope(productID string) (v1StaticWalletProductScope, bool) {
	parts := strings.Split(strings.TrimSpace(productID), ":")
	if len(parts) < 3 || parts[0] != "static" {
		return v1StaticWalletProductScope{}, false
	}
	chainIDRaw, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return v1StaticWalletProductScope{}, false
	}
	chainID := constants.ChainID(chainIDRaw)
	if !constants.IsSupportedChainID(chainID) {
		return v1StaticWalletProductScope{}, false
	}
	scope := v1StaticWalletProductScope{
		ChainID: chainID,
		Symbol:  strings.ToUpper(strings.TrimSpace(parts[2])),
	}
	for i := 3; i+1 < len(parts); i += 2 {
		switch strings.ToLower(strings.TrimSpace(parts[i])) {
		case "token":
			scope.Token = strings.TrimSpace(parts[i+1])
		case "product":
			scope.Product = strings.TrimSpace(parts[i+1])
		}
	}
	return scope, true
}

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
	token := ""
	if s.SelectedToken != nil {
		token = *s.SelectedToken
	}
	return fiber.Map{
		"payment_id":          s.ID.String(),
		"track_id":            s.SessionToken,
		"order_id":            s.OrderID,
		"product_id":          s.ProductID,
		"user_id":             s.UserID,
		"status":              paymentSessionResponseStatus(s, time.Now()),
		"amount":              s.Amount,
		"currency":            s.Currency,
		"chain_id":            chainID,
		"symbol":              s.SelectedSymbol,
		"token":               token,
		"decimals":            s.SelectedDecimals,
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
	resp := fiber.Map{
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
	if r.DomainID != nil {
		resp["domain_id"] = r.DomainID.String()
	}
	return resp
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
		ProductID: v1WalletDisplayProductID(w.ProductID),
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

func v1StaticAddressResponse(w *models.Wallet, chainID constants.ChainID, symbol string, label string) fiber.Map {
	scope := v1StaticAddressScope{
		ChainID:   chainID,
		Symbol:    strings.ToUpper(strings.TrimSpace(symbol)),
		ProductID: w.ProductID,
	}
	if parsed, ok := parseStaticWalletProductScope(w.ProductID); ok {
		scope.Token = parsed.Token
	}
	resp, err := v1StaticAddressResponseForScope(w, scope, label)
	if err != nil {
		return fiber.Map{
			"wallet_id": w.ID.String(),
			"user_id":   w.UserID,
			"chain":     constants.ChainName(chainID),
			"symbol":    strings.ToUpper(strings.TrimSpace(symbol)),
			"address":   "",
			"label":     strings.TrimSpace(label),
		}
	}
	return resp
}

func v1StaticAddressResponseForScope(w *models.Wallet, scope v1StaticAddressScope, label string) (fiber.Map, error) {
	if w == nil {
		return nil, fmt.Errorf("wallet is required")
	}
	address := strings.TrimSpace(repositories.WalletAddressForChainID(*w, scope.ChainID))
	if address == "" {
		return nil, fmt.Errorf("wallet address unavailable for chain_id")
	}
	resp := fiber.Map{
		"wallet_id": w.ID.String(),
		"user_id":   w.UserID,
		"chain":     constants.ChainName(scope.ChainID),
		"chain_id":  int64(scope.ChainID),
		"symbol":    strings.ToUpper(strings.TrimSpace(scope.Symbol)),
		"address":   address,
		"label":     strings.TrimSpace(label),
	}
	if scope.ProductID != "" {
		resp["product_id"] = scope.ProductID
	}
	if scope.Token != "" {
		resp["token"] = scope.Token
	}
	if !w.CreatedAt.IsZero() {
		resp["created_at"] = w.CreatedAt.UTC().Format(time.RFC3339)
	}
	return resp, nil
}

func v1AssetCatalog(registry *asset.Registry) []fiber.Map {
	if registry == nil {
		return []fiber.Map{}
	}
	definitions := registry.ListDefinitions()
	items := make([]fiber.Map, 0, len(definitions))
	for _, def := range definitions {
		deployments := make([]fiber.Map, 0, len(def.Deployments))
		for _, deployment := range def.Deployments {
			if !deployment.IsEnabled() {
				continue
			}
			deploymentAsset := asset.NewDeploymentAsset(def, deployment)
			deployments = append(deployments, fiber.Map{
				"symbol":         deploymentAsset.GetSymbol(),
				"name":           deploymentAsset.GetName(),
				"type":           asset.AssetTypeName(deploymentAsset.GetType()),
				"chain":          chainLabel(deploymentAsset.GetChainID()),
				"network":        constants.ChainName(deploymentAsset.GetChainID()),
				"chain_id":       int64(deploymentAsset.GetChainID()),
				"decimals":       deploymentAsset.GetDecimals(),
				"native":         deploymentAsset.IsNative(),
				"enabled":        deployment.IsEnabled(),
				"identifier":     deploymentAsset.GetIdentifier(),
				"token_address":  asset.TokenAddress(deploymentAsset),
				"mint_address":   asset.MintAddress(deploymentAsset),
				"logo_url":       registry.LogoURL(deploymentAsset.GetSymbol()),
				"chain_logo_url": asset.ChainLogoURL(deploymentAsset.GetChainID()),
			})
		}
		items = append(items, fiber.Map{
			"symbol":      strings.ToUpper(strings.TrimSpace(def.Symbol)),
			"name":        strings.TrimSpace(def.Name),
			"type":        asset.AssetTypeName(def.Type),
			"decimals":    def.Decimals,
			"logo_url":    registry.LogoURL(def.Symbol),
			"deployments": deployments,
		})
	}
	return items
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
