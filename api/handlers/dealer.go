package handlers

import (
	"bytes"
	"context"
	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/helpers"
	"core/models"
	"core/repositories"
	"core/services/pricing"
	services "core/services/system"
	"core/services/txrescan"
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

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/oauth2"
)

const dealerSessionCookie = "dealer_session"
const adminSessionCookie = "admin_session"
const adminPendingTOTPCookie = "admin_totp_pending" // temp: holds admin ID awaiting 2FA
const adminSetupTOTPCookie = "admin_totp_setup"     // temp: holds admin ID during TOTP setup
const oidcStateCookie = "oidc_state"
const oidcNonceCookie = "oidc_nonce"
const flashSuccessCookie = "flash_success"
const flashErrorCookie = "flash_error"

type DealerDeps struct {
	MerchantService     *services.MerchantService
	DomainService       *services.DomainService
	WalletRepo          *repositories.WalletRepo
	ProductRepo         *repositories.ProductRepo
	PaymentRepo         *repositories.PaymentRepo
	WithdrawalRepo      *repositories.WithdrawalRequestRepo
	RefundRepo          *repositories.RefundRepo
	LedgerRepo          *repositories.LedgerRepo
	TransactionRepo     *repositories.TransactionRepo
	WebhookDeliveryRepo *repositories.WebhookDeliveryRepo
	ActivityLogRepo     *repositories.ActivityLogRepo
	AdminRepo           *repositories.AdminRepo
	AssetRegistry       *asset.Registry
	Blockchains         *blockchain.ChainFactory
	TxRescanService     func() *txrescan.Service
	Notifier            WebhookNotifier
	PriceOracle         pricing.PriceOracle
}

