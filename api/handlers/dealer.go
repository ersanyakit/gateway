package handlers

import (
	"context"
	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/helpers"
	"core/models"
	"core/repositories"
	services "core/services/system"
	"core/types"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
)

const dealerSessionCookie = "dealer_session"
const adminSessionCookie = "admin_session"
const adminPendingTOTPCookie = "admin_totp_pending" // temp: holds admin ID awaiting 2FA
const adminSetupTOTPCookie = "admin_totp_setup"    // temp: holds admin ID during TOTP setup
const oidcStateCookie = "oidc_state"
const oidcNonceCookie = "oidc_nonce"
const flashSuccessCookie = "flash_success"
const flashErrorCookie = "flash_error"

type DealerDeps struct {
	MerchantService *services.MerchantService
	DomainService   *services.DomainService
	WalletRepo      *repositories.WalletRepo
	ProductRepo     *repositories.ProductRepo
	PaymentRepo     *repositories.PaymentRepo
	WithdrawalRepo  *repositories.WithdrawalRequestRepo
	TransactionRepo *repositories.TransactionRepo
	ActivityLogRepo *repositories.ActivityLogRepo
	AdminRepo       *repositories.AdminRepo
	AssetRegistry   *asset.Registry
	Blockchains     *blockchain.ChainFactory
}

type DealerPageData struct {
	Title             string
	Active            string
	OIDCLoginURL      string
	OIDCProvider      string
	RegisterURL       string
	LoginURL          string
	OnboardingURL     string
	Error             string
	Success           string
	MerchantID        string
	MerchantName      string
	MerchantEmail     string
	DashboardURL      string
	DomainsURL        string
	LogoutURL         string
	ActivePanel       string
	TreasuryURL       string
	ActivityURL       string
	WithdrawalsURL    string
	DomainsPanelURL   string
	ProductsURL       string
	ProductsPanelURL  string
	Domains           []DealerDomainView
	Wallets           []DealerWalletView
	WithdrawalWallets []DealerWalletView
	WalletPage        DealerPaginationView
	Withdrawals       []DealerWithdrawalView
	Products          []DealerProductView
	Payments          []DealerPaymentView
	Balances          []DealerBalanceView
	ChainVaults       []DealerChainVaultView
	Activities        []DealerActivityView
	AuditLogs         []DealerAuditLogView
	WalletCount       int
	DomainCount       int
	AssetCount        int
	NetworkCount      int
	ActivityCount     int
	ProductCount      int
	PaymentCount      int
	WalletCountAll    int
	MerchantCount     int
	HasSession        bool
	Language          string
	PaymentLinkURL    string

	AdminMerchants     []DealerAdminMerchantView
	AdminWallets       []DealerWalletView
	AdminDeposits      []DealerActivityView
	AdminActivityLogs  []DealerAuditLogView

	AdminPanel          string
	AdminOverviewURL    string
	AdminMerchantsURL   string
	AdminPaymentsURL    string
	AdminDepositsURL    string
	AdminWithdrawalsURL string
	AdminWalletsURL     string
	AdminActivityURL    string
	AdminSweepURL       string
	DepositCount        int
	WithdrawalCount     int

	AdminPagination    DealerPaginationView
	AdminMerchantFilter string
}

type DealerDomainView struct {
	ID          string
	DomainURL   string
	WebhookURL  string
	APIKey      string
	HDAccountID uint32
	CreatedAt   string
}

type DealerWalletView struct {
	ID            string
	ShortID       string
	Merchant      string
	Label         string
	ProductID     string
	UserID        string
	Domain        string
	CreatedAt     string
	Addresses     []DealerAddressView
	MissingChains []DealerMissingChainView
	Balances      []DealerWalletBalanceRow
}

type DealerWalletBalanceRow struct {
	Chain    string
	Symbol   string
	Deposited string
	Locked    string
	Available string
}

type DealerMissingChainView struct {
	ChainName  string
	ChainLabel string
	WalletID   string
}

type DealerProductView struct {
	ID          string
	Name        string
	Description string
	Amount      string
	Currency    string
	Language    string
	Domain      string
	PaymentURL  string
	LogoURL     string
	LogoText    string
	SuccessURL  string
	CancelURL   string
	CreatedAt   string
}

type DealerPaymentView struct {
	ID             string
	OrderID        string
	ProductID      string
	UserID         string
	Merchant       string
	Domain         string
	Amount         string
	Currency       string
	Status         string
	CheckoutURL    string
	SelectedAsset  string
	SelectedChain  string
	DepositAddress string
	CreatedAt      string
}

type DealerAdminMerchantView struct {
	ID        string
	Name      string
	Email     string
	IsActive  bool
	CreatedAt string
}

type DealerPageURL struct {
	Page    int
	URL     string
	Active  bool
	Ellipsis bool
}

type DealerPaginationView struct {
	Page       int
	Limit      int
	Total      int64
	TotalPages int
	From       int
	To         int
	PrevURL    string
	NextURL    string
	HasPrev    bool
	HasNext    bool
	PageURLs   []DealerPageURL
}

type DealerAddressView struct {
	Chain   string
	Address string
}

type DealerWithdrawalView struct {
	ID           string
	MerchantName string
	WalletID     string
	Chain        string
	ToAddress    string
	AmountRaw    string
	Note         string
	Status       string
	TxHash       string
	Error        string
	CreatedAt    string
}

type DealerBalanceView struct {
	Chain         string
	Symbol        string
	Token         string
	AmountRaw     string
	AmountDisplay string
	Decimals      uint8
	Deposits      int64
	Users         int64
	LastDeposit   string
	DisplayToken  string
}

type DealerChainVaultView struct {
	Chain    string
	Assets   []DealerBalanceView
	Deposits int64
	Users    int64
	Empty    bool
}

type DealerActivityView struct {
	ID              string
	Type            string
	Chain           string
	Symbol          string
	AmountRaw       string
	AmountDisplay   string
	Status          string
	Hash            string
	FromAddress     string
	ToAddress       string
	ProductID       string
	UserID          string
	WebhookStatus   string
	WebhookAttempts uint
	CreatedAt       string
}

type DealerAuditLogView struct {
	ID          string
	Event       string
	Status      string
	Actor       string
	Subject     string
	Description string
	IPAddress   string
	UserAgent   string
	Method      string
	Path        string
	CreatedAt   string
	CreatedISO  string
	IsOIDC      bool
	IsFailed    bool
}

type oidcUserInfo struct {
	Sub           string       `json:"sub"`
	Email         string       `json:"email"`
	EmailVerified flexibleBool `json:"email_verified"`
	Name          string       `json:"name"`
}

type flexibleBool bool

func (b *flexibleBool) UnmarshalJSON(data []byte) error {
	value := strings.TrimSpace(string(data))
	if value == "" || value == "null" {
		*b = false
		return nil
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		value = strings.TrimSpace(unquoted)
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil
	}
	*b = flexibleBool(parsed)
	return nil
}

type oidcProviderConfig struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
}

type oidcTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

func HandleDealerHome() fiber.Handler {
	return func(c fiber.Ctx) error {
		data := dealerPageData("Crypto payment gateway", "home")
		if _, ok := requireDealerSession(c); ok {
			data.HasSession = true
		}
		return c.Render("dealer/home", data, "dealer/layout")
	}
}

// HandleDealerLogin renders the dealer OIDC login page.
// @Summary Show dealer login
// @Description Renders the hosted dealer login page with the OIDC sign-in action.
// @Tags Dealers
// @Produce html
// @Success 200 {string} string "HTML dealer login page"
// @Router /dealer/login [get]
func HandleDealerLogin() fiber.Handler {
	return func(c fiber.Ctx) error {
		data := dealerPageData("Bayi girişi", "login")
		applyFlash(c, &data)
		return c.Render("dealer/login", data, "dealer/layout")
	}
}

// HandleDealerLoginSubmit authenticates a dealer with email and password.
// @Summary Dealer email login
// @Description Authenticates a dealer with email and password, sets a dealer session cookie, and redirects to onboarding.
// @Tags Dealers
// @Accept x-www-form-urlencoded
// @Produce html
// @Param email formData string true "Dealer email"
// @Param password formData string true "Password"
// @Success 302 {string} string "Redirect to dealer onboarding"
// @Failure 400 {string} string "HTML login page with validation error"
// @Failure 401 {string} string "HTML login page with authentication error"
// @Router /dealer/login [post]
func HandleDealerLoginSubmit(service *services.MerchantService, activityRepo *repositories.ActivityLogRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		email := strings.TrimSpace(c.FormValue("email"))
		password := c.FormValue("password")
		params := types.MerchantParams{
			Context:  c.Context(),
			Email:    stringPtr(email),
			Password: stringPtr(password),
		}

		if email == "" || password == "" {
			logDealerActivity(c, activityRepo, nil, "dealer", email, "dealer.login", "failed", "auth", "", "E-posta veya şifre boş gönderildi.")
			data := dealerPageData("Bayi girişi", "login")
			data.Error = "E-posta ve şifre zorunlu."
			return c.Status(fiber.StatusBadRequest).Render("dealer/login", data, "dealer/layout")
		}

		merchant, err := service.Authenticate(params)
		if err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", email, "dealer.login", "failed", "auth", "", "E-posta veya şifre hatalı.")
			data := dealerPageData("Bayi girişi", "login")
			data.Error = "E-posta veya şifre hatalı."
			return c.Status(fiber.StatusUnauthorized).Render("dealer/login", data, "dealer/layout")
		}

		logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "dealer.login", "success", "merchant", merchant.ID.String(), "Bayi e-posta ile giriş yaptı.")
		setDealerSessionCookie(c, merchant.ID.String())
		return c.Redirect().To("/dealer/dashboard")
	}
}

// HandleDealerRegister renders the dealer self-service registration page.
// @Summary Show dealer registration
// @Description Renders the hosted self-service dealer registration page.
// @Tags Dealers
// @Produce html
// @Success 200 {string} string "HTML dealer registration page"
// @Router /dealer/register [get]
func HandleDealerRegister() fiber.Handler {
	return func(c fiber.Ctx) error {
		data := dealerPageData("Bayi kaydı", "register")
		applyFlash(c, &data)
		return c.Render("dealer/register", data, "dealer/layout")
	}
}