// WebhookNotifier is the minimal interface the dealer handlers need from the webhook service.
type WebhookNotifier interface {
	Deliver(ctx context.Context, domain models.Domain, tx models.Transaction) error
	DeliverPayment(ctx context.Context, domain models.Domain, session models.PaymentSession) error
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
	TransactionsURL   string
	UsersURL          string
	WithdrawalsURL    string
	RescanURL         string
	DomainsPanelURL   string
	ProductsURL       string
	InvoicesURL       string
	ProductsPanelURL  string
	SettingsPanelURL  string
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

	AdminMerchants    []DealerAdminMerchantView
	AdminWallets      []DealerWalletView
	AdminDeposits     []DealerActivityView
	AdminActivityLogs []DealerAuditLogView
	AdminWebhooks     []DealerWebhookDeliveryView
	AdminRefunds      []DealerRefundView

	AdminPanel          string
	AdminOverviewURL    string
	AdminMerchantsURL   string
	AdminPaymentsURL    string
	AdminDepositsURL    string
	AdminWithdrawalsURL string
	AdminWalletsURL     string
	AdminActivityURL    string
	AdminSweepURL       string
	AdminLinksURL       string
	AdminWebhooksURL    string
	AdminRefundsURL     string
	AdminRescanURL      string
	DepositCount        int
	WithdrawalCount     int

	WalletSearch        string
	PaymentStatusFilter string
	PaymentStats        map[string]int64
	HideTestnets        bool
	HiddenChains        string

	AdminPagination          DealerPaginationView
	AdminMerchantFilter      string
	AdminTOTPEnabled         bool
	AdminSecurityURL         string
	TOTPSecret               string
	TOTPQRDataURL            string
	AdminDepositFromFilter   string
	AdminDepositToFilter     string
	AdminDepositHashFilter   string
	AdminWebhookStatusFilter string
	AdminRefundStatusFilter  string
	AdminRescanResult        string
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
	Chain     string
	Symbol    string
	LogoURL   string
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
	Merchant    string
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
	InvoiceURL     string
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
	Page     int
	URL      string
	Active   bool
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

type DealerWebhookDeliveryView struct {
	ID          string
	EventID     string
	EventType   string
	TargetURL   string
	Status      string
	Attempts    uint
	LastError   string
	CreatedAt   string
	UpdatedAt   string
	DeliveredAt string
}

type DealerRefundView struct {
	ID          string
	PaymentID   string
	MerchantID  string
	DomainID    string
	AmountRaw   string
	Reason      string
	Status      string
	TxHash      string
	Error       string
	RequestedBy string
	CreatedAt   string
}

type DealerBalanceView struct {
	Chain         string
	ChainLogoURL  string
	Symbol        string
	Token         string
	LogoURL       string
	AmountRaw     string
	AmountDisplay string
	AmountUSD     string
	Decimals      uint8
	Deposits      int64
	Users         int64
	LastDeposit   string
	DisplayToken  string
}

type DealerChainVaultView struct {
	Chain        string
	ChainLogoURL string
	Assets       []DealerBalanceView
	Deposits     int64
	Users        int64
	Empty        bool
}

type DealerActivityView struct {
	ID              string
	Type            string
	Chain           string
	ChainLogoURL    string
	Symbol          string
	LogoURL         string
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
func HandleDealerRegisterSubmit(deps DealerDeps) fiber.Handler {
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
			logDealerActivity(c, deps.ActivityLogRepo, nil, "dealer", email, "dealer.register", "failed", "merchant", "", err.Error())
			data := dealerPageData("Bayi kaydı", "register")
			data.Error = err.Error()
			return c.Status(fiber.StatusBadRequest).Render("dealer/register", data, "dealer/layout")
		}

		merchant, err := deps.MerchantService.Create(params)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "dealer", email, "dealer.register", "failed", "merchant", "", err.Error())
			data := dealerPageData("Bayi kaydı", "register")
			data.Error = "Bayi kaydı oluşturulamadı: " + err.Error()
			return c.Status(fiber.StatusInternalServerError).Render("dealer/register", data, "dealer/layout")
		}

		_ = provisionMerchantReserve(c.Context(), merchant.ID, deps)

		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "dealer.register", "success", "merchant", merchant.ID.String(), "Bayi hesabı self servis kayıt ile oluşturuldu.")
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
		paymentStatusFilter := strings.TrimSpace(c.Query("status"))
		var payments []models.PaymentSession
		var paymentTotal int64
		if deps.PaymentRepo != nil {
			payments, paymentTotal, err = deps.PaymentRepo.ListByMerchantPage(c.Context(), merchant.ID, paymentStatusFilter, 1, 100)
			if err != nil {
				data := dealerPageData("Bayi paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Checkout listesi okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		var balances []models.DepositSummary
		var ledgerBalances []repositories.LedgerBalanceRow
		if deps.LedgerRepo != nil {
			ledgerBalances, err = deps.LedgerRepo.MerchantBalances(c.Context(), merchant.ID)
			if err != nil {
				data := dealerPageData("Bayi paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Ledger bakiyesi okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		if len(ledgerBalances) == 0 {
			balances, err = deps.TransactionRepo.MerchantDepositSummary(c.Context(), merchant.ID)
			if err != nil {
				data := dealerPageData("Bayi paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Bakiye özeti okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
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

		const walletPageSize = 20
		walletPage := max(1, parseQueryInt(c.Query("page"), 1))
		walletSearch := strings.TrimSpace(c.Query("q"))
		walletOffset := (walletPage - 1) * walletPageSize
		wallets, walletTotal, _ := deps.WalletRepo.SearchByMerchantPage(c.Context(), merchant.ID, walletSearch, walletPageSize, walletOffset)
		reserveWallets, _ := deps.WalletRepo.ListReserveByMerchant(c.Context(), merchant.ID)

		data := dealerPageData("Bayi paneli", "dashboard")
		fillDealerMerchant(&data, merchant)
		data.ActivePanel = currentDashboardPanel(c)
		applyFlash(c, &data)
		data.Domains = dealerDomainViews(domains)
		data.Withdrawals = dealerWithdrawalViews(withdrawals)
		data.Products = dealerProductViews(c, products)
		data.Payments = dealerPaymentViews(c, payments)
		if len(ledgerBalances) > 0 {
			data.Balances = dealerLedgerBalanceViews(ledgerBalances, deps.AssetRegistry)
		} else {
			data.Balances = dealerBalanceViews(balances, deps.AssetRegistry)
		}
		data.Balances = dealerAllBalanceViews(deps.AssetRegistry, data.Balances)
		enrichBalancesWithUSD(c.Context(), data.Balances, deps.PriceOracle)
		data.ChainVaults = dealerChainVaultViews(data.Balances)
		data.Activities = dealerActivityViews(transactions, deps.AssetRegistry)
		data.AuditLogs = dealerAuditLogViews(auditLogs)
		data.Wallets = dealerWalletViews(wallets)
		data.WithdrawalWallets = dealerWalletViews(reserveWallets)
		usersBaseURL := "/dealer/dashboard/users"
		if walletSearch != "" {
			usersBaseURL += "?q=" + walletSearch
		}
		data.WalletPage = dealerPaginationView(walletPage, walletPageSize, walletTotal, usersBaseURL)
		data.WalletSearch = walletSearch
		data.WalletCount = int(walletTotal)
		data.DomainCount = len(domains)
		data.ProductCount = len(data.Products)
		data.PaymentCount = int(paymentTotal)
		data.WithdrawalCount = len(withdrawals)
		data.PaymentStatusFilter = paymentStatusFilter
		if deps.PaymentRepo != nil {
			data.PaymentStats, _ = deps.PaymentRepo.StatsByMerchant(c.Context(), merchant.ID)
		}
		data.HideTestnets = merchant.HideTestnets
		data.HiddenChains = merchant.HiddenChains
		if merchant.HideTestnets || merchant.HiddenChains != "" {
			data.Balances = filterBalancesBySettings(data.Balances, merchant.HideTestnets, merchant.HiddenChains)
			data.ChainVaults = filterVaultsBySettings(data.ChainVaults, merchant.HideTestnets, merchant.HiddenChains)
		}
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

func HandleDealerInvoiceCreate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}
		if deps.PaymentRepo == nil || deps.WalletRepo == nil {
			return redirectWithError(c, "/dealer/dashboard/products", "Invoice altyapısı hazır değil.")
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

		orderID := strings.TrimSpace(c.FormValue("order_id"))
		if orderID == "" {
			orderID = "dash-" + uuid.NewString()
		}
		productID := strings.TrimSpace(c.FormValue("product_id"))
		userID := strings.TrimSpace(c.FormValue("user_id"))
		amount := strings.TrimSpace(c.FormValue("amount"))
		currency := strings.ToUpper(strings.TrimSpace(c.FormValue("currency")))
		successURL := strings.TrimSpace(c.FormValue("success_url"))
		cancelURL := strings.TrimSpace(c.FormValue("cancel_url"))
		if currency == "" {
			currency = "USD"
		}

		params := types.PaymentCreateParams{
			Context:    c.Context(),
			OrderID:    &orderID,
			ProductID:  stringPtr(productID),
			UserID:     stringPtr(userID),
			Amount:     &amount,
			Currency:   &currency,
			SuccessURL: stringPtr(successURL),
			CancelURL:  stringPtr(cancelURL),
		}
		if err := params.Validate(); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "invoice.create", "failed", "payment", orderID, err.Error())
			return redirectWithError(c, "/dealer/dashboard/products", "Invoice oluşturulamadı: "+err.Error())
		}

		productIDValue := valueOrDefault(params.ProductID, *params.OrderID)
		userIDValue := valueOrDefault(params.UserID, *params.OrderID)
		merchantID := merchant.ID.String()
		wallet, err := deps.WalletRepo.Create(types.WalletParams{
			Context:    c.Context(),
			MerchantId: &merchantID,
			DomainId:   &domainIDString,
			ProductId:  &productIDValue,
			UserId:     &userIDValue,
		})
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "invoice.create", "failed", "payment", orderID, err.Error())
			return redirectWithError(c, "/dealer/dashboard/products", "Wallet oluşturulamadı: "+err.Error())
		}

		expiresAt := time.Now().Add(paymentSessionTTL())
		session := &models.PaymentSession{
			MerchantID: merchant.ID,
			DomainID:   domain.ID,
			WalletID:   wallet.ID,
			OrderID:    *params.OrderID,
			ProductID:  productIDValue,
			UserID:     userIDValue,
			Amount:     *params.Amount,
			Currency:   *params.Currency,
			SuccessURL: valueOrDefault(params.SuccessURL, ""),
			CancelURL:  valueOrDefault(params.CancelURL, ""),
			Status:     models.PaymentStatusPending,
			ExpiresAt:  &expiresAt,
		}
		if err := deps.PaymentRepo.Create(c.Context(), session); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "invoice.create", "failed", "payment", orderID, err.Error())
			return redirectWithError(c, "/dealer/dashboard/products", "Invoice oluşturulamadı: "+err.Error())
		}

		checkoutURL := baseURL(c) + "/checkout/" + session.SessionToken
		invoiceURL := baseURL(c) + "/invoice/" + session.SessionToken
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "invoice.create", "success", "payment", session.ID.String(), "Dashboard invoice oluşturuldu.")
		return redirectWithSuccess(c, "/dealer/dashboard/products", "Invoice oluşturuldu: "+invoiceURL+" | Checkout: "+checkoutURL)
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
		if wallet.HDAddressId != 0 {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, "Sadece reserve (HD index 0) cüzdandan çekim yapılabilir.")
			return redirectWithError(c, "/dealer/dashboard/withdrawals", "Çekim sadece bayi reserve cüzdanından yapılabilir.")
		}

		chain := strings.ToLower(strings.TrimSpace(c.FormValue("chain")))
		symbol := strings.TrimSpace(c.FormValue("symbol"))
		tokenAddress := strings.TrimSpace(c.FormValue("token_address"))
		toAddress := strings.TrimSpace(c.FormValue("to_address"))
		amountRaw := strings.TrimSpace(c.FormValue("amount_raw"))
		note := strings.TrimSpace(c.FormValue("note"))
		chain, token, symbol, decimals, err := resolveWithdrawalAsset(deps.AssetRegistry, chain, symbol, tokenAddress)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, err.Error())
			return redirectWithError(c, "/dealer/dashboard", err.Error())
		}
		params := types.TransferParams{
			Context:   c.Context(),
			WalletID:  &walletIDRaw,
			Chain:     &chain,
			Token:     token,
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
			Token:       token,
			Symbol:      symbol,
			Decimals:    decimals,
			ToAddress:   *params.ToAddress,
			AmountRaw:   *params.AmountRaw,
			Note:        note,
			Status:      models.WithdrawalStatusPending,
			RequestedBy: merchant.Email,
		}
		if err := deps.WithdrawalRepo.CreateWithHold(c.Context(), request, deps.LedgerRepo); err != nil {
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

// HandleDealerWebhookTest sends a signed test event to the domain's configured webhook URL.
func HandleDealerWebhookTest(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Oturum gerekli"})
		}

		domainIDStr := c.Params("id")
		domain, err := deps.DomainService.FindByID(types.DomainParams{
			Context:  c.Context(),
			DomainID: &domainIDStr,
		})
		if err != nil || domain.MerchantID != merchant.ID {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Domain bulunamadı"})
		}
		if domain.WebhookURL == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Bu domain için webhook URL tanımlanmamış"})
		}
		if domain.WebhookSecret == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Bu domain için webhook secret tanımlanmamış"})
		}

		secret, err := helpers.DecryptSecret(domain.WebhookSecret)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Webhook secret çözülemedi"})
		}

		eventID := "test-" + uuid.New().String()
		testPayload := map[string]interface{}{
			"event_id":    eventID,
			"event_type":  "test",
			"merchant_id": merchant.ID.String(),
			"domain_id":   domain.ID.String(),
			"message":     "Bu bir test webhook isteğidir. Sisteme entegre edildiğinizi doğrulamak için gönderilmiştir.",
			"sent_at":     time.Now().UTC().Format(time.RFC3339),
		}
		body, _ := json.Marshal(testPayload)
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		sig := helpers.GenerateSignature(secret, ts, body)

		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequestWithContext(c.Context(), http.MethodPost, domain.WebhookURL, bytes.NewReader(body))
		if err != nil {
			return c.JSON(fiber.Map{"success": false, "error": err.Error()})
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "gateway-webhook/1.0")
		req.Header.Set("X-Gateway-Event", "test")
		req.Header.Set("X-Gateway-Event-Id", eventID)
		req.Header.Set("X-Gateway-Timestamp", ts)
		req.Header.Set("X-Gateway-Signature", "sha256="+sig)

		resp, err := client.Do(req)
		if err != nil {
			return c.JSON(fiber.Map{"success": false, "error": err.Error()})
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		success := resp.StatusCode >= 200 && resp.StatusCode < 300

		return c.JSON(fiber.Map{
			"success":     success,
			"status_code": resp.StatusCode,
			"response":    string(respBody),
		})
	}
}