// HandleDealerRegisterSubmit creates a dealer merchant from the self-service HTML form.
// @Summary Create dealer from form
// @Description Creates a merchant/dealer account from the hosted self-service registration page and redirects to onboarding.
// @Tags Dealers
// @Accept x-www-form-urlencoded
// @Produce html
// @Param name formData string true "Dealer name"
// @Param email formData string true "Dealer email"
// @Param email_repeat formData string true "Dealer email confirmation"
// @Param password formData string true "Password"
// @Param password_repeat formData string true "Password confirmation"
// @Success 302 {string} string "Redirect to dealer onboarding"
// @Failure 400 {string} string "HTML registration page with validation error"
// @Failure 500 {string} string "HTML registration page with server error"
// @Router /dealer/register [post]
func HandleDealerRegisterSubmit(service *services.MerchantService, activityRepo *repositories.ActivityLogRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		name := strings.TrimSpace(c.FormValue("name"))
		email := strings.TrimSpace(c.FormValue("email"))
		emailRepeat := strings.TrimSpace(c.FormValue("email_repeat"))
		password := c.FormValue("password")
		passwordRepeat := c.FormValue("password_repeat")

		params := types.MerchantParams{
			Context:        c.Context(),
			Name:           stringPtr(name),
			Email:          stringPtr(email),
			EmailRepeat:    stringPtr(emailRepeat),
			Password:       stringPtr(password),
			PasswordRepeat: stringPtr(passwordRepeat),
		}
		if err := params.Validate(); err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", email, "dealer.register", "failed", "merchant", "", err.Error())
			data := dealerPageData("Bayi kaydı", "register")
			data.Error = err.Error()
			return c.Status(fiber.StatusBadRequest).Render("dealer/register", data, "dealer/layout")
		}

		merchant, err := service.Create(params)
		if err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", email, "dealer.register", "failed", "merchant", "", err.Error())
			data := dealerPageData("Bayi kaydı", "register")
			data.Error = "Bayi kaydı oluşturulamadı: " + err.Error()
			return c.Status(fiber.StatusInternalServerError).Render("dealer/register", data, "dealer/layout")
		}

		logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "dealer.register", "success", "merchant", merchant.ID.String(), "Bayi hesabı self servis kayıt ile oluşturuldu.")
		setDealerSessionCookie(c, merchant.ID.String())
		return redirectWithSuccess(c, "/dealer/dashboard", "Bayi hesabı oluşturuldu.")
	}
}

// HandleDealerDashboard renders the authenticated dealer panel.
// @Summary Show dealer dashboard
// @Description Renders the authenticated dealer panel with merchant info, domain creation form, and current domains.
// @Tags Dealers
// @Produce html
// @Success 200 {string} string "HTML dealer dashboard"
// @Failure 302 {string} string "Redirect to dealer login"
// @Router /dealer/dashboard [get]
func HandleDealerDashboard(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}

		domains, err := deps.DomainService.ListByMerchant(c.Context(), merchant.ID)
		if err != nil {
			data := dealerPageData("Bayi paneli", "dashboard")
			fillDealerMerchant(&data, merchant)
			data.Error = "Domain listesi okunamadı: " + err.Error()
			return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
		}
		withdrawals, err := deps.WithdrawalRepo.ListByMerchant(c.Context(), merchant.ID, 100)
		if err != nil {
			data := dealerPageData("Bayi paneli", "dashboard")
			fillDealerMerchant(&data, merchant)
			data.Error = "Çekim talepleri okunamadı: " + err.Error()
			return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
		}
		transactions, err := deps.TransactionRepo.ListByMerchant(c.Context(), merchant.ID, 100)
		if err != nil {
			data := dealerPageData("Bayi paneli", "dashboard")
			fillDealerMerchant(&data, merchant)
			data.Error = "İşlem geçmişi okunamadı: " + err.Error()
			return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
		}
		var products []models.Product
		if deps.ProductRepo != nil {
			products, err = deps.ProductRepo.ListByMerchant(c.Context(), merchant.ID, 100)
			if err != nil {
				data := dealerPageData("Bayi paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Ürün listesi okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		var payments []models.PaymentSession
		if deps.PaymentRepo != nil {
			payments, err = deps.PaymentRepo.ListByMerchant(c.Context(), merchant.ID, 100)
			if err != nil {
				data := dealerPageData("Bayi paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Checkout listesi okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		balances, err := deps.TransactionRepo.MerchantDepositSummary(c.Context(), merchant.ID)
		if err != nil {
			data := dealerPageData("Bayi paneli", "dashboard")
			fillDealerMerchant(&data, merchant)
			data.Error = "Bakiye özeti okunamadı: " + err.Error()
			return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
		}
		var auditLogs []models.ActivityLog
		if deps.ActivityLogRepo != nil {
			auditLogs, err = deps.ActivityLogRepo.ListByMerchant(c.Context(), merchant.ID, 100)
			if err != nil {
				data := dealerPageData("Bayi paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Activity log okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}

		wallets, _ := deps.WalletRepo.ListByMerchant(c.Context(), merchant.ID, 200)

		data := dealerPageData("Bayi paneli", "dashboard")
		fillDealerMerchant(&data, merchant)
		data.ActivePanel = currentDashboardPanel(c)
		applyFlash(c, &data)
		data.Domains = dealerDomainViews(domains)
		data.Withdrawals = dealerWithdrawalViews(withdrawals)
		data.Products = dealerProductViews(c, products)
		data.Payments = dealerPaymentViews(c, payments)
		data.Balances = dealerBalanceViews(balances)
		data.Balances = dealerAllBalanceViews(deps.AssetRegistry, data.Balances)
		data.ChainVaults = dealerChainVaultViews(data.Balances)
		data.Activities = dealerActivityViews(transactions)
		data.AuditLogs = dealerAuditLogViews(auditLogs)
		data.Wallets = dealerWalletViews(wallets)
		data.WithdrawalWallets = data.Wallets
		data.WalletCount = len(wallets)
		data.DomainCount = len(domains)
		data.ProductCount = len(data.Products)
		data.PaymentCount = len(data.Payments)
		data.AssetCount = len(data.Balances)
		data.NetworkCount = len(data.ChainVaults)
		data.ActivityCount = len(data.AuditLogs) + len(transactions)
		return c.Render("dealer/dashboard", data, "dealer/layout")
	}
}

// HandleDealerDomainCreate creates a domain from the authenticated dealer panel.
// @Summary Create dealer domain from panel
// @Description Creates a merchant domain using the authenticated dealer session and redirects back to the dashboard.
// @Tags Dealers
// @Accept x-www-form-urlencoded
// @Produce html
// @Param domain_url formData string true "Domain URL"
// @Param webhook_url formData string true "Webhook URL"
// @Param webhook_secret formData string true "Webhook secret"
// @Success 302 {string} string "Redirect to dealer dashboard"
// @Failure 302 {string} string "Redirect to dealer login or dashboard with error"
// @Router /dealer/domains [post]
func HandleDealerDomainCreate(merchantService *services.MerchantService, domainService *services.DomainService, activityRepo *repositories.ActivityLogRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, merchantService)
		if !ok {
			return redirectDealerLogin(c)
		}

		domainURL := strings.TrimSpace(c.FormValue("domain_url"))
		webhookURL := strings.TrimSpace(c.FormValue("webhook_url"))
		webhookSecret := strings.TrimSpace(c.FormValue("webhook_secret"))
		merchantID := merchant.ID.String()
		params := types.DomainParams{
			Context:       c.Context(),
			MerchantID:    &merchantID,
			DomainURL:     &domainURL,
			WebhookURL:    &webhookURL,
			WebhookSecret: &webhookSecret,
		}
		if err := params.Validate(); err != nil {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "failed", "domain", domainURL, err.Error())
			return redirectWithError(c, "/dealer/dashboard", err.Error())
		}
		if err := helpers.ValidateWebhookURL(webhookURL); err != nil {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "failed", "domain", domainURL, err.Error())
			return redirectWithError(c, "/dealer/dashboard", "Geçersiz webhook URL: "+err.Error())
		}
		domain, err := domainService.Create(params)
		if err != nil {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "failed", "domain", domainURL, err.Error())
			return redirectWithError(c, "/dealer/dashboard", "Domain eklenemedi: "+err.Error())
		}
		subjectID := domainURL
		if domain != nil {
			subjectID = domain.ID.String()
		}
		logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "success", "domain", subjectID, "Domain ve webhook endpoint oluşturuldu.")
		return redirectWithSuccess(c, "/dealer/dashboard", "Domain eklendi.")
	}
}

func HandleDealerProductCreate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}
		if deps.ProductRepo == nil {
			return redirectWithError(c, "/dealer/dashboard/products", "Product repository hazır değil.")
		}

		domainIDRaw := strings.TrimSpace(c.FormValue("domain_id"))
		domainID, err := uuid.Parse(domainIDRaw)
		if err != nil {
			return redirectWithError(c, "/dealer/dashboard/products", "Geçerli domain seçmelisin.")
		}
		domainIDString := domainID.String()
		domain, err := deps.DomainService.FindByID(types.DomainParams{
			Context:  c.Context(),
			DomainID: &domainIDString,
		})
		if err != nil || domain.MerchantID != merchant.ID {
			return redirectWithError(c, "/dealer/dashboard/products", "Domain bulunamadı.")
		}

		name := strings.TrimSpace(c.FormValue("name"))
		description := strings.TrimSpace(c.FormValue("description"))
		amount := strings.TrimSpace(c.FormValue("amount"))
		currency := strings.ToUpper(strings.TrimSpace(c.FormValue("currency")))
		language := normalizeLanguage(c.FormValue("language"))
		successURL := strings.TrimSpace(c.FormValue("success_url"))
		cancelURL := strings.TrimSpace(c.FormValue("cancel_url"))
		logoURL := strings.TrimSpace(c.FormValue("logo_url"))
		if name == "" {
			return redirectWithError(c, "/dealer/dashboard/products", "Ürün adı zorunlu.")
		}
		if err := types.ValidatePositiveDecimal(amount); err != nil {
			return redirectWithError(c, "/dealer/dashboard/products", "Tutar pozitif decimal olmalı.")
		}
		if currency == "" {
			currency = "USD"
		}

		product := &models.Product{
			MerchantID:  merchant.ID,
			DomainID:    domain.ID,
			Name:        name,
			Description: description,
			Amount:      amount,
			Currency:    currency,
			Language:    language,
			SuccessURL:  successURL,
			CancelURL:   cancelURL,
			LogoURL:     logoURL,
			IsActive:    true,
		}
		if err := deps.ProductRepo.Create(c.Context(), product); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "product.create", "failed", "product", name, err.Error())
			return redirectWithError(c, "/dealer/dashboard/products", "Ürün oluşturulamadı: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "product.create", "success", "product", product.ID.String(), "Payment link ürünü oluşturuldu.")
		link := baseURL(c) + "/payment-links/" + product.LinkToken
		return redirectWithSuccess(c, "/dealer/dashboard/products", "Payment link oluşturuldu: "+link)
	}
}