func HandleDealerDomainUpdateWebhook(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Oturum gerekli"})
		}

		domainIDStr := c.Params("id")
		domainUUID, err := uuid.Parse(domainIDStr)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Geçersiz domain ID"})
		}

		webhookURL := strings.TrimSpace(c.FormValue("webhook_url"))
		webhookSecret := strings.TrimSpace(c.FormValue("webhook_secret"))
		if webhookURL == "" || webhookSecret == "" {
			return redirectWithError(c, "/dealer/dashboard/domains", "Webhook URL ve secret boş olamaz")
		}
		if err := helpers.ValidateWebhookURL(webhookURL); err != nil {
			return redirectWithError(c, "/dealer/dashboard/domains", "Geçersiz webhook URL: "+err.Error())
		}

		if err := deps.DomainService.UpdateWebhook(c.Context(), domainUUID, merchant.ID, webhookURL, webhookSecret); err != nil {
			return redirectWithError(c, "/dealer/dashboard/domains", "Güncelleme hatası: "+err.Error())
		}

		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.update_webhook", "success", "domain", domainIDStr, "Webhook URL ve secret güncellendi.")
		return redirectWithSuccess(c, "/dealer/dashboard/domains", "Webhook başarıyla güncellendi.")
	}
}

// HandleDealerDomainRotateAPISecret rotates the API secret for a dealer-owned domain.
// @Summary Rotate domain API secret
// @Description Rotates the API secret for an authenticated dealer domain. The new secret is returned once in the response.
// @Tags Dealers
// @Produce json
// @Param id path string true "Domain ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} types.ErrorResponse
// @Failure 401 {object} types.ErrorResponse
// @Router /dealer/domains/{id}/rotate-api-secret [post]
func HandleDealerDomainRotateAPISecret(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Oturum gerekli"})
		}
		domainIDStr := c.Params("id")
		domainUUID, err := uuid.Parse(domainIDStr)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Geçersiz domain ID"})
		}
		apiSecret, err := deps.DomainService.RotateAPISecret(c.Context(), domainUUID, merchant.ID)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.rotate_api_secret", "success", "domain", domainIDStr, "API secret rotated.")
		return c.JSON(fiber.Map{
			"result":     "ok",
			"domain_id":  domainIDStr,
			"api_secret": apiSecret,
		})
	}
}

func HandleDealerSettingsUpdate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}
		hideTestnets := c.FormValue("hide_testnets") == "on"
		hiddenChains := strings.TrimSpace(c.FormValue("hidden_chains"))
		if err := deps.MerchantService.Repo().UpdateSettings(c.Context(), merchant.ID, hideTestnets, hiddenChains); err != nil {
			return redirectWithError(c, "/dealer/dashboard/settings", "Ayarlar kaydedilemedi: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "settings.update", "success", "merchant", merchant.ID.String(), "Görünüm ayarları güncellendi.")
		return redirectWithSuccess(c, "/dealer/dashboard/settings", "Ayarlar kaydedildi.")
	}
}