func HandlePaymentLink(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if deps.ProductRepo == nil || deps.PaymentRepo == nil || deps.WalletRepo == nil {
			return renderPaymentLinkError(c, "Payment link altyapısı hazır değil.")
		}
		product, err := deps.ProductRepo.FindByToken(c.Context(), c.Params("token"))
		if err != nil {
			return renderPaymentLinkError(c, "Payment link bulunamadı.")
		}
		language := normalizeLanguage(product.Language)
		if requestedLang := strings.TrimSpace(c.Query("lang")); requestedLang != "" {
			language = normalizeLanguage(requestedLang)
		}
		c.Cookie(&fiber.Cookie{
			Name:     "gateway_lang",
			Value:    language,
			Path:     "/",
			HTTPOnly: true,
			SameSite: "Lax",
			MaxAge:   60 * 60 * 24 * 365,
		})

		orderID := "plink-" + product.ID.String() + "-" + uuid.NewString()
		userID := valueOrDefault(stringPtr(strings.TrimSpace(c.Query("user_id"))), "guest")
		merchantID := product.MerchantID.String()
		domainID := product.DomainID.String()
		productID := product.ID.String()
		wallet, err := deps.WalletRepo.Create(types.WalletParams{
			Context:    c.Context(),
			MerchantId: &merchantID,
			DomainId:   &domainID,
			ProductId:  &productID,
			UserId:     &userID,
		})
		if err != nil {
			return renderPaymentLinkError(c, "Wallet oluşturulamadı: "+err.Error())
		}

		expiresAt := time.Now().Add(paymentSessionTTL())
		session := &models.PaymentSession{
			MerchantID: product.MerchantID,
			DomainID:   product.DomainID,
			WalletID:   wallet.ID,
			OrderID:    orderID,
			ProductID:  productID,
			UserID:     userID,
			Amount:     product.Amount,
			Currency:   product.Currency,
			SuccessURL: product.SuccessURL,
			CancelURL:  product.CancelURL,
			Status:     models.PaymentStatusPending,
			ExpiresAt:  &expiresAt,
		}
		if err := deps.PaymentRepo.Create(c.Context(), session); err != nil {
			return renderPaymentLinkError(c, "Checkout oluşturulamadı: "+err.Error())
		}
		return c.Redirect().To("/checkout/" + session.SessionToken + "?lang=" + url.QueryEscape(language))
	}
}

func HandleDealerFillWalletAddress(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}

		walletID, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/dealer/dashboard/treasury", "Geçersiz wallet.")
		}

		chain := strings.ToLower(strings.TrimSpace(c.FormValue("chain")))
		if chain == "" {
			return redirectWithError(c, "/dealer/dashboard/treasury", "Chain belirtilmeli.")
		}

		wallet, err := deps.WalletRepo.FindByID(c.Context(), walletID)
		if err != nil || wallet.MerchantID != merchant.ID {
			return redirectWithError(c, "/dealer/dashboard/treasury", "Wallet bulunamadı.")
		}

		_, err = deps.WalletRepo.FillChainAddress(c.Context(), walletID, chain, deps.Blockchains)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "wallet.fill_address", "failed", "wallet", walletID.String(), err.Error())
			return redirectWithError(c, "/dealer/dashboard/treasury", "Adres türetilemedi: "+err.Error())
		}

		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "wallet.fill_address", "success", "wallet", walletID.String(), chain+" adresi oluşturuldu.")
		return redirectWithSuccess(c, "/dealer/dashboard/treasury", chain+" adresi oluşturuldu.")
	}
}

func HandleDealerWithdrawalCreate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}

		walletIDRaw := strings.TrimSpace(c.FormValue("wallet_id"))
		walletID, err := uuid.Parse(walletIDRaw)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", "", "Geçersiz wallet ile çekim talebi denendi.")
			return redirectWithError(c, "/dealer/dashboard", "Geçerli wallet seçmelisin.")
		}
		wallet, err := deps.WalletRepo.FindByID(c.Context(), walletID)
		if err != nil || wallet.MerchantID != merchant.ID {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, "Wallet bulunamadı veya merchant ile eşleşmedi.")
			return redirectWithError(c, "/dealer/dashboard", "Wallet bulunamadı.")
		}

		chain := strings.ToLower(strings.TrimSpace(c.FormValue("chain")))
		toAddress := strings.TrimSpace(c.FormValue("to_address"))
		amountRaw := strings.TrimSpace(c.FormValue("amount_raw"))
		note := strings.TrimSpace(c.FormValue("note"))
		params := types.TransferParams{
			Context:   c.Context(),
			WalletID:  &walletIDRaw,
			Chain:     &chain,
			ToAddress: &toAddress,
			AmountRaw: &amountRaw,
		}
		if err := params.ValidateWithdraw(); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, err.Error())
			return redirectWithError(c, "/dealer/dashboard", err.Error())
		}

		request := &models.WithdrawalRequest{
			MerchantID:  merchant.ID,
			WalletID:    wallet.ID,
			Chain:       *params.Chain,
			ToAddress:   *params.ToAddress,
			AmountRaw:   *params.AmountRaw,
			Note:        note,
			Status:      models.WithdrawalStatusPending,
			RequestedBy: merchant.Email,
		}
		if err := deps.WithdrawalRepo.Create(c.Context(), request); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, err.Error())
			return redirectWithError(c, "/dealer/dashboard", "Çekim talebi oluşturulamadı: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "success", "withdrawal", request.ID.String(), "Çekim talebi admin onayına gönderildi.")
		return redirectWithSuccess(c, "/dealer/dashboard", "Çekim talebi admin onayına gönderildi.")
	}
}

// HandleDealerOnboarding renders the dealer onboarding result page.
// @Summary Show dealer onboarding
// @Description Renders the hosted onboarding page after a dealer merchant is created.
// @Tags Dealers
// @Produce html
// @Param merchant_id query string false "Merchant ID"
// @Param name query string false "Dealer name"
// @Param email query string false "Dealer email"
// @Success 200 {string} string "HTML dealer onboarding page"
// @Router /dealer/onboarding [get]
func HandleDealerOnboarding() fiber.Handler {
	return func(c fiber.Ctx) error {
		data := dealerPageData("Bayi hesabı oluşturuldu", "register")
		data.MerchantID = c.Query("merchant_id")
		data.MerchantName = c.Query("name")
		data.MerchantEmail = c.Query("email")
		return c.Render("dealer/onboarding", data, "dealer/layout")
	}
}

func HandleDealerLogout(service *services.MerchantService, activityRepo *repositories.ActivityLogRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		if merchant, ok := requireDealerMerchant(c, service); ok {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "dealer.logout", "success", "merchant", merchant.ID.String(), "Bayi oturumu kapattı.")
		}
		clearDealerSessionCookie(c)
		return redirectWithSuccess(c, "/dealer/login", "Oturum kapatıldı.")
	}
}

func HandleAdminLogin() fiber.Handler {
	return func(c fiber.Ctx) error {
		data := dealerPageData("Admin girişi", "admin-login")
		applyFlash(c, &data)
		return c.Render("dealer/admin_login", data, "dealer/layout")
	}
}

func HandleAdminLoginSubmit(adminRepo *repositories.AdminRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		email := strings.TrimSpace(c.FormValue("email"))
		password := c.FormValue("password")

		renderErr := func(msg string) error {
			data := dealerPageData("Admin girişi", "admin-login")
			data.Error = msg
			return c.Status(fiber.StatusUnauthorized).Render("dealer/admin_login", data, "dealer/layout")
		}

		admin, err := adminRepo.Authenticate(c.Context(), email, password)
		if err != nil {
			// Fallback to env-based credentials for backward compat.
			if !verifyAdminCredentials(email, password) {
				return renderErr("Admin bilgileri hatalı.")
			}
			// Env-based login — no 2FA, issue session directly.
			setAdminSessionCookie(c, email)
			return c.Redirect().To("/admin")
		}

		if admin.TOTPEnabled {
			// TOTP required — store pending admin ID, redirect to verify page.
			val := signedDealerSessionValue(admin.ID.String())
			c.Cookie(&fiber.Cookie{
				Name:     adminPendingTOTPCookie,
				Value:    val,
				HTTPOnly: true,
				SameSite: "Lax",
				MaxAge:   300,
			})
			return c.Redirect().To("/admin/2fa/verify")
		}

		// TOTP not set up yet — redirect to setup.
		val := signedDealerSessionValue(admin.ID.String())
		c.Cookie(&fiber.Cookie{
			Name:     adminSetupTOTPCookie,
			Value:    val,
			HTTPOnly: true,
			SameSite: "Lax",
			MaxAge:   600,
		})
		return c.Redirect().To("/admin/2fa/setup")
	}
}

func HandleAdminTOTPSetup(adminRepo *repositories.AdminRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminID, ok := verifyAdminTempCookie(c, adminSetupTOTPCookie)
		if !ok {
			return redirectWithError(c, "/admin/login", "Oturum süresi doldu.")
		}
		admin, err := adminRepo.FindByID(c.Context(), adminID)
		if err != nil {
			return redirectWithError(c, "/admin/login", "Admin bulunamadı.")
		}

		// Generate a new TOTP secret if one doesn't exist.
		secret := admin.TOTPSecret
		if secret == "" {
			key, err := totp.Generate(totp.GenerateOpts{
				Issuer:      "Gateway Admin",
				AccountName: admin.Email,
			})
			if err != nil {
				return redirectWithError(c, "/admin/login", "2FA anahtar oluşturulamadı.")
			}
			secret = key.Secret()
			// Save provisionally (not enabled yet until user confirms code).
			_ = adminRepo.SaveTOTPSecret(c.Context(), adminID, secret)
		}

		qrURL := fmt.Sprintf(
			"otpauth://totp/Gateway%%20Admin:%s?secret=%s&issuer=Gateway%%20Admin",
			url.QueryEscape(admin.Email), secret,
		)
		data := dealerPageData("2FA kurulum", "admin-2fa-setup")
		data.Success = qrURL
		data.MerchantEmail = admin.Email
		return c.Render("dealer/admin_2fa_setup", data, "dealer/layout")
	}
}

func HandleAdminTOTPSetupSubmit(adminRepo *repositories.AdminRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminID, ok := verifyAdminTempCookie(c, adminSetupTOTPCookie)
		if !ok {
			return redirectWithError(c, "/admin/login", "Oturum süresi doldu.")
		}
		code := strings.TrimSpace(c.FormValue("code"))
		admin, err := adminRepo.FindByID(c.Context(), adminID)
		if err != nil {
			return redirectWithError(c, "/admin/login", "Admin bulunamadı.")
		}
		if !totp.Validate(code, admin.TOTPSecret) {
			data := dealerPageData("2FA kurulum", "admin-2fa-setup")
			data.Error = "Kod hatalı. Lütfen tekrar deneyin."
			data.MerchantEmail = admin.Email
			qrURL := fmt.Sprintf(
				"otpauth://totp/Gateway%%20Admin:%s?secret=%s&issuer=Gateway%%20Admin",
				url.QueryEscape(admin.Email), admin.TOTPSecret,
			)
			data.Success = qrURL
			return c.Render("dealer/admin_2fa_setup", data, "dealer/layout")
		}
		// Enable TOTP.
		_ = adminRepo.SaveTOTPSecret(c.Context(), adminID, admin.TOTPSecret)
		// Clear setup cookie, issue full session.
		c.Cookie(&fiber.Cookie{Name: adminSetupTOTPCookie, Value: "", MaxAge: -1, HTTPOnly: true})
		setAdminSessionCookie(c, admin.Email)
		return redirectWithSuccess(c, "/admin", "2FA başarıyla etkinleştirildi.")
	}
}

func HandleAdminTOTPVerify(adminRepo *repositories.AdminRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, ok := verifyAdminTempCookie(c, adminPendingTOTPCookie)
		if !ok {
			return redirectWithError(c, "/admin/login", "Oturum süresi doldu.")
		}
		data := dealerPageData("2FA doğrulama", "admin-2fa-verify")
		applyFlash(c, &data)
		return c.Render("dealer/admin_2fa_verify", data, "dealer/layout")
	}
}

func HandleAdminTOTPVerifySubmit(adminRepo *repositories.AdminRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminID, ok := verifyAdminTempCookie(c, adminPendingTOTPCookie)
		if !ok {
			return redirectWithError(c, "/admin/login", "Oturum süresi doldu.")
		}
		code := strings.TrimSpace(c.FormValue("code"))
		admin, err := adminRepo.FindByID(c.Context(), adminID)
		if err != nil {
			return redirectWithError(c, "/admin/login", "Admin bulunamadı.")
		}
		if !totp.Validate(code, admin.TOTPSecret) {
			return redirectWithError(c, "/admin/2fa/verify", "Kod hatalı. Lütfen tekrar deneyin.")
		}
		c.Cookie(&fiber.Cookie{Name: adminPendingTOTPCookie, Value: "", MaxAge: -1, HTTPOnly: true})
		setAdminSessionCookie(c, admin.Email)
		return c.Redirect().To("/admin")
	}
}

// HandleAdminManageAdmins shows admin list and create form.
func HandleAdminManageAdmins(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		admins, _ := deps.AdminRepo.List(c.Context())
		data := adminPageData("", "admins")
		data.AdminPanel = "admins"
		data.AdminMerchants = adminListToMerchantViews(admins)
		applyFlash(c, &data)
		return c.Render("dealer/admin_dashboard", data, "dealer/layout")
	}
}

func HandleAdminCreateAdmin(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		email := strings.TrimSpace(c.FormValue("email"))
		name := strings.TrimSpace(c.FormValue("name"))
		password := c.FormValue("password")
		if email == "" || password == "" {
			return redirectWithError(c, "/admin/admins", "E-posta ve şifre zorunlu.")
		}
		if _, err := deps.AdminRepo.Create(c.Context(), email, name, password); err != nil {
			return redirectWithError(c, "/admin/admins", "Admin oluşturulamadı: "+err.Error())
		}
		return redirectWithSuccess(c, "/admin/admins", "Admin hesabı oluşturuldu.")
	}
}

func HandleAdminToggleAdmin(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/admins", "Geçersiz ID.")
		}
		admin, err := deps.AdminRepo.FindByID(c.Context(), id)
		if err != nil {
			return redirectWithError(c, "/admin/admins", "Admin bulunamadı.")
		}
		_ = deps.AdminRepo.SetActive(c.Context(), id, !admin.IsActive)
		return redirectWithSuccess(c, "/admin/admins", "Admin durumu güncellendi.")
	}
}

func HandleAdminResetTOTP(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/admins", "Geçersiz ID.")
		}
		// Clear TOTP so next login triggers re-setup.
		deps.AdminRepo.DB().WithContext(c.Context()).Model(&models.Admin{}).
			Where("id = ?", id).
			Updates(map[string]any{"totp_secret": "", "totp_enabled": false})
		return redirectWithSuccess(c, "/admin/admins", "2FA sıfırlandı. Sonraki girişte yeniden kurulacak.")
	}
}

// verifyAdminTempCookie decrypts a temporary admin cookie returning the admin UUID.
func verifyAdminTempCookie(c fiber.Ctx, cookieName string) (uuid.UUID, bool) {
	val, err := verifyDealerSessionValue(c.Cookies(cookieName))
	if err != nil || val == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(val)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// adminListToMerchantViews repurposes DealerAdminMerchantView for the admin accounts list.
func adminListToMerchantViews(admins []models.Admin) []DealerAdminMerchantView {
	views := make([]DealerAdminMerchantView, 0, len(admins))
	for _, a := range admins {
		views = append(views, DealerAdminMerchantView{
			ID:        a.ID.String(),
			Name:      a.Name,
			Email:     a.Email,
			IsActive:  a.IsActive,
			CreatedAt: formatPanelTime(a.CreatedAt),
		})
	}
	return views
}

func HandleAdminDashboard(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}

		panel := currentAdminPanel(c)
		data := adminPageData(adminEmail, panel)
		applyFlash(c, &data)

		page := parseQueryInt(c.Query("page"), 1)
		limit := parseQueryInt(c.Query("limit"), 50)
		if page < 1 {
			page = 1
		}
		if limit < 1 || limit > 200 {
			limit = 50
		}

		// Total counts for header stats.
		var merchantTotal, paymentTotal, depositTotal, withdrawalTotal, walletTotal, activityTotal int64
		deps.MerchantService.Repo().DB().WithContext(c.Context()).Model(&models.Merchant{}).Where("deleted_at IS NULL").Count(&merchantTotal)
		deps.PaymentRepo.CountAll(c.Context(), &paymentTotal)
		deps.TransactionRepo.DB().WithContext(c.Context()).Model(&models.Transaction{}).Count(&depositTotal)
		deps.WithdrawalRepo.DB().WithContext(c.Context()).Model(&models.WithdrawalRequest{}).Count(&withdrawalTotal)
		deps.WalletRepo.DB().WithContext(c.Context()).Model(&models.Wallet{}).Count(&walletTotal)
		deps.ActivityLogRepo.DB().WithContext(c.Context()).Model(&models.ActivityLog{}).Count(&activityTotal)
		data.MerchantCount = int(merchantTotal)
		data.PaymentCount = int(paymentTotal)
		data.DepositCount = int(depositTotal)
		data.WithdrawalCount = int(withdrawalTotal)
		data.WalletCountAll = int(walletTotal)
		data.ActivityCount = int(activityTotal)

		switch panel {
		case "merchants":
			rows, total, _ := deps.MerchantService.Repo().ListPage(c.Context(), page, limit)
			data.AdminMerchants = dealerAdminMerchantViews(rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/merchants")

		case "payments":
			rows, total, _ := deps.PaymentRepo.ListPage(c.Context(), page, limit)
			data.Payments = dealerPaymentViews(c, rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/payments")

		case "deposits":
			rows, total, _ := deps.TransactionRepo.ListPage(c.Context(), page, limit)
			data.AdminDeposits = dealerActivityViews(rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/deposits")

		case "withdrawals":
			rows, total, _ := deps.WithdrawalRepo.ListPage(c.Context(), page, limit)
			data.Withdrawals = dealerWithdrawalViews(rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/withdrawals")

		case "wallets":
			rows, total, _ := deps.WalletRepo.ListPage(c.Context(), page, limit)
			balanceMap := buildWalletBalanceMap(c.Context(), deps.TransactionRepo)
			data.AdminWallets = dealerWalletViewsWithBalances(rows, balanceMap)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/wallets")

		case "activity":
			merchantFilter := strings.TrimSpace(c.Query("merchant_id"))
			data.AdminMerchantFilter = merchantFilter
			var mID *uuid.UUID
			if merchantFilter != "" {
				if parsed, err := uuid.Parse(merchantFilter); err == nil {
					mID = &parsed
				}
			}
			rows, total, _ := deps.ActivityLogRepo.ListPage(c.Context(), page, limit, mID)
			data.AdminActivityLogs = dealerAuditLogViews(rows)
			merchants, _ := deps.MerchantService.List(c.Context(), 500)
			data.AdminMerchants = dealerAdminMerchantViews(merchants)
			activityBase := "/admin/activity"
			if merchantFilter != "" {
				activityBase += "?merchant_id=" + merchantFilter
			}
			data.AdminPagination = dealerPaginationView(page, limit, total, activityBase)

		case "sweep":
			wallets, _ := deps.WalletRepo.List(c.Context(), 200)
			data.WithdrawalWallets = dealerWalletViews(wallets)

		default: // overview
			recentRows, _, _ := deps.TransactionRepo.ListPage(c.Context(), 1, 8)
			data.AdminDeposits = dealerActivityViews(recentRows)
		}

		return c.Render("dealer/admin_dashboard", data, "dealer/layout")
	}
}

func HandleAdminMerchantToggle(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, ok := requireAdmin(c)
		if !ok {
			return c.Status(403).SendString("unauthorized")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/merchants", "Geçersiz merchant ID.")
		}
		merchants, _ := deps.MerchantService.List(c.Context(), 1000)
		for _, m := range merchants {
			if m.ID == id {
				newActive := !m.IsActive
				if err := deps.MerchantService.Repo().SetActive(c.Context(), id, newActive); err != nil {
					return redirectWithError(c, "/admin/merchants", "Güncelleme başarısız: "+err.Error())
				}
				status := "aktifleştirildi"
				if !newActive {
					status = "pasif edildi"
				}
				return redirectWithSuccess(c, "/admin/merchants", m.Name+" "+status+".")
			}
		}
		return redirectWithError(c, "/admin/merchants", "Merchant bulunamadı.")
	}
}

func HandleAdminSweep(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		walletID := strings.TrimSpace(c.FormValue("wallet_id"))
		chain := strings.TrimSpace(c.FormValue("chain"))
		toAddress := strings.TrimSpace(c.FormValue("to_address"))
		amountRaw := strings.TrimSpace(c.FormValue("amount_raw"))
		isSweep := amountRaw == "" || amountRaw == "0"

		if walletID == "" || chain == "" || toAddress == "" {
			return redirectWithError(c, "/admin/sweep", "Wallet, chain ve hedef adres zorunlu.")
		}

		params := types.TransferParams{
			Context:   c.Context(),
			WalletID:  &walletID,
			Chain:     &chain,
			ToAddress: &toAddress,
		}
		if !isSweep {
			params.AmountRaw = &amountRaw
		}

		if isSweep {
			if err := params.ValidateSweep(); err != nil {
				return redirectWithError(c, "/admin/sweep", err.Error())
			}
		} else {
			if err := params.ValidateWithdraw(); err != nil {
				return redirectWithError(c, "/admin/sweep", err.Error())
			}
		}

		result, err := ExecuteWalletTransfer(deps.WalletRepo, deps.Blockchains, params, isSweep)
		if err != nil {
			if deps.ActivityLogRepo != nil {
				logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.sweep", "failed", "wallet", walletID, err.Error())
			}
			return redirectWithError(c, "/admin/sweep", "Transfer başarısız: "+err.Error())
		}
		if deps.ActivityLogRepo != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.sweep", "success", "wallet", walletID, "Tx: "+result.TxHash)
		}
		return redirectWithSuccess(c, "/admin/sweep", "Transfer gönderildi. Tx: "+result.TxHash)
	}
}