// provisionMerchantReserve creates the system reserve domain + HD-index-0 wallet for a merchant.
// Called at registration and first OIDC login. Idempotent.
func provisionMerchantReserve(ctx context.Context, merchantID uuid.UUID, deps DealerDeps) error {
	domain, err := deps.DomainService.CreateReserve(ctx, merchantID)
	if err != nil {
		return fmt.Errorf("reserve domain: %w", err)
	}
	if _, err := deps.WalletRepo.CreateReserveWallet(ctx, merchantID, domain.ID, domain.HDAccountID); err != nil {
		return fmt.Errorf("reserve wallet: %w", err)
	}
	return nil
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

func totpQRDataURL(otpauthURL string) string {
	png, err := qrcode.Encode(otpauthURL, qrcode.Medium, 256)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
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
		data.TOTPSecret = secret
		data.TOTPQRDataURL = totpQRDataURL(qrURL)
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
			data.TOTPSecret = admin.TOTPSecret
			data.TOTPQRDataURL = totpQRDataURL(qrURL)
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
		_ = deps.AdminRepo.DisableTOTP(c.Context(), id)
		return redirectWithSuccess(c, "/admin/admins", "2FA sıfırlandı. Sonraki girişte yeniden kurulacak.")
	}
}

// HandleAdminTOTPEnable initiates 2FA setup for the currently logged-in admin.
// It sets the temporary setup cookie (same as the login flow) and redirects to /admin/2fa/setup.
func HandleAdminTOTPEnable(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		admin, err := deps.AdminRepo.FindByEmail(c.Context(), adminEmail)
		if err != nil {
			return redirectWithError(c, "/admin/security", "Admin bulunamadı.")
		}
		if admin.TOTPEnabled {
			return redirectWithError(c, "/admin/security", "2FA zaten etkin.")
		}
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

// HandleAdminTOTPDisableConfirm shows the TOTP verification form for disabling 2FA.
func HandleAdminTOTPDisableConfirm(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		admin, err := deps.AdminRepo.FindByEmail(c.Context(), adminEmail)
		if err != nil {
			return redirectWithError(c, "/admin/security", "Admin bulunamadı.")
		}
		if !admin.TOTPEnabled {
			return redirectWithError(c, "/admin/security", "2FA zaten devre dışı.")
		}
		data := adminPageData(adminEmail, "security")
		data.AdminTOTPEnabled = true
		applyFlash(c, &data)
		return c.Render("dealer/admin_2fa_disable", data, "dealer/layout")
	}
}