func HandleAdminWithdrawalApprove(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/withdrawals", "Geçersiz talep.")
		}
		request, err := deps.WithdrawalRepo.Find(c.Context(), id)
		if err != nil || request.Status != models.WithdrawalStatusPending {
			return redirectWithError(c, "/admin/withdrawals", "Pending talep bulunamadı.")
		}
		walletID := request.WalletID.String()
		params := types.TransferParams{
			Context:   c.Context(),
			WalletID:  &walletID,
			Chain:     &request.Chain,
			ToAddress: &request.ToAddress,
			AmountRaw: &request.AmountRaw,
		}
		if err := params.ValidateWithdraw(); err != nil {
			_ = deps.WithdrawalRepo.MarkFailed(c.Context(), id, adminEmail, err.Error())
			return redirectWithError(c, "/admin/withdrawals", err.Error())
		}
		result, err := ExecuteWalletTransfer(deps.WalletRepo, deps.Blockchains, params, false)
		if err != nil {
			_ = deps.WithdrawalRepo.MarkFailed(c.Context(), id, adminEmail, err.Error())
			return redirectWithError(c, "/admin/withdrawals", "Transfer başarısız: "+err.Error())
		}
		if err := deps.WithdrawalRepo.MarkApproved(c.Context(), id, adminEmail, result.TxHash); err != nil {
			return redirectWithError(c, "/admin/withdrawals", "Talep güncellenemedi: "+err.Error())
		}
		return redirectWithSuccess(c, "/admin/withdrawals", "Çekim onaylandı ve transfer gönderildi.")
	}
}

func HandleAdminWithdrawalReject(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/withdrawals", "Geçersiz talep.")
		}
		reason := strings.TrimSpace(c.FormValue("reason"))
		if reason == "" {
			reason = "Admin tarafından reddedildi."
		}
		if err := deps.WithdrawalRepo.MarkRejected(c.Context(), id, adminEmail, reason); err != nil {
			return redirectWithError(c, "/admin/withdrawals", "Talep reddedilemedi: "+err.Error())
		}
		return redirectWithSuccess(c, "/admin/withdrawals", "Çekim talebi reddedildi.")
	}
}

func HandleAdminLogout() fiber.Handler {
	return func(c fiber.Ctx) error {
		clearAdminSessionCookie(c)
		return redirectWithSuccess(c, "/admin/login", "Admin oturumu kapatıldı.")
	}
}

// HandleOIDCLogin starts the OIDC authorization-code flow.
// @Summary Start dealer OIDC login
// @Description Redirects the dealer to the configured OIDC authorization URL.
// @Tags Dealers
// @Produce html
// @Success 302 {string} string "Redirect to OIDC provider"
// @Failure 501 {string} string "HTML page explaining missing OIDC configuration"
// @Router /auth/oidc/login [get]
func HandleOIDCLogin() fiber.Handler {
	return func(c fiber.Ctx) error {
		authURL, _, _, err := oidcEndpoints()
		clientID := strings.TrimSpace(os.Getenv("OIDC_CLIENT_ID"))
		redirectURI := strings.TrimSpace(os.Getenv("OIDC_REDIRECT_URI"))
		if err != nil || clientID == "" || redirectURI == "" {
			data := dealerPageData("OIDC yapılandırması eksik", "login")
			data.Error = "OIDC_AUTHORITY/OIDC_AUTH_URL, OIDC_CLIENT_ID veya OIDC_REDIRECT_URI eksik."
			return c.Status(fiber.StatusNotImplemented).Render("dealer/oidc_missing", data, "dealer/layout")
		}
		state := uuid.NewString()
		nonce := uuid.NewString()
		setOIDCCookie(c, oidcStateCookie, state)
		setOIDCCookie(c, oidcNonceCookie, nonce)
		q := url.Values{}
		q.Set("client_id", clientID)
		q.Set("redirect_uri", redirectURI)
		q.Set("response_type", "code")
		q.Set("scope", oidcScopes())
		q.Set("state", state)
		q.Set("nonce", nonce)
		return c.Redirect().To(authURL + "?" + q.Encode())
	}
}

// HandleOIDCCallback completes the OIDC authorization-code flow and opens a dealer session.
// @Summary Complete dealer OIDC login
// @Description Exchanges the OIDC authorization code for tokens, fetches userinfo, and signs the dealer in.
// @Tags Dealers
// @Produce html
// @Param code query string true "Authorization code"
// @Param state query string true "OIDC state"
// @Success 302 {string} string "Redirect to dealer dashboard"
// @Failure 400 {string} string "Redirect to dealer login with error"
// @Router /auth/oidc/callback [get]
func HandleOIDCCallback(service *services.MerchantService, activityRepo *repositories.ActivityLogRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		code := strings.TrimSpace(c.Query("code"))
		state := strings.TrimSpace(c.Query("state"))
		expectedState := strings.TrimSpace(c.Cookies(oidcStateCookie))
		clearOIDCCookie(c, oidcStateCookie)
		clearOIDCCookie(c, oidcNonceCookie)
		if code == "" || state == "" || expectedState == "" || !hmac.Equal([]byte(state), []byte(expectedState)) {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", "OIDC state doğrulaması başarısız.")
			return redirectWithError(c, "/dealer/login", "OIDC oturum doğrulaması başarısız.")
		}

		_, tokenEndpoint, userInfoEndpoint, err := oidcEndpoints()
		if err != nil || tokenEndpoint == "" || userInfoEndpoint == "" {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", "OIDC endpoint yapılandırması eksik.")
			return redirectWithError(c, "/dealer/login", "OIDC endpoint yapılandırması eksik.")
		}

		token, err := exchangeOIDCCode(c, tokenEndpoint, code)
		if err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", err.Error())
			return redirectWithError(c, "/dealer/login", "OIDC token alınamadı: "+err.Error())
		}
		userInfo, err := fetchOIDCUserInfo(userInfoEndpoint, token.AccessToken)
		if err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", err.Error())
			return redirectWithError(c, "/dealer/login", "OIDC kullanıcı bilgisi alınamadı: "+err.Error())
		}
		email := strings.TrimSpace(userInfo.Email)
		if email == "" {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", "OIDC email bilgisi dönmedi.")
			return redirectWithError(c, "/dealer/login", "OIDC email bilgisi dönmedi.")
		}

		merchant, err := findOrCreateOIDCMerchant(c, service, userInfo)
		if err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", email, "dealer.oidc_callback", "failed", "auth", "", err.Error())
			return redirectWithError(c, "/dealer/login", "Bayi hesabı açılamadı: "+err.Error())
		}
		logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "dealer.oidc_login", "success", "merchant", merchant.ID.String(), "Bayi OIDC ile giriş yaptı.")
		setDealerSessionCookie(c, merchant.ID.String())
		return redirectWithSuccess(c, "/dealer/dashboard", "OIDC ile giriş yapıldı.")
	}
}

func setOIDCCookie(c fiber.Ctx, name string, value string) {

	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   300,
		Expires:  time.Now().Add(5 * time.Minute),
		Secure:   strings.EqualFold(c.Protocol(), "https") || strings.EqualFold(c.Get("X-Forwarded-Proto"), "https"),
	})

}

func clearOIDCCookie(c fiber.Ctx, name string) {

	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   -1,
		Expires:  time.Now().Add(-time.Hour),
	})

}

func redirectWithSuccess(c fiber.Ctx, path string, message string) error {
	setFlashCookie(c, flashSuccessCookie, message)
	return c.Redirect().To(path)
}

func redirectWithError(c fiber.Ctx, path string, message string) error {
	setFlashCookie(c, flashErrorCookie, message)
	return c.Redirect().To(path)
}

func applyFlash(c fiber.Ctx, data *DealerPageData) {
	if data == nil {
		return
	}
	data.Success = flashCookieValue(c.Cookies(flashSuccessCookie))
	data.Error = flashCookieValue(c.Cookies(flashErrorCookie))
	clearFlashCookie(c, flashSuccessCookie)
	clearFlashCookie(c, flashErrorCookie)
}

func setFlashCookie(c fiber.Ctx, name string, value string) {
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(value)),
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   120,
		Expires:  time.Now().Add(2 * time.Minute),
		Secure:   strings.EqualFold(c.Protocol(), "https") || strings.EqualFold(c.Get("X-Forwarded-Proto"), "https"),
	})
}

func flashCookieValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return ""
	}
	return string(decoded)
}

func clearFlashCookie(c fiber.Ctx, name string) {
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   -1,
		Expires:  time.Now().Add(-time.Hour),
	})
}

func exchangeOIDCCode(c fiber.Ctx, tokenEndpoint string, code string) (*oidcTokenResponse, error) {
	clientID := strings.TrimSpace(os.Getenv("OIDC_CLIENT_ID"))
	clientSecret := os.Getenv("OIDC_CLIENT_SECRET")
	redirectURI := strings.TrimSpace(os.Getenv("OIDC_REDIRECT_URI"))
	if clientID == "" || clientSecret == "" || redirectURI == "" {
		return nil, errors.New("OIDC_CLIENT_ID, OIDC_CLIENT_SECRET veya OIDC_REDIRECT_URI eksik")
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(c.Context(), http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token endpoint HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var token oidcTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, errors.New("access_token boş")
	}
	return &token, nil
}

func fetchOIDCUserInfo(userInfoEndpoint string, accessToken string) (*oidcUserInfo, error) {
	req, err := http.NewRequest(http.MethodGet, userInfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("userinfo endpoint HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var userInfo oidcUserInfo
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return nil, err
	}
	return &userInfo, nil
}

func findOrCreateOIDCMerchant(c fiber.Ctx, service *services.MerchantService, userInfo *oidcUserInfo) (*models.Merchant, error) {
	email := strings.TrimSpace(userInfo.Email)
	params := types.MerchantParams{
		Context: c.Context(),
		Email:   &email,
	}
	merchant, err := service.FindByEmail(params)
	if err == nil {
		return merchant, nil
	}

	name := strings.TrimSpace(userInfo.Name)
	if len(name) < 3 {
		name = strings.Split(email, "@")[0]
	}
	if len(name) < 3 {
		name = "OIDC Dealer"
	}
	password := uuid.NewString() + uuid.NewString()
	createParams := types.MerchantParams{
		Context:        c.Context(),
		Name:           &name,
		Email:          &email,
		EmailRepeat:    &email,
		Password:       &password,
		PasswordRepeat: &password,
	}
	if err := createParams.Validate(); err != nil {
		return nil, err
	}
	merchant, err = service.Create(createParams)
	if err == nil {
		return merchant, nil
	}
	return service.FindByEmail(params)
}

func setDealerSessionCookie(c fiber.Ctx, merchantID string) {
	c.Cookie(&fiber.Cookie{
		Name:     dealerSessionCookie,
		Value:    signedDealerSessionValue(merchantID),
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   int((12 * time.Hour).Seconds()),
		Expires:  time.Now().Add(12 * time.Hour),
		Secure:   strings.EqualFold(c.Protocol(), "https") || strings.EqualFold(c.Get("X-Forwarded-Proto"), "https"),
	})
}

func clearDealerSessionCookie(c fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     dealerSessionCookie,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   -1,
		Expires:  time.Now().Add(-time.Hour),
	})
}

func setAdminSessionCookie(c fiber.Ctx, email string) {
	c.Cookie(&fiber.Cookie{
		Name:     adminSessionCookie,
		Value:    signedDealerSessionValue(email),
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   int((8 * time.Hour).Seconds()),
		Expires:  time.Now().Add(8 * time.Hour),
		Secure:   strings.EqualFold(c.Protocol(), "https") || strings.EqualFold(c.Get("X-Forwarded-Proto"), "https"),
	})
}

func clearAdminSessionCookie(c fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     adminSessionCookie,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   -1,
		Expires:  time.Now().Add(-time.Hour),
	})
}

func requireAdmin(c fiber.Ctx) (string, bool) {
	email, err := verifyDealerSessionValue(c.Cookies(adminSessionCookie))
	if err != nil || strings.TrimSpace(email) == "" {
		clearAdminSessionCookie(c)
		return "", false
	}
	return email, true
}

func requireDealerSession(c fiber.Ctx) (string, bool) {
	merchantID, err := verifyDealerSessionValue(c.Cookies(dealerSessionCookie))
	if err != nil || strings.TrimSpace(merchantID) == "" {
		return "", false
	}
	return merchantID, true
}

func verifyAdminCredentials(email string, password string) bool {
	expectedEmail := strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))
	expectedPassword := os.Getenv("ADMIN_PASSWORD")
	if expectedEmail == "" {
		expectedEmail = "admin@gateway.local"
	}
	if expectedPassword == "" {
		expectedPassword = "admin123"
	}
	return strings.EqualFold(strings.TrimSpace(email), expectedEmail) && password == expectedPassword
}

func requireDealerMerchant(c fiber.Ctx, service *services.MerchantService) (*models.Merchant, bool) {
	merchantID, err := verifyDealerSessionValue(c.Cookies(dealerSessionCookie))
	if err != nil {
		clearDealerSessionCookie(c)
		return nil, false
	}
	id, err := uuid.Parse(merchantID)
	if err != nil {
		clearDealerSessionCookie(c)
		return nil, false
	}
	merchant, err := service.FindByID(types.MerchantParams{
		Context: c.Context(),
		ID:      &id,
	})
	if err != nil {
		clearDealerSessionCookie(c)
		return nil, false
	}
	return merchant, true
}

func redirectDealerLogin(c fiber.Ctx) error {
	return redirectWithError(c, "/dealer/login", "Devam etmek için giriş yapmalısın.")
}

func fillDealerMerchant(data *DealerPageData, merchant *models.Merchant) {
	if data == nil || merchant == nil {
		return
	}
	data.MerchantID = merchant.ID.String()
	data.MerchantName = merchant.Name
	data.MerchantEmail = merchant.Email
	data.HasSession = true
}

func dealerPageData(title string, active string) DealerPageData {
	oidcURL := "/auth/oidc/login"
	provider := strings.TrimSpace(os.Getenv("OIDC_PROVIDER_NAME"))
	if provider == "" {
		provider = "Kurumsal hesap"
	}

	return DealerPageData{
		Title:            title,
		Active:           active,
		OIDCLoginURL:     oidcURL,
		OIDCProvider:     provider,
		RegisterURL:      "/dealer/register",
		LoginURL:         "/dealer/login",
		OnboardingURL:    "/dealer/onboarding",
		DashboardURL:     "/dealer/dashboard",
		TreasuryURL:      "/dealer/dashboard/treasury",
		ActivityURL:      "/dealer/dashboard/activity",
		WithdrawalsURL:   "/dealer/dashboard/withdrawals",
		DomainsPanelURL:  "/dealer/dashboard/domains",
		ProductsURL:      "/dealer/products",
		ProductsPanelURL: "/dealer/dashboard/products",
		DomainsURL:       "/dealer/domains",
		LogoutURL:        "/dealer/logout",
		ActivePanel:      "treasury",
	}
}

func normalizeLanguage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "en", "en-us", "en-gb":
		return "en"
	default:
		return "tr"
	}
}

func renderPaymentLinkError(c fiber.Ctx, message string) error {
	return c.Status(fiber.StatusNotFound).Render("gateway/payment_result", fiber.Map{
		"Title":      "Payment link unavailable",
		"Message":    message,
		"Status":     "error",
		"ResultKind": "error",
	})
}

func dashboardPanel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "activity":
		return "activity"
	case "withdrawals":
		return "withdrawals"
	case "domains":
		return "domains"
	case "products":
		return "products"
	default:
		return "treasury"
	}
}

func currentDashboardPanel(c fiber.Ctx) string {
	if strings.EqualFold(c.Path(), "/dealer/domains") {
		return "domains"
	}
	return dashboardPanel(c.Params("section"))
}

func currentAdminPanel(c fiber.Ctx) string {
	path := strings.ToLower(strings.TrimSuffix(c.Path(), "/"))
	section := c.Params("section")
	if section != "" {
		return strings.ToLower(strings.TrimSpace(section))
	}
	switch path {
	case "/admin/merchants":
		return "merchants"
	case "/admin/payments":
		return "payments"
	case "/admin/deposits":
		return "deposits"
	case "/admin/withdrawals":
		return "withdrawals"
	case "/admin/wallets":
		return "wallets"
	case "/admin/activity":
		return "activity"
	case "/admin/sweep":
		return "sweep"
	case "/admin/admins":
		return "admins"
	default:
		return "overview"
	}
}

func adminPageData(adminEmail string, panel string) DealerPageData {
	data := DealerPageData{
		Title:        "Admin paneli",
		Active:       "admin",
		HasSession:   true,
		MerchantEmail: adminEmail,
		LogoutURL:    "/admin/logout",

		AdminPanel:          panel,
		AdminOverviewURL:    "/admin",
		AdminMerchantsURL:   "/admin/merchants",
		AdminPaymentsURL:    "/admin/payments",
		AdminDepositsURL:    "/admin/deposits",
		AdminWithdrawalsURL: "/admin/withdrawals",
		AdminWalletsURL:     "/admin/wallets",
		AdminActivityURL:    "/admin/activity",
		AdminSweepURL:       "/admin/sweep",
	}
	return data
}

func dealerDomainViews(domains []models.Domain) []DealerDomainView {
	views := make([]DealerDomainView, 0, len(domains))
	for _, domain := range domains {
		views = append(views, DealerDomainView{
			ID:          domain.ID.String(),
			DomainURL:   domain.DomainURL,
			WebhookURL:  domain.WebhookURL,
			APIKey:      domain.APIKey,
			HDAccountID: domain.HDAccountID,
			CreatedAt:   formatPanelTime(domain.CreatedAt),
		})
	}
	return views
}