// HandleAdminTOTPDisableSubmit verifies the TOTP code and disables 2FA for the current admin.
func HandleAdminTOTPDisableSubmit(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		admin, err := deps.AdminRepo.FindByEmail(c.Context(), adminEmail)
		if err != nil {
			return redirectWithError(c, "/admin/security", "Admin bulunamadı.")
		}
		if !admin.TOTPEnabled {
			return redirectWithSuccess(c, "/admin/security", "2FA zaten devre dışı.")
		}
		code := strings.TrimSpace(c.FormValue("code"))
		if !totp.Validate(code, admin.TOTPSecret) {
			data := adminPageData(adminEmail, "security")
			data.AdminTOTPEnabled = true
			data.Error = "Kod hatalı. Lütfen tekrar deneyin."
			return c.Status(fiber.StatusUnprocessableEntity).Render("dealer/admin_2fa_disable", data, "dealer/layout")
		}
		if err := deps.AdminRepo.DisableTOTP(c.Context(), admin.ID); err != nil {
			return redirectWithError(c, "/admin/security", "2FA devre dışı bırakılamadı: "+err.Error())
		}
		return redirectWithSuccess(c, "/admin/security", "2FA başarıyla devre dışı bırakıldı.")
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
			fromFilter := strings.TrimSpace(c.Query("from"))
			toFilter := strings.TrimSpace(c.Query("to"))
			hashFilter := strings.TrimSpace(c.Query("hash"))
			data.AdminDepositFromFilter = fromFilter
			data.AdminDepositToFilter = toFilter
			data.AdminDepositHashFilter = hashFilter
			rows, total, _ := deps.TransactionRepo.ListPageFiltered(c.Context(), page, limit, fromFilter, toFilter, hashFilter)
			data.AdminDeposits = dealerActivityViews(rows, deps.AssetRegistry)
			depositBase := buildDepositFilterURL(fromFilter, toFilter, hashFilter)
			data.AdminPagination = dealerPaginationView(page, limit, total, depositBase)

		case "withdrawals":
			rows, total, _ := deps.WithdrawalRepo.ListPage(c.Context(), page, limit)
			data.Withdrawals = dealerWithdrawalViews(rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/withdrawals")

		case "wallets":
			rows, total, _ := deps.WalletRepo.ListPage(c.Context(), page, limit)
			balanceMap := buildWalletBalanceMap(c.Context(), deps.TransactionRepo, deps.AssetRegistry)
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

		case "webhooks":
			statusFilter := strings.TrimSpace(c.Query("status"))
			data.AdminWebhookStatusFilter = statusFilter
			rows, total, _ := deps.WebhookDeliveryRepo.ListPage(c.Context(), page, limit, statusFilter)
			data.AdminWebhooks = dealerWebhookDeliveryViews(rows)
			webhookBase := "/admin/webhooks"
			if statusFilter != "" {
				webhookBase += "?status=" + statusFilter
			}
			data.AdminPagination = dealerPaginationView(page, limit, total, webhookBase)

		case "refunds":
			statusFilter := strings.TrimSpace(c.Query("status"))
			data.AdminRefundStatusFilter = statusFilter
			rows, total, _ := deps.RefundRepo.ListPage(c.Context(), page, limit, statusFilter)
			data.AdminRefunds = dealerRefundViews(rows)
			refundBase := "/admin/refunds"
			if statusFilter != "" {
				refundBase += "?status=" + statusFilter
			}
			data.AdminPagination = dealerPaginationView(page, limit, total, refundBase)

		case "sweep":
			wallets, _ := deps.WalletRepo.List(c.Context(), 200)
			data.WithdrawalWallets = dealerWalletViews(wallets)

		case "rescan":
			// Form-only panel.

		case "links":
			rows, total, _ := deps.ProductRepo.ListPage(c.Context(), page, limit)
			data.Products = dealerProductViews(c, rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/links")

		case "security":
			if admin, err := deps.AdminRepo.FindByEmail(c.Context(), adminEmail); err == nil {
				data.AdminTOTPEnabled = admin.TOTPEnabled
			}

		default: // overview
			recentRows, _, _ := deps.TransactionRepo.ListPage(c.Context(), 1, 8)
			data.AdminDeposits = dealerActivityViews(recentRows, deps.AssetRegistry)
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

func HandleAdminWebhookReplay(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if deps.WebhookDeliveryRepo == nil || deps.Notifier == nil {
			return redirectWithError(c, "/admin/webhooks", "Webhook replay servisi hazır değil.")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/webhooks", "Geçersiz webhook delivery.")
		}
		delivery, err := deps.WebhookDeliveryRepo.Find(c.Context(), id)
		if err != nil {
			return redirectWithError(c, "/admin/webhooks", "Webhook delivery bulunamadı.")
		}
		domainID := delivery.DomainID.String()
		domain, err := deps.DomainService.FindByID(types.DomainParams{
			Context:  c.Context(),
			DomainID: &domainID,
		})
		if err != nil {
			_ = deps.WebhookDeliveryRepo.MarkAttempt(c.Context(), id, false, err)
			return redirectWithError(c, "/admin/webhooks", "Domain bulunamadı: "+err.Error())
		}

		if delivery.PaymentID != nil {
			session, err := deps.PaymentRepo.FindByID(c.Context(), *delivery.PaymentID)
			if err != nil {
				_ = deps.WebhookDeliveryRepo.MarkAttempt(c.Context(), id, false, err)
				return redirectWithError(c, "/admin/webhooks", "Payment bulunamadı: "+err.Error())
			}
			if err := deps.Notifier.DeliverPayment(c.Context(), *domain, *session); err != nil {
				_ = deps.WebhookDeliveryRepo.MarkAttempt(c.Context(), id, false, err)
				return redirectWithError(c, "/admin/webhooks", "Replay başarısız: "+err.Error())
			}
			_ = deps.WebhookDeliveryRepo.MarkAttempt(c.Context(), id, true, nil)
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "success", "webhook_delivery", id.String(), "Payment webhook yeniden gönderildi.")
			return redirectWithSuccess(c, "/admin/webhooks", "Payment webhook yeniden gönderildi.")
		}

		if delivery.TransactionID != nil {
			txModel, err := deps.TransactionRepo.FindByID(c.Context(), *delivery.TransactionID)
			if err != nil {
				_ = deps.WebhookDeliveryRepo.MarkAttempt(c.Context(), id, false, err)
				return redirectWithError(c, "/admin/webhooks", "Transaction bulunamadı: "+err.Error())
			}
			if err := deps.Notifier.Deliver(c.Context(), *domain, *txModel); err != nil {
				_ = deps.WebhookDeliveryRepo.MarkAttempt(c.Context(), id, false, err)
				return redirectWithError(c, "/admin/webhooks", "Replay başarısız: "+err.Error())
			}
			_ = deps.WebhookDeliveryRepo.MarkAttempt(c.Context(), id, true, nil)
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "success", "webhook_delivery", id.String(), "Transaction webhook yeniden gönderildi.")
			return redirectWithSuccess(c, "/admin/webhooks", "Transaction webhook yeniden gönderildi.")
		}

		err = errors.New("delivery payment veya transaction referansı taşımıyor")
		_ = deps.WebhookDeliveryRepo.MarkAttempt(c.Context(), id, false, err)
		return redirectWithError(c, "/admin/webhooks", err.Error())
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
		approvedRequest, err := deps.WithdrawalRepo.ApproveWithTransfer(c.Context(), id, adminEmail, deps.LedgerRepo, func(locked *models.WithdrawalRequest) (string, error) {
			walletID := locked.WalletID.String()
			params := types.TransferParams{
				Context:   c.Context(),
				WalletID:  &walletID,
				Chain:     &locked.Chain,
				Token:     locked.Token,
				ToAddress: &locked.ToAddress,
				AmountRaw: &locked.AmountRaw,
			}
			if err := params.ValidateWithdraw(); err != nil {
				return "", err
			}
			result, err := ExecuteWalletTransfer(deps.WalletRepo, deps.Blockchains, params, false)
			if err != nil {
				return "", err
			}
			return result.TxHash, nil
		})
		if err != nil {
			if approvedRequest != nil && approvedRequest.Status == models.WithdrawalStatusProcessing {
				return redirectWithError(c, "/admin/withdrawals", "Transfer gönderildi ancak ledger güncellenemedi: "+err.Error())
			}
			return redirectWithError(c, "/admin/withdrawals", "Transfer başarısız: "+err.Error())
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

func HandleAdminRefundApprove(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if deps.RefundRepo == nil || deps.PaymentRepo == nil || deps.TransactionRepo == nil {
			return redirectWithError(c, "/admin/refunds", "Refund altyapısı hazır değil.")
		}

		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/refunds", "Geçersiz refund.")
		}
		refund, err := deps.RefundRepo.Find(c.Context(), id)
		if err != nil || refund.Status != models.RefundStatusPending {
			return redirectWithError(c, "/admin/refunds", "Pending refund bulunamadı.")
		}

		session, err := deps.PaymentRepo.FindByID(c.Context(), refund.PaymentID)
		if err != nil || session.Status != models.PaymentStatusPaid {
			return redirectWithError(c, "/admin/refunds", "Paid payment bulunamadı.")
		}
		if session.MerchantID != refund.MerchantID || session.DomainID != refund.DomainID {
			return redirectWithError(c, "/admin/refunds", "Refund payment merchant/domain ile eşleşmiyor.")
		}
		if session.SelectedChainID == nil || !constants.IsSupportedChainID(*session.SelectedChainID) {
			return redirectWithError(c, "/admin/refunds", "Payment chain bilgisi eksik veya desteklenmiyor.")
		}
		if session.TxUniqueHash == nil || strings.TrimSpace(*session.TxUniqueHash) == "" {
			return redirectWithError(c, "/admin/refunds", "Payment için orijinal deposit transaction bulunamadı.")
		}

		txModel, err := deps.TransactionRepo.FindByUniqueHash(c.Context(), *session.TxUniqueHash)
		if err != nil {
			return redirectWithError(c, "/admin/refunds", "Orijinal deposit transaction okunamadı: "+err.Error())
		}
		toAddress := strings.TrimSpace(txModel.FromAddress)
		if toAddress == "" {
			return redirectWithError(c, "/admin/refunds", "Refund hedef adresi bulunamadı.")
		}

		walletID := session.WalletID.String()
		chain := constants.ChainName(*session.SelectedChainID)
		claimedRefund, err := deps.RefundRepo.ClaimPending(c.Context(), id, adminEmail)
		if err != nil {
			return redirectWithError(c, "/admin/refunds", "Refund başka bir işlem tarafından alınmış veya artık pending değil.")
		}
		amountRaw := claimedRefund.AmountRaw
		params := types.TransferParams{
			Context:   c.Context(),
			WalletID:  &walletID,
			Chain:     &chain,
			ToAddress: &toAddress,
			AmountRaw: &amountRaw,
		}
		if err := params.ValidateWithdraw(); err != nil {
			_ = deps.RefundRepo.MarkFailed(c.Context(), id, adminEmail, err.Error())
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "refund.approve", "failed", "refund", id.String(), err.Error())
			return redirectWithError(c, "/admin/refunds", err.Error())
		}

		result, err := ExecuteWalletTransfer(deps.WalletRepo, deps.Blockchains, params, false)
		if err != nil {
			_ = deps.RefundRepo.MarkFailed(c.Context(), id, adminEmail, err.Error())
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "refund.approve", "failed", "refund", id.String(), err.Error())
			return redirectWithError(c, "/admin/refunds", "Refund transfer başarısız: "+err.Error())
		}
		if err := deps.RefundRepo.RecordBroadcast(c.Context(), id, adminEmail, result.TxHash); err != nil {
			return redirectWithError(c, "/admin/refunds", "Refund transfer gönderildi ancak tx hash kaydedilemedi: "+err.Error())
		}
		if err := deps.RefundRepo.MarkSucceededWithLedger(c.Context(), id, adminEmail, result.TxHash, *session, deps.LedgerRepo); err != nil {
			_ = deps.RefundRepo.SetProcessingError(c.Context(), id, "ledger/finalize failed: "+err.Error())
			return redirectWithError(c, "/admin/refunds", "Refund transfer gönderildi ancak ledger/status güncellenemedi: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "refund.approve", "success", "refund", id.String(), "Refund gönderildi. Tx: "+result.TxHash)
		return redirectWithSuccess(c, "/admin/refunds", "Refund onaylandı ve transfer gönderildi.")
	}
}

func HandleAdminRefundReject(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if deps.RefundRepo == nil {
			return redirectWithError(c, "/admin/refunds", "Refund altyapısı hazır değil.")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/refunds", "Geçersiz refund.")
		}
		reason := strings.TrimSpace(c.FormValue("reason"))
		if reason == "" {
			reason = "Admin tarafından reddedildi."
		}
		if err := deps.RefundRepo.MarkRejected(c.Context(), id, adminEmail, reason); err != nil {
			return redirectWithError(c, "/admin/refunds", "Refund reddedilemedi: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "refund.reject", "success", "refund", id.String(), reason)
		return redirectWithSuccess(c, "/admin/refunds", "Refund talebi reddedildi.")
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
		ctx, cancel := context.WithTimeout(c.Context(), 15*time.Second)
		defer cancel()

		oauthConfig, _, err := oidcOAuthConfig(ctx)
		if err != nil {
			data := dealerPageData("OIDC yapılandırması eksik", "login")
			data.Error = err.Error()
			return c.Status(fiber.StatusNotImplemented).Render("dealer/oidc_missing", data, "dealer/layout")
		}
		state := uuid.NewString()
		nonce := uuid.NewString()
		setOIDCCookie(c, oidcStateCookie, state)
		setOIDCCookie(c, oidcNonceCookie, nonce)

		options := []oauth2.AuthCodeOption{oidc.Nonce(nonce)}
		if prompt := strings.TrimSpace(os.Getenv("OIDC_PROMPT")); prompt != "" {
			options = append(options, oauth2.SetAuthURLParam("prompt", prompt))
		}
		return c.Redirect().To(oauthConfig.AuthCodeURL(state, options...))
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
func HandleOIDCCallback(service *services.MerchantService, activityRepo *repositories.ActivityLogRepo, deps ...DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		code := strings.TrimSpace(c.Query("code"))
		state := strings.TrimSpace(c.Query("state"))
		expectedState := strings.TrimSpace(c.Cookies(oidcStateCookie))
		expectedNonce := strings.TrimSpace(c.Cookies(oidcNonceCookie))
		clearOIDCCookie(c, oidcStateCookie)
		clearOIDCCookie(c, oidcNonceCookie)
		if code == "" || state == "" || expectedState == "" || !hmac.Equal([]byte(state), []byte(expectedState)) {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", "OIDC state doğrulaması başarısız.")
			return redirectWithError(c, "/dealer/login", "OIDC oturum doğrulaması başarısız.")
		}
		if expectedNonce == "" {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", "OIDC nonce cookie bulunamadı.")
			return redirectWithError(c, "/dealer/login", "OIDC nonce doğrulaması başarısız.")
		}

		ctx, cancel := context.WithTimeout(c.Context(), 20*time.Second)
		defer cancel()
		oauthConfig, provider, err := oidcOAuthConfig(ctx)
		if err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", err.Error())
			return redirectWithError(c, "/dealer/login", "OIDC yapılandırması eksik: "+err.Error())
		}

		token, err := oauthConfig.Exchange(ctx, code)
		if err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", err.Error())
			return redirectWithError(c, "/dealer/login", "OIDC token alınamadı: "+err.Error())
		}
		rawIDToken, ok := token.Extra("id_token").(string)
		if !ok || strings.TrimSpace(rawIDToken) == "" {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", "OIDC id_token dönmedi.")
			return redirectWithError(c, "/dealer/login", "OIDC id_token dönmedi.")
		}
		idToken, err := provider.Verifier(&oidc.Config{ClientID: oauthConfig.ClientID}).Verify(ctx, rawIDToken)
		if err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", err.Error())
			return redirectWithError(c, "/dealer/login", "OIDC id_token doğrulanamadı: "+err.Error())
		}
		if !hmac.Equal([]byte(idToken.Nonce), []byte(expectedNonce)) {
			logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", "OIDC nonce doğrulaması başarısız.")
			return redirectWithError(c, "/dealer/login", "OIDC nonce doğrulaması başarısız.")
		}
		if idToken.AccessTokenHash != "" && token.AccessToken != "" {
			if err := idToken.VerifyAccessToken(token.AccessToken); err != nil {
				logDealerActivity(c, activityRepo, nil, "dealer", "", "dealer.oidc_callback", "failed", "auth", "", err.Error())
				return redirectWithError(c, "/dealer/login", "OIDC access token doğrulanamadı: "+err.Error())
			}
		}

		userInfo, err := oidcUserFromToken(ctx, provider, token, idToken)
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
		if len(deps) > 0 {
			_ = provisionMerchantReserve(c.Context(), merchant.ID, deps[0])
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

func oidcOAuthConfig(ctx context.Context) (*oauth2.Config, *oidc.Provider, error) {
	authority := oidcAuthority()
	clientID := strings.TrimSpace(os.Getenv("OIDC_CLIENT_ID"))
	redirectURI := strings.TrimSpace(os.Getenv("OIDC_REDIRECT_URI"))
	if authority == "" || clientID == "" || redirectURI == "" {
		return nil, nil, errors.New("OIDC_AUTHORITY, OIDC_CLIENT_ID veya OIDC_REDIRECT_URI eksik")
	}

	provider, err := oidc.NewProvider(ctx, authority)
	if err != nil {
		return nil, nil, fmt.Errorf("OIDC provider discovery başarısız: %w", err)
	}

	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
		RedirectURL:  redirectURI,
		Endpoint:     provider.Endpoint(),
		Scopes:       oidcScopesList(),
	}, provider, nil
}

func oidcUserFromToken(ctx context.Context, provider *oidc.Provider, token *oauth2.Token, idToken *oidc.IDToken) (*oidcUserInfo, error) {
	var claims oidcUserInfo
	if idToken != nil {
		if err := idToken.Claims(&claims); err != nil {
			return nil, err
		}
		if claims.Sub == "" {
			claims.Sub = idToken.Subject
		}
	}

	if provider != nil && token != nil && token.AccessToken != "" {
		userInfo, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
		if err == nil {
			if claims.Sub == "" {
				claims.Sub = userInfo.Subject
			}
			if claims.Email == "" {
				claims.Email = userInfo.Email
			}
			if !bool(claims.EmailVerified) {
				claims.EmailVerified = flexibleBool(userInfo.EmailVerified)
			}
			var extraClaims oidcUserInfo
			if err := userInfo.Claims(&extraClaims); err == nil {
				if claims.Name == "" {
					claims.Name = extraClaims.Name
				}
				if claims.Email == "" {
					claims.Email = extraClaims.Email
				}
				if claims.Sub == "" {
					claims.Sub = extraClaims.Sub
				}
			}
		} else if strings.TrimSpace(claims.Email) == "" {
			return nil, err
		}
	}

	claims.Email = strings.ToLower(strings.TrimSpace(claims.Email))
	claims.Name = strings.TrimSpace(claims.Name)
	claims.Sub = strings.TrimSpace(claims.Sub)
	if claims.Email == "" {
		return nil, errors.New("OIDC email bilgisi dönmedi")
	}
	return &claims, nil
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
		TransactionsURL:  "/dealer/dashboard/transactions",
		UsersURL:         "/dealer/dashboard/users",
		WithdrawalsURL:   "/dealer/dashboard/withdrawals",
		RescanURL:        "/dealer/dashboard/rescan",
		DomainsPanelURL:  "/dealer/dashboard/domains",
		ProductsURL:      "/dealer/products",
		InvoicesURL:      "/dealer/invoices",
		ProductsPanelURL: "/dealer/dashboard/products",
		SettingsPanelURL: "/dealer/dashboard/settings",
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
	case "transactions":
		return "transactions"
	case "withdrawals":
		return "withdrawals"
	case "rescan":
		return "rescan"
	case "domains":
		return "domains"
	case "products":
		return "products"
	case "users":
		return "users"
	case "settings":
		return "settings"
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
	case "/admin/webhooks":
		return "webhooks"
	case "/admin/refunds":
		return "refunds"
	case "/admin/rescan":
		return "rescan"
	case "/admin/sweep":
		return "sweep"
	case "/admin/admins":
		return "admins"
	case "/admin/security":
		return "security"
	case "/admin/links":
		return "links"
	default:
		return "overview"
	}
}

func adminPageData(adminEmail string, panel string) DealerPageData {
	data := DealerPageData{
		Title:         "Admin paneli",
		Active:        "admin",
		HasSession:    true,
		MerchantEmail: adminEmail,
		LogoutURL:     "/admin/logout",

		AdminPanel:          panel,
		AdminOverviewURL:    "/admin",
		AdminMerchantsURL:   "/admin/merchants",
		AdminPaymentsURL:    "/admin/payments",
		AdminDepositsURL:    "/admin/deposits",
		AdminWithdrawalsURL: "/admin/withdrawals",
		AdminWalletsURL:     "/admin/wallets",
		AdminActivityURL:    "/admin/activity",
		AdminSweepURL:       "/admin/sweep",
		AdminSecurityURL:    "/admin/security",
		AdminLinksURL:       "/admin/links",
		AdminWebhooksURL:    "/admin/webhooks",
		AdminRefundsURL:     "/admin/refunds",
		AdminRescanURL:      "/admin/rescan",
	}
	return data
}

func dealerWebhookDeliveryViews(rows []models.WebhookDelivery) []DealerWebhookDeliveryView {
	views := make([]DealerWebhookDeliveryView, 0, len(rows))
	for _, row := range rows {
		deliveredAt := ""
		if row.DeliveredAt != nil {
			deliveredAt = formatPanelTime(*row.DeliveredAt)
		}
		views = append(views, DealerWebhookDeliveryView{
			ID:          row.ID.String(),
			EventID:     row.EventID,
			EventType:   row.EventType,
			TargetURL:   row.TargetURL,
			Status:      row.Status,
			Attempts:    row.Attempts,
			LastError:   row.LastError,
			CreatedAt:   formatPanelTime(row.CreatedAt),
			UpdatedAt:   formatPanelTime(row.UpdatedAt),
			DeliveredAt: deliveredAt,
		})
	}
	return views
}

func dealerRefundViews(rows []models.Refund) []DealerRefundView {
	views := make([]DealerRefundView, 0, len(rows))
	for _, row := range rows {
		views = append(views, DealerRefundView{
			ID:          row.ID.String(),
			PaymentID:   row.PaymentID.String(),
			MerchantID:  row.MerchantID.String(),
			DomainID:    row.DomainID.String(),
			AmountRaw:   row.AmountRaw,
			Reason:      row.Reason,
			Status:      row.Status,
			TxHash:      row.TxHash,
			Error:       row.Error,
			RequestedBy: row.RequestedBy,
			CreatedAt:   formatPanelTime(row.CreatedAt),
		})
	}
	return views
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
			Merchant:    product.Merchant.Name,
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
		base := baseURL(c)
		checkoutURL := base + "/checkout/" + payment.SessionToken
		invoiceURL := base + "/invoice/" + payment.SessionToken
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
			InvoiceURL:     invoiceURL,
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
		domainLabel := wallet.Domain.DomainURL
		if domainLabel == "" {
			domainLabel = shortText(wallet.DomainID.String(), 8, 6)
		}
		views = append(views, DealerWalletView{
			ID:            wallet.ID.String(),
			ShortID:       shortText(wallet.ID.String(), 8, 6),
			Merchant:      merchant,
			Label:         walletLabel(wallet),
			ProductID:     emptyDash(wallet.ProductID),
			UserID:        emptyDash(wallet.UserID),
			Domain:        domainLabel,
			CreatedAt:     formatPanelTime(wallet.CreatedAt),
			Addresses:     walletAddressViews(wallet),
			MissingChains: missing,
		})
	}
	return views
}

// buildWalletBalanceMap returns a map of walletID -> balance rows (one per chain+symbol).
func buildWalletBalanceMap(ctx context.Context, txRepo *repositories.TransactionRepo, registry *asset.Registry) map[uuid.UUID][]DealerWalletBalanceRow {
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
			LogoURL:   registryLogoURL(registry, r.Symbol),
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
		{"ARB", "arbitrum", wallet.ArbitrumAddress},
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

func dealerBalanceViews(summaries []models.DepositSummary, registry *asset.Registry) []DealerBalanceView {
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
			ChainLogoURL:  asset.ChainLogoURL(summary.ChainID),
			Symbol:        summary.Symbol,
			Token:         token,
			LogoURL:       registryLogoURL(registry, summary.Symbol),
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

func dealerLedgerBalanceViews(rows []repositories.LedgerBalanceRow, registry *asset.Registry) []DealerBalanceView {
	views := make([]DealerBalanceView, 0, len(rows))
	for _, row := range rows {
		if row.Account != models.LedgerAccountMerchantAvailable {
			continue
		}
		token := ""
		if row.Token != nil {
			token = *row.Token
		}
		views = append(views, DealerBalanceView{
			Chain:         chainLabel(constants.ChainID(row.ChainID)),
			ChainLogoURL:  asset.ChainLogoURL(constants.ChainID(row.ChainID)),
			Symbol:        row.Symbol,
			Token:         token,
			LogoURL:       registryLogoURL(registry, row.Symbol),
			AmountRaw:     row.BalanceRaw,
			AmountDisplay: formatTokenAmount(row.BalanceRaw, row.Decimals),
			Decimals:      row.Decimals,
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
			balance.LogoURL = registry.LogoURL(assetInfo.GetSymbol())
			balance.ChainLogoURL = asset.ChainLogoURL(assetInfo.GetChainID())
			views = append(views, balance)
			continue
		}
		views = append(views, DealerBalanceView{
			Chain:         chainLabel(assetInfo.GetChainID()),
			ChainLogoURL:  asset.ChainLogoURL(assetInfo.GetChainID()),
			Symbol:        assetInfo.GetSymbol(),
			Token:         token,
			LogoURL:       registry.LogoURL(assetInfo.GetSymbol()),
			AmountRaw:     "0",
			AmountDisplay: "0",
			Decimals:      assetInfo.GetDecimals(),
			DisplayToken:  emptyDash(token),
		})
	}
	return views
}

func filterBalancesBySettings(balances []DealerBalanceView, hideTestnets bool, hiddenChains string) []DealerBalanceView {
	hidden := parseHiddenChains(hiddenChains)
	out := balances[:0]
	for _, b := range balances {
		chainID := chainSlugToID(b.Chain)
		if hideTestnets && constants.IsTestnet(chainID) {
			continue
		}
		if hidden[b.Chain] {
			continue
		}
		out = append(out, b)
	}
	return out
}

func filterVaultsBySettings(vaults []DealerChainVaultView, hideTestnets bool, hiddenChains string) []DealerChainVaultView {
	hidden := parseHiddenChains(hiddenChains)
	out := vaults[:0]
	for _, v := range vaults {
		chainID := chainSlugToID(v.Chain)
		if hideTestnets && constants.IsTestnet(chainID) {
			continue
		}
		if hidden[v.Chain] {
			continue
		}
		out = append(out, v)
	}
	return out
}

func parseHiddenChains(s string) map[string]bool {
	m := make(map[string]bool)
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			m[p] = true
		}
	}
	return m
}

func chainSlugToID(slug string) constants.ChainID {
	slug = strings.ToLower(strings.TrimSpace(slug))
	aliases := map[constants.ChainID][]string{
		constants.Bitcoin:     {"bitcoin", "btc"},
		constants.Ethereum:    {"ethereum", "eth"},
		constants.Base:        {"base"},
		constants.Arbitrum:    {"arbitrum", "arb", "arbitrum-one"},
		constants.Binance:     {"bnbchain", "bnb chain", "bsc", "binance", "bnb"},
		constants.Unichain:    {"unichain", "uni"},
		constants.Avalanche:   {"avalanche", "avax"},
		constants.Chiliz:      {"chiliz", "chz"},
		constants.ChilizSpicy: {"chiliz-spicy", "chiliz spicy", "spicy"},
		constants.Solana:      {"solana", "sol"},
		constants.TRON:        {"tron", "trx"},
	}
	for id, values := range aliases {
		for _, value := range values {
			if value == slug {
				return id
			}
		}
		if strings.EqualFold(constants.ChainName(id), slug) {
			return id
		}
	}
	return -1
}

func enrichBalancesWithUSD(ctx context.Context, balances []DealerBalanceView, oracle pricing.PriceOracle) {
	if oracle == nil {
		return
	}
	cache := make(map[string]string)
	for i := range balances {
		sym := balances[i].Symbol
		if _, ok := cache[sym]; !ok {
			price, err := oracle.Price(ctx, sym, "USD")
			if err != nil || price == nil {
				cache[sym] = ""
				continue
			}
			amtF := parseTokenFloat(balances[i].AmountDisplay)
			pf, _ := price.Float64()
			usd := amtF * pf
			if usd > 0 {
				cache[sym] = fmt.Sprintf("$%.2f", usd)
			} else {
				cache[sym] = ""
			}
		}
		balances[i].AmountUSD = cache[sym]
	}
}

func parseTokenFloat(display string) float64 {
	display = strings.TrimSpace(display)
	if display == "" || display == "0" {
		return 0
	}
	f, err := strconv.ParseFloat(display, 64)
	if err != nil {
		return 0
	}
	return f
}

func balanceKey(chain string, symbol string, token string) string {
	return strings.ToLower(strings.TrimSpace(chain)) + "|" + strings.ToUpper(strings.TrimSpace(symbol)) + "|" + strings.ToLower(strings.TrimSpace(token))
}

func registryLogoURL(registry *asset.Registry, symbol string) string {
	if registry == nil {
		return ""
	}
	return registry.LogoURL(symbol)
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
			Chain:        chain,
			ChainLogoURL: asset.ChainLogoURL(chainID),
			Assets:       assets,
			Empty:        len(assets) == 0,
		}
		for _, asset := range assets {
			view.Deposits += asset.Deposits
			view.Users += asset.Users
		}
		views = append(views, view)
	}
	return views
}