func dealerProductViews(c fiber.Ctx, products []models.Product) []DealerProductView {
	views := make([]DealerProductView, 0, len(products))
	for _, product := range products {
		logoText := "?"
		if product.Name != "" {
			runes := []rune(product.Name)
			logoText = strings.ToUpper(string(runes[0]))
		}
		views = append(views, DealerProductView{
			ID:          product.ID.String(),
			Name:        product.Name,
			Description: product.Description,
			Amount:      product.Amount,
			Currency:    product.Currency,
			Language:    strings.ToUpper(product.Language),
			Domain:      product.Domain.DomainURL,
			PaymentURL:  baseURL(c) + "/payment-links/" + product.LinkToken,
			LogoURL:     product.LogoURL,
			LogoText:    logoText,
			SuccessURL:  product.SuccessURL,
			CancelURL:   product.CancelURL,
			CreatedAt:   formatPanelTime(product.CreatedAt),
		})
	}
	return views
}

func dealerPaymentViews(c fiber.Ctx, payments []models.PaymentSession) []DealerPaymentView {
	views := make([]DealerPaymentView, 0, len(payments))
	for _, payment := range payments {
		selectedChain := "-"
		if payment.SelectedChainID != nil {
			selectedChain = chainLabel(*payment.SelectedChainID)
		}
		checkoutURL := baseURL(c) + "/checkout/" + payment.SessionToken
		merchant := payment.Merchant.Name
		if strings.TrimSpace(merchant) == "" {
			merchant = shortText(payment.MerchantID.String(), 8, 6)
		}
		domain := payment.Domain.DomainURL
		if strings.TrimSpace(domain) == "" {
			domain = shortText(payment.DomainID.String(), 8, 6)
		}
		views = append(views, DealerPaymentView{
			ID:             payment.ID.String(),
			OrderID:        payment.OrderID,
			ProductID:      emptyDash(payment.ProductID),
			UserID:         emptyDash(payment.UserID),
			Merchant:       merchant,
			Domain:         domain,
			Amount:         payment.Amount,
			Currency:       payment.Currency,
			Status:         payment.Status,
			CheckoutURL:    checkoutURL,
			SelectedAsset:  emptyDash(payment.SelectedSymbol),
			SelectedChain:  selectedChain,
			DepositAddress: shortText(payment.DepositAddress, 12, 8),
			CreatedAt:      formatPanelTime(payment.CreatedAt),
		})
	}
	return views
}

func dealerAdminMerchantViews(merchants []models.Merchant) []DealerAdminMerchantView {
	views := make([]DealerAdminMerchantView, 0, len(merchants))
	for _, merchant := range merchants {
		views = append(views, DealerAdminMerchantView{
			ID:        merchant.ID.String(),
			Name:      merchant.Name,
			Email:     merchant.Email,
			IsActive:  merchant.IsActive,
			CreatedAt: formatPanelTime(merchant.CreatedAt),
		})
	}
	return views
}

func dealerWalletViews(wallets []models.Wallet) []DealerWalletView {
	views := make([]DealerWalletView, 0, len(wallets))
	for _, wallet := range wallets {
		merchant := wallet.Merchant.Name
		if strings.TrimSpace(merchant) == "" {
			merchant = shortText(wallet.MerchantID.String(), 8, 6)
		}
		missing := make([]DealerMissingChainView, 0)
		for _, def := range walletChainDefs(wallet) {
			if strings.TrimSpace(def.address) == "" {
				missing = append(missing, DealerMissingChainView{
					ChainName:  def.chainName,
					ChainLabel: def.label,
					WalletID:   wallet.ID.String(),
				})
			}
		}
		views = append(views, DealerWalletView{
			ID:            wallet.ID.String(),
			ShortID:       shortText(wallet.ID.String(), 8, 6),
			Merchant:      merchant,
			Label:         walletLabel(wallet),
			ProductID:     emptyDash(wallet.ProductID),
			UserID:        emptyDash(wallet.UserID),
			Domain:        shortText(wallet.DomainID.String(), 8, 6),
			CreatedAt:     formatPanelTime(wallet.CreatedAt),
			Addresses:     walletAddressViews(wallet),
			MissingChains: missing,
		})
	}
	return views
}

// buildWalletBalanceMap returns a map of walletID -> balance rows (one per chain+symbol).
func buildWalletBalanceMap(ctx context.Context, txRepo *repositories.TransactionRepo) map[uuid.UUID][]DealerWalletBalanceRow {
	rows, err := txRepo.AllWalletDeposits(ctx)
	result := make(map[uuid.UUID][]DealerWalletBalanceRow)
	if err != nil {
		return result
	}
	for _, r := range rows {
		display := formatTokenAmount(r.Deposited, r.Decimals)
		result[r.WalletID] = append(result[r.WalletID], DealerWalletBalanceRow{
			Chain:     chainLabel(constants.ChainID(r.ChainID)),
			Symbol:    r.Symbol,
			Deposited: display,
		})
	}
	return result
}

func dealerWalletViewsWithBalances(wallets []models.Wallet, balanceMap map[uuid.UUID][]DealerWalletBalanceRow) []DealerWalletView {
	views := dealerWalletViews(wallets)
	for i, v := range views {
		if id, err := uuid.Parse(v.ID); err == nil {
			if bals, ok := balanceMap[id]; ok {
				views[i].Balances = bals
			}
		}
	}
	return views
}

func dealerPaginationView(page int, limit int, total int64, basePath string) DealerPaginationView {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(limit) - 1) / int64(limit))
	}
	from := 0
	to := 0
	if total > 0 {
		from = (page-1)*limit + 1
		to = page * limit
		if int64(to) > total {
			to = int(total)
		}
	}
	view := DealerPaginationView{
		Page:       page,
		Limit:      limit,
		Total:      total,
		TotalPages: totalPages,
		From:       from,
		To:         to,
		HasPrev:    page > 1,
		HasNext:    totalPages > 0 && page < totalPages,
	}
	if view.HasPrev {
		view.PrevURL = fmt.Sprintf("%s?page=%d&limit=%d", basePath, page-1, limit)
	}
	if view.HasNext {
		view.NextURL = fmt.Sprintf("%s?page=%d&limit=%d", basePath, page+1, limit)
	}

	// Build page link list (show at most ~7 items with ellipsis).
	if totalPages > 1 {
		seen := make(map[int]bool)
		addPage := func(p int) {
			if p < 1 || p > totalPages || seen[p] {
				return
			}
			seen[p] = true
			view.PageURLs = append(view.PageURLs, DealerPageURL{
				Page:   p,
				URL:    fmt.Sprintf("%s?page=%d&limit=%d", basePath, p, limit),
				Active: p == page,
			})
		}
		for p := 1; p <= min(3, totalPages); p++ {
			addPage(p)
		}
		for p := max(1, page-1); p <= min(totalPages, page+1); p++ {
			addPage(p)
		}
		for p := max(1, totalPages-2); p <= totalPages; p++ {
			addPage(p)
		}
	}
	return view
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type walletChainDef struct {
	label     string
	chainName string
	address   string
}

func walletChainDefs(wallet models.Wallet) []walletChainDef {
	return []walletChainDef{
		{"BTC", "bitcoin", wallet.BitcoinAddress},
		{"ETH", "ethereum", wallet.EthereumAddress},
		{"BASE", "base", wallet.BaseAddress},
		{"UNI", "unichain", wallet.UnichainAddress},
		{"AVAX", "avalanche", wallet.AvalancheAddress},
		{"BSC", "bnbchain", wallet.BinanceAddress},
		{"CHZ", "chiliz", wallet.ChilizAddress},
		{"CHZ-Spicy", "chiliz-spicy", wallet.ChilizSpicyAddress},
		{"TRX", "tron", wallet.TronAddress},
		{"SOL", "solana", wallet.SolanaAddress},
	}
}

func walletAddressViews(wallet models.Wallet) []DealerAddressView {
	filtered := make([]DealerAddressView, 0, 10)
	for _, def := range walletChainDefs(wallet) {
		if strings.TrimSpace(def.address) != "" {
			filtered = append(filtered, DealerAddressView{Chain: def.label, Address: def.address})
		}
	}
	return filtered
}

func walletLabel(wallet models.Wallet) string {
	productID := strings.TrimSpace(wallet.ProductID)
	userID := strings.TrimSpace(wallet.UserID)
	switch {
	case productID != "" && userID != "":
		return productID + " · " + userID
	case productID != "":
		return productID
	case userID != "":
		return userID
	default:
		return "Wallet " + shortText(wallet.ID.String(), 8, 6)
	}
}

func dealerBalanceViews(summaries []models.DepositSummary) []DealerBalanceView {
	views := make([]DealerBalanceView, 0, len(summaries))
	for _, summary := range summaries {
		token := ""
		if summary.Token != nil {
			token = *summary.Token
		}
		lastDeposit := ""
		if summary.LastDepositAt != nil {
			lastDeposit = formatPanelTime(*summary.LastDepositAt)
		}
		views = append(views, DealerBalanceView{
			Chain:         chainLabel(summary.ChainID),
			Symbol:        summary.Symbol,
			Token:         token,
			AmountRaw:     summary.AmountRaw,
			AmountDisplay: formatTokenAmount(summary.AmountRaw, summary.Decimals),
			Decimals:      summary.Decimals,
			Deposits:      summary.TransactionCount,
			Users:         summary.UserCount,
			LastDeposit:   lastDeposit,
			DisplayToken:  emptyDash(token),
		})
	}
	return views
}

func dealerAllBalanceViews(registry *asset.Registry, balances []DealerBalanceView) []DealerBalanceView {
	if registry == nil {
		return balances
	}
	byKey := make(map[string]DealerBalanceView, len(balances))
	for _, balance := range balances {
		byKey[balanceKey(balance.Chain, balance.Symbol, balance.Token)] = balance
	}

	assets := registry.ListAll()
	views := make([]DealerBalanceView, 0, len(assets))
	seen := make(map[string]struct{}, len(assets))
	for _, assetInfo := range assets {
		token := ""
		if !assetInfo.IsNative() {
			token = assetInfo.GetIdentifier()
		}
		key := balanceKey(chainLabel(assetInfo.GetChainID()), assetInfo.GetSymbol(), token)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if balance, ok := byKey[key]; ok {
			views = append(views, balance)
			continue
		}
		views = append(views, DealerBalanceView{
			Chain:         chainLabel(assetInfo.GetChainID()),
			Symbol:        assetInfo.GetSymbol(),
			Token:         token,
			AmountRaw:     "0",
			AmountDisplay: "0",
			Decimals:      assetInfo.GetDecimals(),
			DisplayToken:  emptyDash(token),
		})
	}
	return views
}

func balanceKey(chain string, symbol string, token string) string {
	return strings.ToLower(strings.TrimSpace(chain)) + "|" + strings.ToUpper(strings.TrimSpace(symbol)) + "|" + strings.ToLower(strings.TrimSpace(token))
}

func dealerChainVaultViews(balances []DealerBalanceView) []DealerChainVaultView {
	byChain := make(map[string][]DealerBalanceView)
	for _, balance := range balances {
		byChain[balance.Chain] = append(byChain[balance.Chain], balance)
	}

	views := make([]DealerChainVaultView, 0, len(constants.AllChainIDs()))
	for _, chainID := range constants.AllChainIDs() {
		chain := chainLabel(chainID)
		assets := byChain[chain]
		view := DealerChainVaultView{
			Chain:  chain,
			Assets: assets,
			Empty:  len(assets) == 0,
		}
		for _, asset := range assets {
			view.Deposits += asset.Deposits
			view.Users += asset.Users
		}
		views = append(views, view)
	}
	return views
}

func dealerActivityViews(transactions []models.Transaction) []DealerActivityView {
	views := make([]DealerActivityView, 0, len(transactions))
	for _, tx := range transactions {
		webhookStatus := "bekliyor"
		if tx.WebhookSentAt != nil {
			webhookStatus = "gönderildi"
		} else if tx.WebhookLastError != "" {
			webhookStatus = "hatalı"
		}
		eventType := tx.EventType
		if eventType == "" {
			eventType = "transaction"
		}
		views = append(views, DealerActivityView{
			ID:              tx.ID.String(),
			Type:            eventType,
			Chain:           chainLabel(tx.ChainID),
			Symbol:          tx.Symbol,
			AmountRaw:       tx.Amount,
			AmountDisplay:   formatTokenAmount(tx.Amount, tx.Decimals),
			Status:          tx.Status,
			Hash:            tx.Hash,
			FromAddress:     shortText(tx.FromAddress, 10, 8),
			ToAddress:       shortText(tx.ToAddress, 10, 8),
			ProductID:       emptyDash(tx.ProductID),
			UserID:          emptyDash(tx.UserID),
			WebhookStatus:   webhookStatus,
			WebhookAttempts: tx.WebhookAttempts,
			CreatedAt:       formatPanelTime(tx.CreatedAt),
		})
	}
	return views
}

func dealerAuditLogViews(logs []models.ActivityLog) []DealerAuditLogView {
	views := make([]DealerAuditLogView, 0, len(logs))
	for _, log := range logs {
		subject := strings.TrimSpace(log.SubjectType)
		if strings.TrimSpace(log.SubjectID) != "" {
			if subject != "" {
				subject += " · "
			}
			subject += shortText(log.SubjectID, 12, 8)
		}
		views = append(views, DealerAuditLogView{
			ID:          log.ID.String(),
			Event:       log.Event,
			Status:      log.Status,
			Actor:       emptyDash(log.ActorEmail),
			Subject:     emptyDash(subject),
			Description: emptyDash(log.Description),
			IPAddress:   emptyDash(log.IPAddress),
			UserAgent:   shortText(log.UserAgent, 52, 18),
			Method:      emptyDash(log.Method),
			Path:        emptyDash(log.Path),
			CreatedAt:   formatPanelTime(log.CreatedAt),
			CreatedISO:  log.CreatedAt.UTC().Format(time.RFC3339Nano),
			IsOIDC:      strings.Contains(log.Event, "oidc"),
			IsFailed:    log.Status == "failed",
		})
	}
	return views
}

func logDealerActivity(c fiber.Ctx, repo *repositories.ActivityLogRepo, merchantID *uuid.UUID, actorType string, actorEmail string, event string, status string, subjectType string, subjectID string, description string) {
	if repo == nil {
		return
	}
	log := &models.ActivityLog{
		MerchantID:  merchantID,
		ActorType:   emptyDefault(actorType, "system"),
		ActorEmail:  strings.TrimSpace(actorEmail),
		Event:       emptyDefault(event, "activity"),
		Status:      emptyDefault(status, "info"),
		SubjectType: strings.TrimSpace(subjectType),
		SubjectID:   strings.TrimSpace(subjectID),
		Description: strings.TrimSpace(description),
		IPAddress:   clientIP(c),
		UserAgent:   strings.TrimSpace(c.Get("User-Agent")),
		Method:      strings.TrimSpace(c.Method()),
		Path:        strings.TrimSpace(c.Path()),
		CreatedAt:   time.Now().UTC(),
	}
	_ = repo.Create(c.Context(), log)
}

func clientIP(c fiber.Ctx) string {
	for _, header := range []string{"CF-Connecting-IP", "X-Forwarded-For", "X-Real-IP"} {
		value := strings.TrimSpace(c.Get(header))
		if value == "" {
			continue
		}
		if header == "X-Forwarded-For" {
			value = strings.TrimSpace(strings.Split(value, ",")[0])
		}
		if value != "" {
			return value
		}
	}
	return strings.TrimSpace(c.IP())
}

func emptyDefault(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func dealerWithdrawalViews(requests []models.WithdrawalRequest) []DealerWithdrawalView {
	views := make([]DealerWithdrawalView, 0, len(requests))
	for _, request := range requests {
		merchantName := request.Merchant.Name
		if merchantName == "" {
			merchantName = request.MerchantID.String()
		}
		views = append(views, DealerWithdrawalView{
			ID:           request.ID.String(),
			MerchantName: merchantName,
			WalletID:     request.WalletID.String(),
			Chain:        request.Chain,
			ToAddress:    request.ToAddress,
			AmountRaw:    request.AmountRaw,
			Note:         request.Note,
			Status:       request.Status,
			TxHash:       request.TxHash,
			Error:        request.Error,
			CreatedAt:    formatPanelTime(request.CreatedAt),
		})
	}
	return views
}

func chainLabel(chainID constants.ChainID) string {
	switch chainID {
	case constants.Bitcoin:
		return "Bitcoin"
	case constants.Ethereum:
		return "Ethereum"
	case constants.Base:
		return "Base"
	case constants.Binance:
		return "BNB Chain"
	case constants.Unichain:
		return "Unichain"
	case constants.Avalanche:
		return "Avalanche"
	case constants.Chiliz:
		return "Chiliz"
	case constants.Solana:
		return "Solana"
	case constants.TRON:
		return "TRON"
	case constants.ChilizSpicy:
		return "Chiliz Spicy"
	default:
		return fmt.Sprintf("chain-%d", chainID)
	}
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func formatPanelTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format("2006-01-02 15:04:05.000 UTC")
}

func shortText(value string, prefix int, suffix int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	if prefix <= 0 || suffix <= 0 || len(value) <= prefix+suffix+3 {
		return value
	}
	return value[:prefix] + "..." + value[len(value)-suffix:]
}

func formatTokenAmount(raw string, decimals uint8) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "0"
	}
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = strings.TrimPrefix(value, "-")
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
		if negative {
			return "-" + value
		}
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
	if negative {
		whole = "-" + whole
	}
	if fraction == "" {
		return whole
	}
	return whole + "." + fraction
}

func signedDealerSessionValue(merchantID string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(merchantID))
	signature := dealerSessionSignature(payload)
	return payload + "." + signature
}

func verifyDealerSessionValue(value string) (string, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("invalid session")
	}
	expected := dealerSessionSignature(parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return "", errors.New("invalid session signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func dealerSessionSignature(payload string) string {
	mac := hmac.New(sha256.New, []byte(dealerSessionSecret()))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func dealerSessionSecret() string {
	for _, key := range []string{"DEALER_SESSION_SECRET", "SESSION_SECRET", "MASTER_KEY"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return fmt.Sprintf("dev-session-secret-%s", dealerSessionCookie)
}

func oidcScopes() string {
	scopes := strings.TrimSpace(os.Getenv("OIDC_SCOPES"))
	if scopes == "" {
		return "openid profile email roles"
	}
	return strings.ReplaceAll(scopes, ",", " ")
}

func oidcEndpoints() (string, string, string, error) {
	authURL := strings.TrimSpace(os.Getenv("OIDC_AUTH_URL"))
	tokenURL := strings.TrimSpace(os.Getenv("OIDC_TOKEN_URL"))
	userInfoURL := strings.TrimSpace(os.Getenv("OIDC_USERINFO_URL"))
	authority := strings.TrimRight(strings.TrimSpace(os.Getenv("OIDC_AUTHORITY")), "/")

	if authority != "" {
		if cfg, err := fetchOIDCDiscovery(authority); err == nil {
			if authURL == "" {
				authURL = cfg.AuthorizationEndpoint
			}
			if tokenURL == "" {
				tokenURL = cfg.TokenEndpoint
			}
			if userInfoURL == "" {
				userInfoURL = cfg.UserInfoEndpoint
			}
		}
		if authURL == "" {
			authURL = authority + "/connect/authorize"
		}
		if tokenURL == "" {
			tokenURL = authority + "/connect/token"
		}
		if userInfoURL == "" {
			userInfoURL = authority + "/connect/userinfo"
		}
	}

	if !validAbsoluteURL(authURL) {
		return "", "", "", errors.New("OIDC authorization endpoint missing")
	}
	if tokenURL != "" && !validAbsoluteURL(tokenURL) {
		return "", "", "", errors.New("OIDC token endpoint invalid")
	}
	if userInfoURL != "" && !validAbsoluteURL(userInfoURL) {
		return "", "", "", errors.New("OIDC userinfo endpoint invalid")
	}
	return authURL, tokenURL, userInfoURL, nil
}

func fetchOIDCDiscovery(authority string) (*oidcProviderConfig, error) {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(authority + "/.well-known/openid-configuration")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("discovery HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var cfg oidcProviderConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validAbsoluteURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Scheme != "" && parsed.Host != ""
}

func stringPtr(value string) *string {
	return &value
}

func parseQueryInt(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}