func dealerActivityViews(transactions []models.Transaction, registry *asset.Registry) []DealerActivityView {
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
			ChainLogoURL:    asset.ChainLogoURL(tx.ChainID),
			Symbol:          tx.Symbol,
			LogoURL:         registryLogoURL(registry, tx.Symbol),
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
	case constants.Arbitrum:
		return "Arbitrum"
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
	return strings.Join(oidcScopesList(), " ")
}

func oidcScopesList() []string {
	raw := strings.TrimSpace(os.Getenv("OIDC_SCOPES"))
	if raw == "" {
		raw = "openid profile email roles"
	}
	raw = strings.ReplaceAll(raw, ",", " ")
	parts := strings.Fields(raw)
	hasOpenID := false
	for _, scope := range parts {
		if scope == oidc.ScopeOpenID {
			hasOpenID = true
			break
		}
	}
	if !hasOpenID {
		parts = append([]string{oidc.ScopeOpenID}, parts...)
	}
	return parts
}

func oidcAuthority() string {

	for _, key := range []string{"OIDC_AUTHORITY", "OIDC_ISSUER_URL"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return ""

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

func buildDepositFilterURL(from, to, hash string) string {
	base := "/admin/deposits"
	params := []string{}
	if from != "" {
		params = append(params, "from="+url.QueryEscape(from))
	}
	if to != "" {
		params = append(params, "to="+url.QueryEscape(to))
	}
	if hash != "" {
		params = append(params, "hash="+url.QueryEscape(hash))
	}
	if len(params) > 0 {
		return base + "?" + strings.Join(params, "&")
	}
	return base
}
