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
	webhooksvc "core/services/webhook"
	"core/types"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

const dealerSessionCookie = "dealer_session"
const adminSessionCookie = "admin_session"
const adminSessionEmailLocal = "admin_session_email"
const adminPendingTOTPCookie = "admin_totp_pending" // temp: holds admin ID awaiting 2FA
const adminSetupTOTPCookie = "admin_totp_setup"     // temp: holds admin ID during TOTP setup
const oidcStateCookie = "oidc_state"
const oidcNonceCookie = "oidc_nonce"
const oidcPortalCookie = "oidc_portal"
const flashSuccessCookie = "flash_success"
const flashErrorCookie = "flash_error"
const flashDebugCookie = "flash_debug"

const adminSessionDefaultTTL = 8 * time.Hour
const adminSessionRememberTTL = 30 * 24 * time.Hour
const adminPendingTOTPTTL = 5 * time.Minute
const adminSetupTOTPTTL = 10 * time.Minute
const oidcPortalMerchant = "merchant"
const oidcPortalAdmin = "admin"

var runtimeDealerSessionSecret = "runtime-session-secret-" + uuid.NewString()

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
	DeliverRaw(ctx context.Context, domain models.Domain, eventType, eventID, eventVersion string, body []byte) error
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
	AdminAssets       []DealerAssetOption

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
	AdminTestDepositURL string
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
	AdminLoginEmail          string
	AdminRememberMe          bool
	OIDCDebug                string
	TOTPSecret               string
	TOTPQRDataURL            htmltemplate.URL
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
	MerchantID    string
	Merchant      string
	Label         string
	ProductID     string
	UserID        string
	OwnerRef      string
	DomainID      string
	Domain        string
	WalletKind    string
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
	ID                 string
	EventID            string
	EventType          string
	TargetURL          string
	Status             string
	Attempts           uint
	LastError          string
	FailureCategory    string
	NextRetryAt        string
	NextAction         string
	OriginalDeliveryID string
	ReplayCount        uint
	ReplayRequestedBy  string
	ReplayRequestedAt  string
	CreatedAt          string
	UpdatedAt          string
	DeliveredAt        string
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

type DealerAssetOption struct {
	Value    string
	Label    string
	Chain    string
	Symbol   string
	Token    string
	Decimals uint8
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
	Sub           string              `json:"sub"`
	Email         string              `json:"email"`
	EmailVerified flexibleBool        `json:"email_verified"`
	Name          string              `json:"name"`
	Roles         stringList          `json:"roles"`
	Role          stringList          `json:"role"`
	RoleURI       stringList          `json:"http://schemas.microsoft.com/ws/2008/06/identity/claims/role"`
	Groups        stringList          `json:"groups"`
	Permissions   stringList          `json:"permissions"`
	RoleSources   map[string][]string `json:"-"`
}

type adminSessionPayloadData struct {
	Email     string `json:"email"`
	ExpiresAt int64  `json:"expires_at"`
}

type adminTempSessionPayloadData struct {
	AdminID    string `json:"admin_id"`
	RememberMe bool   `json:"remember_me"`
}

type flexibleBool bool

type stringList []string

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

func (s *stringList) UnmarshalJSON(data []byte) error {
	value := strings.TrimSpace(string(data))
	if value == "" || value == "null" {
		*s = nil
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*s = normalizeStringList(list)
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = normalizeStringList(strings.FieldsFunc(single, func(r rune) bool {
			return r == ',' || r == ' '
		}))
		return nil
	}
	return nil
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
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

// HandleDealerLogin renders the merchant portal OIDC login page.
// @Summary Show merchant login
// @Description Renders the hosted merchant portal login page with the OIDC sign-in action.
// @Tags Merchant Portal
// @Produce html
// @Success 200 {string} string "HTML merchant login page"
// @Router /merchant/login [get]
func HandleDealerLogin() fiber.Handler {
	return func(c fiber.Ctx) error {
		data := dealerPageData("Üye işyeri girişi", "login")
		applyFlash(c, &data)
		return c.Render("dealer/login", data, "dealer/layout")
	}
}

// HandleDealerLoginSubmit authenticates a merchant with email and password.
// @Summary Merchant email login
// @Description Authenticates a merchant with email and password, sets a merchant portal session cookie, and redirects to onboarding.
// @Tags Merchant Portal
// @Accept x-www-form-urlencoded
// @Produce html
// @Param email formData string true "Merchant email"
// @Param password formData string true "Password"
// @Success 302 {string} string "Redirect to merchant onboarding"
// @Failure 400 {string} string "HTML login page with validation error"
// @Failure 401 {string} string "HTML login page with authentication error"
// @Router /merchant/login [post]
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
			data := dealerPageData("Üye işyeri girişi", "login")
			data.Error = "E-posta ve şifre zorunlu."
			return c.Status(fiber.StatusBadRequest).Render("dealer/login", data, "dealer/layout")
		}

		merchant, err := service.Authenticate(params)
		if err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", email, "dealer.login", "failed", "auth", "", "E-posta veya şifre hatalı.")
			data := dealerPageData("Üye işyeri girişi", "login")
			data.Error = "E-posta veya şifre hatalı."
			return c.Status(fiber.StatusUnauthorized).Render("dealer/login", data, "dealer/layout")
		}

		logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "dealer.login", "success", "merchant", merchant.ID.String(), "Üye işyeri e-posta ile giriş yaptı.")
		setDealerSessionCookie(c, merchant.ID.String())
		return c.Redirect().To("/merchant/dashboard")
	}
}

// HandleDealerRegister renders the merchant portal self-service registration page.
// @Summary Show merchant registration
// @Description Renders the hosted self-service merchant registration page.
// @Tags Merchant Portal
// @Produce html
// @Success 200 {string} string "HTML merchant registration page"
// @Router /merchant/register [get]
func HandleDealerRegister() fiber.Handler {
	return func(c fiber.Ctx) error {
		data := dealerPageData("Üye işyeri kaydı", "register")
		applyFlash(c, &data)
		return c.Render("dealer/register", data, "dealer/layout")
	}
}

// HandleDealerRegisterSubmit creates a merchant from the self-service HTML form.
// @Summary Create merchant from form
// @Description Creates a merchant account from the hosted self-service registration page and redirects to onboarding.
// @Tags Merchant Portal
// @Accept x-www-form-urlencoded
// @Produce html
// @Param name formData string true "Merchant name"
// @Param email formData string true "Merchant email"
// @Param email_repeat formData string true "Merchant email confirmation"
// @Param password formData string true "Password"
// @Param password_repeat formData string true "Password confirmation"
// @Success 302 {string} string "Redirect to merchant onboarding"
// @Failure 400 {string} string "HTML registration page with validation error"
// @Failure 500 {string} string "HTML registration page with server error"
// @Router /merchant/register [post]
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
			data := dealerPageData("Üye işyeri kaydı", "register")
			data.Error = err.Error()
			return c.Status(fiber.StatusBadRequest).Render("dealer/register", data, "dealer/layout")
		}

		merchant, err := deps.MerchantService.Create(params)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "dealer", email, "dealer.register", "failed", "merchant", "", err.Error())
			data := dealerPageData("Üye işyeri kaydı", "register")
			data.Error = "Üye işyeri kaydı oluşturulamadı: " + err.Error()
			return c.Status(fiber.StatusInternalServerError).Render("dealer/register", data, "dealer/layout")
		}

		_ = provisionMerchantReserve(c.Context(), merchant.ID, deps)

		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "dealer.register", "success", "merchant", merchant.ID.String(), "Üye işyeri hesabı self servis kayıt ile oluşturuldu.")
		setDealerSessionCookie(c, merchant.ID.String())
		return redirectWithSuccess(c, "/merchant/dashboard", "Üye işyeri hesabı oluşturuldu.")
	}
}

// HandleDealerDashboard renders the authenticated merchant portal.
// @Summary Show merchant dashboard
// @Description Renders the authenticated merchant portal with merchant info, domain creation form, and current domains.
// @Tags Merchant Portal
// @Produce html
// @Success 200 {string} string "HTML merchant dashboard"
// @Failure 302 {string} string "Redirect to merchant login"
// @Router /merchant/dashboard [get]
func HandleDealerDashboard(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}

		domains, err := deps.DomainService.ListByMerchant(c.Context(), merchant.ID)
		if err != nil {
			data := dealerPageData("Üye işyeri paneli", "dashboard")
			fillDealerMerchant(&data, merchant)
			data.Error = "Domain listesi okunamadı: " + err.Error()
			return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
		}
		withdrawals, err := deps.WithdrawalRepo.ListByMerchant(c.Context(), merchant.ID, 100)
		if err != nil {
			data := dealerPageData("Üye işyeri paneli", "dashboard")
			fillDealerMerchant(&data, merchant)
			data.Error = "Çekim talepleri okunamadı: " + err.Error()
			return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
		}
		transactions, err := deps.TransactionRepo.ListByMerchant(c.Context(), merchant.ID, 100)
		if err != nil {
			data := dealerPageData("Üye işyeri paneli", "dashboard")
			fillDealerMerchant(&data, merchant)
			data.Error = "İşlem geçmişi okunamadı: " + err.Error()
			return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
		}
		var products []models.Product
		if deps.ProductRepo != nil {
			products, err = deps.ProductRepo.ListByMerchant(c.Context(), merchant.ID, 100)
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
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
				data := dealerPageData("Üye işyeri paneli", "dashboard")
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
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Ledger bakiyesi okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		if len(ledgerBalances) == 0 {
			balances, err = deps.TransactionRepo.MerchantDepositSummary(c.Context(), merchant.ID)
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Bakiye özeti okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		var auditLogs []models.ActivityLog
		if deps.ActivityLogRepo != nil {
			auditLogs, err = deps.ActivityLogRepo.ListByMerchant(c.Context(), merchant.ID, 100)
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
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

		data := dealerPageData("Üye işyeri paneli", "dashboard")
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
		usersBaseURL := "/merchant/dashboard/users"
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

// HandleDealerDomainCreate creates a domain from the authenticated merchant portal.
// @Summary Create merchant domain from panel
// @Description Creates a merchant domain using the authenticated merchant session and redirects back to the dashboard.
// @Tags Merchant Portal
// @Accept x-www-form-urlencoded
// @Produce html
// @Param domain_url formData string true "Domain URL"
// @Param webhook_url formData string true "Webhook URL"
// @Param webhook_secret formData string true "Webhook secret"
// @Success 302 {string} string "Redirect to merchant dashboard"
// @Failure 302 {string} string "Redirect to merchant login or dashboard with error"
// @Router /merchant/domains [post]
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
			return redirectWithError(c, "/merchant/dashboard", err.Error())
		}
		if err := helpers.ValidateWebhookURL(webhookURL); err != nil {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "failed", "domain", domainURL, err.Error())
			return redirectWithError(c, "/merchant/dashboard", "Geçersiz webhook URL: "+err.Error())
		}
		domain, err := domainService.Create(params)
		if err != nil {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "failed", "domain", domainURL, err.Error())
			return redirectWithError(c, "/merchant/dashboard", "Domain eklenemedi: "+err.Error())
		}
		subjectID := domainURL
		if domain != nil {
			subjectID = domain.ID.String()
		}
		logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "success", "domain", subjectID, "Domain ve webhook endpoint oluşturuldu.")
		return redirectWithSuccess(c, "/merchant/dashboard", "Domain eklendi.")
	}
}

func HandleDealerProductCreate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}
		if deps.ProductRepo == nil {
			return redirectWithError(c, "/merchant/dashboard/products", "Product repository hazır değil.")
		}

		domainIDRaw := strings.TrimSpace(c.FormValue("domain_id"))
		domainID, err := uuid.Parse(domainIDRaw)
		if err != nil {
			return redirectWithError(c, "/merchant/dashboard/products", "Geçerli domain seçmelisin.")
		}
		domainIDString := domainID.String()
		domain, err := deps.DomainService.FindByID(types.DomainParams{
			Context:  c.Context(),
			DomainID: &domainIDString,
		})
		if err != nil || domain.MerchantID != merchant.ID {
			return redirectWithError(c, "/merchant/dashboard/products", "Domain bulunamadı.")
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
			return redirectWithError(c, "/merchant/dashboard/products", "Ürün adı zorunlu.")
		}
		if err := types.ValidatePositiveDecimal(amount); err != nil {
			return redirectWithError(c, "/merchant/dashboard/products", "Tutar pozitif decimal olmalı.")
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
			return redirectWithError(c, "/merchant/dashboard/products", "Ürün oluşturulamadı: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "product.create", "success", "product", product.ID.String(), "Payment link ürünü oluşturuldu.")
		link := baseURL(c) + "/payment-links/" + product.LinkToken
		return redirectWithSuccess(c, "/merchant/dashboard/products", "Payment link oluşturuldu: "+link)
	}
}

func HandleDealerInvoiceCreate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}
		if deps.PaymentRepo == nil || deps.WalletRepo == nil {
			return redirectWithError(c, "/merchant/dashboard/products", "Invoice altyapısı hazır değil.")
		}

		domainIDRaw := strings.TrimSpace(c.FormValue("domain_id"))
		domainID, err := uuid.Parse(domainIDRaw)
		if err != nil {
			return redirectWithError(c, "/merchant/dashboard/products", "Geçerli domain seçmelisin.")
		}
		domainIDString := domainID.String()
		domain, err := deps.DomainService.FindByID(types.DomainParams{
			Context:  c.Context(),
			DomainID: &domainIDString,
		})
		if err != nil || domain.MerchantID != merchant.ID {
			return redirectWithError(c, "/merchant/dashboard/products", "Domain bulunamadı.")
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
			return redirectWithError(c, "/merchant/dashboard/products", "Invoice oluşturulamadı: "+err.Error())
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
			return redirectWithError(c, "/merchant/dashboard/products", "Wallet oluşturulamadı: "+err.Error())
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
			return redirectWithError(c, "/merchant/dashboard/products", "Invoice oluşturulamadı: "+err.Error())
		}

		checkoutURL := baseURL(c) + "/checkout/" + session.SessionToken
		invoiceURL := baseURL(c) + "/invoice/" + session.SessionToken
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "invoice.create", "success", "payment", session.ID.String(), "Dashboard invoice oluşturuldu.")
		return redirectWithSuccess(c, "/merchant/dashboard/products", "Invoice oluşturuldu: "+invoiceURL+" | Checkout: "+checkoutURL)
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
		walletUserID := orderID
		wallet, err := deps.WalletRepo.Create(types.WalletParams{
			Context:    c.Context(),
			MerchantId: &merchantID,
			DomainId:   &domainID,
			ProductId:  &productID,
			UserId:     &walletUserID,
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
			return redirectWithError(c, "/merchant/dashboard/treasury", "Geçersiz wallet.")
		}

		chain := strings.ToLower(strings.TrimSpace(c.FormValue("chain")))
		if chain == "" {
			return redirectWithError(c, "/merchant/dashboard/treasury", "Chain belirtilmeli.")
		}

		wallet, err := deps.WalletRepo.FindByID(c.Context(), walletID)
		if err != nil || wallet.MerchantID != merchant.ID {
			return redirectWithError(c, "/merchant/dashboard/treasury", "Wallet bulunamadı.")
		}

		_, err = deps.WalletRepo.FillChainAddress(c.Context(), walletID, chain, deps.Blockchains)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "wallet.fill_address", "failed", "wallet", walletID.String(), err.Error())
			return redirectWithError(c, "/merchant/dashboard/treasury", "Adres türetilemedi: "+err.Error())
		}

		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "wallet.fill_address", "success", "wallet", walletID.String(), chain+" adresi oluşturuldu.")
		return redirectWithSuccess(c, "/merchant/dashboard/treasury", chain+" adresi oluşturuldu.")
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
			return redirectWithError(c, "/merchant/dashboard", "Geçerli wallet seçmelisin.")
		}
		wallet, err := deps.WalletRepo.FindByID(c.Context(), walletID)
		if err != nil || wallet.MerchantID != merchant.ID {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, "Wallet bulunamadı veya merchant ile eşleşmedi.")
			return redirectWithError(c, "/merchant/dashboard", "Wallet bulunamadı.")
		}
		if wallet.HDAddressId != 0 {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, "Sadece reserve (HD index 0) cüzdandan çekim yapılabilir.")
			return redirectWithError(c, "/merchant/dashboard/withdrawals", "Çekim sadece üye işyeri reserve cüzdanından yapılabilir.")
		}

		chain := strings.ToLower(strings.TrimSpace(c.FormValue("chain")))
		symbol := strings.TrimSpace(c.FormValue("symbol"))
		tokenAddress := strings.TrimSpace(c.FormValue("token_address"))
		toAddress := strings.TrimSpace(c.FormValue("to_address"))
		amount := strings.TrimSpace(c.FormValue("amount"))
		if amount == "" {
			amount = strings.TrimSpace(c.FormValue("amount_raw"))
		}
		note := strings.TrimSpace(c.FormValue("note"))
		chain, token, symbol, decimals, err := resolveWithdrawalAsset(deps.AssetRegistry, chain, symbol, tokenAddress)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, err.Error())
			return redirectWithError(c, "/merchant/dashboard", err.Error())
		}
		if err := types.ValidatePositiveDecimal(amount); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, err.Error())
			return redirectWithError(c, "/merchant/dashboard/withdrawals", "Tutar pozitif decimal olmalı.")
		}
		amountRaw, err := types.DecimalToRaw(amount, decimals)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, err.Error())
			return redirectWithError(c, "/merchant/dashboard/withdrawals", "Tutar geçersiz: "+err.Error())
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
			return redirectWithError(c, "/merchant/dashboard", err.Error())
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
			return redirectWithError(c, "/merchant/dashboard", "Çekim talebi oluşturulamadı: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "success", "withdrawal", request.ID.String(), "Çekim talebi admin onayına gönderildi.")
		return redirectWithSuccess(c, "/merchant/dashboard", "Çekim talebi admin onayına gönderildi.")
	}
}

// HandleDealerOnboarding renders the merchant onboarding result page.
// @Summary Show merchant onboarding
// @Description Renders the hosted onboarding page after a merchant is created.
// @Tags Merchant Portal
// @Produce html
// @Param merchant_id query string false "Merchant ID"
// @Param name query string false "Merchant name"
// @Param email query string false "Merchant email"
// @Success 200 {string} string "HTML merchant onboarding page"
// @Router /merchant/onboarding [get]
func HandleDealerOnboarding() fiber.Handler {
	return func(c fiber.Ctx) error {
		data := dealerPageData("Üye işyeri hesabı oluşturuldu", "register")
		data.MerchantID = c.Query("merchant_id")
		data.MerchantName = c.Query("name")
		data.MerchantEmail = c.Query("email")
		return c.Render("dealer/onboarding", data, "dealer/layout")
	}
}

func HandleDealerLogout(service *services.MerchantService, activityRepo *repositories.ActivityLogRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		if merchant, ok := requireDealerMerchant(c, service); ok {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "dealer.logout", "success", "merchant", merchant.ID.String(), "Üye işyeri oturumu kapattı.")
		}
		clearDealerSessionCookie(c)
		return redirectWithSuccess(c, "/merchant/login", "Oturum kapatıldı.")
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
			return redirectWithError(c, "/merchant/dashboard/domains", "Webhook URL ve secret boş olamaz")
		}
		if err := helpers.ValidateWebhookURL(webhookURL); err != nil {
			return redirectWithError(c, "/merchant/dashboard/domains", "Geçersiz webhook URL: "+err.Error())
		}

		if err := deps.DomainService.UpdateWebhook(c.Context(), domainUUID, merchant.ID, webhookURL, webhookSecret); err != nil {
			return redirectWithError(c, "/merchant/dashboard/domains", "Güncelleme hatası: "+err.Error())
		}

		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.update_webhook", "success", "domain", domainIDStr, "Webhook URL ve secret güncellendi.")
		return redirectWithSuccess(c, "/merchant/dashboard/domains", "Webhook başarıyla güncellendi.")
	}
}

// HandleDealerDomainRotateAPISecret rotates the API secret for a merchant-owned domain.
// @Summary Rotate domain API secret
// @Description Rotates the API secret for an authenticated merchant domain. The new secret is returned once in the response.
// @Tags Merchant Portal
// @Produce json
// @Param id path string true "Domain ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} types.ErrorResponse
// @Failure 401 {object} types.ErrorResponse
// @Router /merchant/domains/{id}/rotate-api-secret [post]
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
			return redirectWithError(c, "/merchant/dashboard/settings", "Ayarlar kaydedilemedi: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "settings.update", "success", "merchant", merchant.ID.String(), "Görünüm ayarları güncellendi.")
		return redirectWithSuccess(c, "/merchant/dashboard/settings", "Ayarlar kaydedildi.")
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

func ensureDealerReserveWallet(ctx context.Context, merchantID uuid.UUID, deps DealerDeps) (*models.Wallet, error) {
	if deps.DomainService == nil || deps.WalletRepo == nil {
		return nil, errors.New("reserve wallet services are not ready")
	}
	wallet, err := deps.WalletRepo.FindReserveWallet(ctx, merchantID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		domain, createErr := deps.DomainService.CreateReserve(ctx, merchantID)
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

func HandleAdminLogin() fiber.Handler {
	return func(c fiber.Ctx) error {
		data := adminLoginPageData()
		applyFlash(c, &data)
		return c.Render("dealer/admin_login", data, "dealer/layout")
	}
}

func HandleAdminLoginSubmit(adminRepo *repositories.AdminRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		email := strings.TrimSpace(c.FormValue("email"))
		password := c.FormValue("password")
		rememberMe := adminRememberRequested(c)

		renderErr := func(msg string) error {
			data := adminLoginPageData()
			data.Error = msg
			data.AdminLoginEmail = email
			data.AdminRememberMe = rememberMe
			return c.Status(fiber.StatusUnauthorized).Render("dealer/admin_login", data, "dealer/layout")
		}

		admin, err := adminRepo.Authenticate(c.Context(), email, password)
		if err != nil {
			return renderErr("Admin bilgileri hatalı.")
		}
		return continueAdminLogin(c, admin, rememberMe)
	}
}

func continueAdminLogin(c fiber.Ctx, admin *models.Admin, rememberMe bool) error {
	if admin == nil {
		return redirectWithError(c, "/admin/login", "Admin bulunamadı.")
	}
	if admin.TOTPEnabled {
		setAdminTempCookie(c, adminPendingTOTPCookie, admin.ID, rememberMe, adminPendingTOTPTTL)
		return c.Redirect().To("/admin/2fa/verify")
	}
	setAdminTempCookie(c, adminSetupTOTPCookie, admin.ID, rememberMe, adminSetupTOTPTTL)
	return c.Redirect().To("/admin/2fa/setup")
}

func totpQRDataURL(otpauthURL string) htmltemplate.URL {
	png, err := qrcode.Encode(otpauthURL, qrcode.Medium, 256)
	if err != nil {
		return ""
	}
	return htmltemplate.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
}

func HandleAdminTOTPSetup(adminRepo *repositories.AdminRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminID, _, ok := verifyAdminTempLoginCookie(c, adminSetupTOTPCookie)
		if !ok {
			return redirectWithError(c, "/admin/login", "Oturum süresi doldu.")
		}
		admin, err := adminRepo.FindByID(c.Context(), adminID)
		if err != nil {
			return redirectWithError(c, "/admin/login", "Admin bulunamadı.")
		}

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
			if err := adminRepo.SaveTOTPSecret(c.Context(), adminID, secret); err != nil {
				return redirectWithError(c, "/admin/login", "2FA anahtarı kaydedilemedi: "+err.Error())
			}
		}

		qrURL := fmt.Sprintf(
			"otpauth://totp/Gateway%%20Admin:%s?secret=%s&issuer=Gateway%%20Admin",
			url.QueryEscape(admin.Email), secret,
		)
		qrDataURL := totpQRDataURL(qrURL)
		if qrDataURL == "" {
			return redirectWithError(c, "/admin/login", "2FA QR kodu oluşturulamadı.")
		}
		data := dealerPageData("2FA kurulum", "admin-2fa-setup")
		data.TOTPSecret = secret
		data.TOTPQRDataURL = qrDataURL
		data.MerchantEmail = admin.Email
		return c.Render("dealer/admin_2fa_setup", data, "dealer/layout")
	}
}

func HandleAdminTOTPSetupSubmit(adminRepo *repositories.AdminRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminID, rememberMe, ok := verifyAdminTempLoginCookie(c, adminSetupTOTPCookie)
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
			data.TOTPSecret = admin.TOTPSecret
			data.TOTPQRDataURL = totpQRDataURL(qrURL)
			return c.Render("dealer/admin_2fa_setup", data, "dealer/layout")
		}
		if err := adminRepo.EnableTOTPSecret(c.Context(), adminID, admin.TOTPSecret); err != nil {
			return redirectWithError(c, "/admin/login", "2FA etkinleştirilemedi: "+err.Error())
		}
		clearAdminTempCookie(c, adminSetupTOTPCookie)
		setAdminSessionCookie(c, admin.Email, rememberMe)
		return redirectWithSuccess(c, "/admin", "2FA başarıyla etkinleştirildi.")
	}
}

func HandleAdminTOTPVerify(adminRepo *repositories.AdminRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, _, ok := verifyAdminTempLoginCookie(c, adminPendingTOTPCookie)
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
		adminID, rememberMe, ok := verifyAdminTempLoginCookie(c, adminPendingTOTPCookie)
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
		clearAdminTempCookie(c, adminPendingTOTPCookie)
		setAdminSessionCookie(c, admin.Email, rememberMe)
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
		setAdminTempCookie(c, adminSetupTOTPCookie, admin.ID, false, adminSetupTOTPTTL)
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
	adminID, _, ok := verifyAdminTempLoginCookie(c, cookieName)
	return adminID, ok
}

func verifyAdminTempLoginCookie(c fiber.Ctx, cookieName string) (uuid.UUID, bool, bool) {
	val, err := verifyDealerSessionValue(c.Cookies(cookieName))
	if err != nil || val == "" {
		return uuid.Nil, false, false
	}
	id, rememberMe, err := parseAdminTempSessionPayload(val)
	if err != nil {
		return uuid.Nil, false, false
	}
	return id, rememberMe, true
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
			rows, total, err := deps.MerchantService.Repo().ListPage(c.Context(), page, limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Merchant listesi okunamadı", err)
			}
			data.AdminMerchants = dealerAdminMerchantViews(rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/merchants")

		case "payments":
			rows, total, err := deps.PaymentRepo.ListPage(c.Context(), page, limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Payment listesi okunamadı", err)
			}
			data.Payments = dealerPaymentViews(c, rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/payments")

		case "deposits":
			fromFilter := strings.TrimSpace(c.Query("from"))
			toFilter := strings.TrimSpace(c.Query("to"))
			hashFilter := strings.TrimSpace(c.Query("hash"))
			data.AdminDepositFromFilter = fromFilter
			data.AdminDepositToFilter = toFilter
			data.AdminDepositHashFilter = hashFilter
			rows, total, err := deps.TransactionRepo.ListPageFiltered(c.Context(), page, limit, fromFilter, toFilter, hashFilter)
			if err != nil {
				return renderAdminDashboardError(c, data, "Deposit listesi okunamadı", err)
			}
			data.AdminDeposits = dealerActivityViews(rows, deps.AssetRegistry)
			depositBase := buildDepositFilterURL(fromFilter, toFilter, hashFilter)
			data.AdminPagination = dealerPaginationView(page, limit, total, depositBase)

		case "withdrawals":
			rows, total, err := deps.WithdrawalRepo.ListPage(c.Context(), page, limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Çekim listesi okunamadı", err)
			}
			data.Withdrawals = dealerWithdrawalViews(rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/withdrawals")

		case "wallets":
			rows, total, err := deps.WalletRepo.ListPage(c.Context(), page, limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Wallet listesi okunamadı", err)
			}
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
			rows, total, err := deps.ActivityLogRepo.ListPage(c.Context(), page, limit, mID)
			if err != nil {
				return renderAdminDashboardError(c, data, "Activity listesi okunamadı", err)
			}
			data.AdminActivityLogs = dealerAuditLogViews(rows)
			merchants, err := deps.MerchantService.List(c.Context(), 500)
			if err != nil {
				return renderAdminDashboardError(c, data, "Merchant filtresi okunamadı", err)
			}
			data.AdminMerchants = dealerAdminMerchantViews(merchants)
			activityBase := "/admin/activity"
			if merchantFilter != "" {
				activityBase += "?merchant_id=" + merchantFilter
			}
			data.AdminPagination = dealerPaginationView(page, limit, total, activityBase)

		case "webhooks":
			statusFilter := strings.TrimSpace(c.Query("status"))
			data.AdminWebhookStatusFilter = statusFilter
			rows, total, err := deps.WebhookDeliveryRepo.ListPage(c.Context(), page, limit, statusFilter)
			if err != nil {
				return renderAdminDashboardError(c, data, "Webhook listesi okunamadı", err)
			}
			data.AdminWebhooks = dealerWebhookDeliveryViews(rows)
			webhookBase := "/admin/webhooks"
			if statusFilter != "" {
				webhookBase += "?status=" + statusFilter
			}
			data.AdminPagination = dealerPaginationView(page, limit, total, webhookBase)

		case "refunds":
			statusFilter := strings.TrimSpace(c.Query("status"))
			data.AdminRefundStatusFilter = statusFilter
			rows, total, err := deps.RefundRepo.ListPage(c.Context(), page, limit, statusFilter)
			if err != nil {
				return renderAdminDashboardError(c, data, "Refund listesi okunamadı", err)
			}
			data.AdminRefunds = dealerRefundViews(rows)
			refundBase := "/admin/refunds"
			if statusFilter != "" {
				refundBase += "?status=" + statusFilter
			}
			data.AdminPagination = dealerPaginationView(page, limit, total, refundBase)

		case "sweep":
			wallets, walletTotal, err := deps.WalletRepo.ListPage(c.Context(), page, limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Sweep wallet listesi okunamadı", err)
			}
			data.WithdrawalWallets = dealerWalletViews(wallets)
			data.AdminPagination = dealerPaginationView(page, limit, walletTotal, "/admin/sweep")

		case "test-deposit":
			wallets, err := deps.WalletRepo.List(c.Context(), 500)
			if err != nil {
				return renderAdminDashboardError(c, data, "Test deposit wallet listesi okunamadı", err)
			}
			data.AdminWallets = dealerWalletViews(wallets)
			data.AdminAssets = dealerAssetOptions(deps.AssetRegistry)

		case "rescan":
			// Form-only panel.

		case "links":
			rows, total, err := deps.ProductRepo.ListPage(c.Context(), page, limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Link listesi okunamadı", err)
			}
			data.Products = dealerProductViews(c, rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/links")

		case "security":
			admin, err := deps.AdminRepo.FindByEmail(c.Context(), adminEmail)
			if err != nil {
				return renderAdminDashboardError(c, data, "Admin güvenlik ayarları okunamadı", err)
			}
			data.AdminTOTPEnabled = admin.TOTPEnabled

		default: // overview
			recentRows, _, err := deps.TransactionRepo.ListPage(c.Context(), 1, 8)
			if err != nil {
				return renderAdminDashboardError(c, data, "Son depositler okunamadı", err)
			}
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
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "failed", "webhook_delivery", c.Params("id"), "Geçersiz replay isteği.")
			return redirectWithError(c, "/admin/webhooks", "Geçersiz webhook delivery.")
		}
		delivery, created, err := deps.WebhookDeliveryRepo.EnqueueReplay(c.Context(), repositories.WebhookReplayParams{
			DeliveryID: id,
			ActorEmail: adminEmail,
		})
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "failed", "webhook_delivery", id.String(), "Replay reddedildi veya delivery bulunamadı.")
			return redirectWithError(c, "/admin/webhooks", "Webhook delivery bulunamadı veya replay yetkin yok.")
		}
		if !created {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "success", "webhook_delivery", id.String(), "Replay zaten aktif; duplicate istek no-op.")
			return redirectWithSuccess(c, "/admin/webhooks", "Webhook replay zaten kuyrukta.")
		}

		boundary := dealerWebhookDeliveryBoundary(deps)
		if err := boundary.DeliverOne(c.Context(), *delivery); err != nil {
			safeErr := webhooksvc.SanitizeDeliveryError(err)
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "failed", "webhook_delivery", delivery.ID.String(), "Replay başarısız: "+safeErr)
			return redirectWithError(c, "/admin/webhooks", "Replay başarısız: "+safeErr)
		}
		message := "Webhook yeniden gönderildi."
		switch {
		case delivery.PaymentID != nil:
			message = "Payment webhook yeniden gönderildi."
		case delivery.TransactionID != nil:
			message = "Transaction webhook yeniden gönderildi."
		case strings.TrimSpace(delivery.PayloadJSON) != "":
			message = "Lifecycle webhook yeniden gönderildi."
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "success", "webhook_delivery", id.String(), message)
		return redirectWithSuccess(c, "/admin/webhooks", message)
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

func HandleAdminTestDeposit(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if deps.WalletRepo == nil || deps.TransactionRepo == nil || deps.LedgerRepo == nil || deps.Notifier == nil {
			return redirectWithError(c, "/admin/test-deposit", "Test deposit altyapısı hazır değil.")
		}

		walletID, err := uuid.Parse(strings.TrimSpace(c.FormValue("wallet_id")))
		if err != nil {
			return redirectWithError(c, "/admin/test-deposit", "Geçerli wallet seçmelisin.")
		}
		wallet, err := deps.WalletRepo.FindByID(c.Context(), walletID)
		if err != nil {
			return redirectWithError(c, "/admin/test-deposit", "Wallet bulunamadı: "+err.Error())
		}

		selectedAsset, err := parseAdminAssetSelection(deps.AssetRegistry, c.FormValue("asset"))
		if err != nil {
			return redirectWithError(c, "/admin/test-deposit", err.Error())
		}
		chainID := selectedAsset.GetChainID()
		toAddress := repositories.WalletAddressForChainID(*wallet, chainID)
		if strings.TrimSpace(toAddress) == "" {
			return redirectWithError(c, "/admin/test-deposit", "Seçilen wallet için "+constants.ChainName(chainID)+" adresi yok.")
		}

		amount := strings.TrimSpace(c.FormValue("amount"))
		amountRaw, err := types.DecimalToRaw(amount, selectedAsset.GetDecimals())
		if err != nil {
			return redirectWithError(c, "/admin/test-deposit", "Tutar geçersiz: "+err.Error())
		}

		token := tokenForSelectedAsset(selectedAsset)
		symbol := strings.ToUpper(strings.TrimSpace(selectedAsset.GetSymbol()))
		status := models.TransactionStatusConfirmed
		hash := "manual-" + uuid.NewString()
		block := "0"
		blockHash := hash
		fromAddress := "admin-manual-test"
		txParam := types.TransactionParam{
			Context:   c.Context(),
			ChainID:   chainID,
			Hash:      &hash,
			Block:     &block,
			BlockHash: &blockHash,
			Token:     token,
			Symbol:    &symbol,
			Decimals:  selectedAsset.GetDecimals(),
			From:      &fromAddress,
			To:        &toAddress,
			Amount:    &amountRaw,
			Status:    &status,
		}

		if err := deps.TransactionRepo.Create(txParam); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "test_deposit.create", "failed", "transaction", hash, err.Error())
			return redirectWithError(c, "/admin/test-deposit", "Transaction oluşturulamadı: "+err.Error())
		}
		uniqueHash, err := deps.TransactionRepo.UniqueHash(txParam)
		if err != nil {
			return redirectWithError(c, "/admin/test-deposit", "Unique hash üretilemedi: "+err.Error())
		}
		if _, err := deps.TransactionRepo.BindWallet(c.Context(), uniqueHash, "deposit_confirmed", wallet); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "test_deposit.bind", "failed", "transaction", hash, err.Error())
			return redirectWithError(c, "/admin/test-deposit", "Wallet bind başarısız: "+err.Error())
		}
		txModel, err := deps.TransactionRepo.MarkFinality(c.Context(), uniqueHash, 1, 1, true)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "test_deposit.finality", "failed", "transaction", hash, err.Error())
			return redirectWithError(c, "/admin/test-deposit", "Finality işlenemedi: "+err.Error())
		}
		if err := deps.LedgerRepo.PostManualDeposit(c.Context(), *txModel); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "test_deposit.ledger", "failed", "transaction", hash, err.Error())
			return redirectWithError(c, "/admin/test-deposit", "Ledger yüklenemedi: "+err.Error())
		}

		var enqueueErrors []string
		transactionDeliveryCtx, cancelTransactionDelivery := context.WithTimeout(context.Background(), 20*time.Second)
		if err := deliverAdminTransactionWebhook(transactionDeliveryCtx, deps, wallet.Domain, *txModel); err != nil {
			enqueueErrors = append(enqueueErrors, "deposit webhook: "+err.Error())
		}
		cancelTransactionDelivery()

		paymentDeliveryCtx, cancelPaymentDelivery := context.WithTimeout(context.Background(), 20*time.Second)
		if paymentWebhookSent, err := deliverAdminPaymentWebhookIfMatched(paymentDeliveryCtx, deps, *txModel); err != nil {
			enqueueErrors = append(enqueueErrors, "payment webhook: "+err.Error())
		} else if paymentWebhookSent {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "test_deposit.payment_webhook", "success", "transaction", hash, "Manual test deposit payment session ile eşleşti ve webhook kuyruğa alındı.")
		}
		cancelPaymentDelivery()

		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "test_deposit.create", "success", "transaction", hash, amount+" "+symbol+" manual test deposit oluşturuldu.")
		if len(enqueueErrors) > 0 {
			return redirectWithError(c, "/admin/test-deposit", "Test deposit işlendi, ancak "+strings.Join(enqueueErrors, " | "))
		}
		return redirectWithSuccess(c, "/admin/test-deposit", "Test deposit işlendi, bakiye yüklendi ve webhook kuyruğa alındı. Tx: "+hash)
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
				enqueueDealerPayoutLifecycle(c.Context(), deps, *approvedRequest, constants.WebhookEventPayoutBroadcastV1)
				return redirectWithError(c, "/admin/withdrawals", "Transfer gönderildi ancak ledger güncellenemedi: "+err.Error())
			}
			if approvedRequest != nil && approvedRequest.Status == models.WithdrawalStatusFailed {
				enqueueDealerPayoutLifecycle(c.Context(), deps, *approvedRequest, constants.WebhookEventPayoutFailedV1)
			}
			return redirectWithError(c, "/admin/withdrawals", "Transfer başarısız: "+err.Error())
		}
		if approvedRequest != nil {
			broadcastRequest := *approvedRequest
			broadcastRequest.Status = models.WithdrawalStatusProcessing
			enqueueDealerPayoutLifecycle(c.Context(), deps, broadcastRequest, constants.WebhookEventPayoutBroadcastV1)
			enqueueDealerPayoutLifecycle(c.Context(), deps, *approvedRequest, constants.WebhookEventPayoutFinalizedV1)
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
		if request, err := deps.WithdrawalRepo.Find(c.Context(), id); err == nil {
			enqueueDealerPayoutLifecycle(c.Context(), deps, *request, constants.WebhookEventPayoutRejectedV1)
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

		reserveWallet, err := ensureDealerReserveWallet(c.Context(), refund.MerchantID, deps)
		if err != nil {
			return redirectWithError(c, "/admin/refunds", "Bayi reserve cüzdanı hazırlanamadı: "+err.Error())
		}
		walletID := reserveWallet.ID.String()
		chain := constants.ChainName(*session.SelectedChainID)
		claimedRefund, err := deps.RefundRepo.ClaimPendingWithHold(c.Context(), id, adminEmail, *session, deps.LedgerRepo)
		if err != nil {
			return redirectWithError(c, "/admin/refunds", "Refund başka bir işlem tarafından alınmış, artık pending değil veya ledger rezervasyonu yapılamadı: "+err.Error())
		}
		amountRaw := claimedRefund.AmountRaw
		params := types.TransferParams{
			Context:   c.Context(),
			WalletID:  &walletID,
			Chain:     &chain,
			Token:     session.SelectedToken,
			ToAddress: &toAddress,
			AmountRaw: &amountRaw,
		}
		if err := params.ValidateWithdraw(); err != nil {
			_ = deps.RefundRepo.MarkFailed(c.Context(), id, adminEmail, err.Error())
			claimedRefund.Status = models.RefundStatusFailed
			claimedRefund.Error = err.Error()
			enqueueDealerRefundLifecycle(c.Context(), deps, *claimedRefund, constants.WebhookEventRefundFailedV1)
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "refund.approve", "failed", "refund", id.String(), err.Error())
			return redirectWithError(c, "/admin/refunds", err.Error())
		}

		result, err := ExecuteWalletTransfer(deps.WalletRepo, deps.Blockchains, params, false)
		if err != nil {
			_ = deps.RefundRepo.MarkFailed(c.Context(), id, adminEmail, err.Error())
			claimedRefund.Status = models.RefundStatusFailed
			claimedRefund.Error = err.Error()
			enqueueDealerRefundLifecycle(c.Context(), deps, *claimedRefund, constants.WebhookEventRefundFailedV1)
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "refund.approve", "failed", "refund", id.String(), err.Error())
			return redirectWithError(c, "/admin/refunds", "Refund transfer başarısız: "+err.Error())
		}
		if err := deps.RefundRepo.RecordBroadcast(c.Context(), id, adminEmail, result.TxHash); err != nil {
			return redirectWithError(c, "/admin/refunds", "Refund transfer gönderildi ancak tx hash kaydedilemedi: "+err.Error())
		}
		claimedRefund.Status = models.RefundStatusProcessing
		claimedRefund.TxHash = result.TxHash
		claimedRefund.ReviewedBy = adminEmail
		enqueueDealerRefundLifecycle(c.Context(), deps, *claimedRefund, constants.WebhookEventRefundBroadcastV1)
		if err := deps.RefundRepo.MarkSucceededWithLedger(c.Context(), id, adminEmail, result.TxHash, *session, deps.LedgerRepo); err != nil {
			_ = deps.RefundRepo.SetProcessingError(c.Context(), id, "ledger/finalize failed: "+err.Error())
			return redirectWithError(c, "/admin/refunds", "Refund transfer gönderildi ancak ledger/status güncellenemedi: "+err.Error())
		}
		claimedRefund.Status = models.RefundStatusSucceeded
		claimedRefund.Error = ""
		enqueueDealerRefundLifecycle(c.Context(), deps, *claimedRefund, constants.WebhookEventRefundSucceededV1)
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
		if refund, err := deps.RefundRepo.Find(c.Context(), id); err == nil {
			enqueueDealerRefundLifecycle(c.Context(), deps, *refund, constants.WebhookEventRefundRejectedV1)
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
// @Summary Start merchant OIDC login
// @Description Redirects the merchant to the configured OIDC authorization URL.
// @Tags Merchant Portal
// @Produce html
// @Success 302 {string} string "Redirect to OIDC provider"
// @Failure 501 {string} string "HTML page explaining missing OIDC configuration"
// @Router /auth/oidc/login [get]
func HandleOIDCLogin() fiber.Handler {
	return func(c fiber.Ctx) error {
		return startOIDCLogin(c, oidcPortalMerchant)
	}
}

func HandleAdminOIDCLogin() fiber.Handler {
	return func(c fiber.Ctx) error {
		return startOIDCLogin(c, oidcPortalAdmin)
	}
}

func startOIDCLogin(c fiber.Ctx, portal string) error {
	ctx, cancel := context.WithTimeout(c.Context(), 15*time.Second)
	defer cancel()

	oauthConfig, _, err := oidcOAuthConfig(ctx)
	if err != nil {
		data := dealerPageData("OIDC yapılandırması eksik", "login")
		if portal == oidcPortalAdmin {
			data = adminLoginPageData()
			data.Title = "Admin OIDC yapılandırması eksik"
		}
		data.Error = err.Error()
		return c.Status(fiber.StatusNotImplemented).Render("dealer/oidc_missing", data, "dealer/layout")
	}
	state := uuid.NewString()
	nonce := uuid.NewString()
	setOIDCCookie(c, oidcStateCookie, state)
	setOIDCCookie(c, oidcNonceCookie, nonce)
	setOIDCCookie(c, oidcPortalCookie, signedDealerSessionValue(portal))

	options := []oauth2.AuthCodeOption{oidc.Nonce(nonce)}
	if prompt := strings.TrimSpace(os.Getenv("OIDC_PROMPT")); prompt != "" {
		options = append(options, oauth2.SetAuthURLParam("prompt", prompt))
	}
	return c.Redirect().To(oauthConfig.AuthCodeURL(state, options...))
}

// HandleOIDCCallback completes the OIDC authorization-code flow and opens a merchant portal session.
// @Summary Complete merchant OIDC login
// @Description Exchanges the OIDC authorization code for tokens, fetches userinfo, and signs the merchant in.
// @Tags Merchant Portal
// @Produce html
// @Param code query string true "Authorization code"
// @Param state query string true "OIDC state"
// @Success 302 {string} string "Redirect to merchant dashboard"
// @Failure 400 {string} string "Redirect to merchant login with error"
// @Router /auth/oidc/callback [get]
func HandleOIDCCallback(service *services.MerchantService, activityRepo *repositories.ActivityLogRepo, deps ...DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		portal := oidcPortalFromCookie(c)
		loginPath := "/merchant/login"
		actorKind := "dealer"
		callbackEvent := "dealer.oidc_callback"
		if portal == oidcPortalAdmin {
			loginPath = "/admin/login"
			actorKind = "admin"
			callbackEvent = "admin.oidc_callback"
		}
		fail := func(email string, message string) error {
			logDealerActivity(c, activityRepo, nil, actorKind, email, callbackEvent, "failed", "auth", "", message)
			return redirectWithError(c, loginPath, message)
		}

		code := strings.TrimSpace(c.Query("code"))
		state := strings.TrimSpace(c.Query("state"))
		expectedState := strings.TrimSpace(c.Cookies(oidcStateCookie))
		expectedNonce := strings.TrimSpace(c.Cookies(oidcNonceCookie))
		clearOIDCCookie(c, oidcStateCookie)
		clearOIDCCookie(c, oidcNonceCookie)
		clearOIDCCookie(c, oidcPortalCookie)
		if code == "" || state == "" || expectedState == "" || !hmac.Equal([]byte(state), []byte(expectedState)) {
			return fail("", "OIDC oturum doğrulaması başarısız.")
		}
		if expectedNonce == "" {
			return fail("", "OIDC nonce doğrulaması başarısız.")
		}

		ctx, cancel := context.WithTimeout(c.Context(), 20*time.Second)
		defer cancel()
		oauthConfig, provider, err := oidcOAuthConfig(ctx)
		if err != nil {
			return fail("", "OIDC yapılandırması eksik: "+err.Error())
		}

		token, err := oauthConfig.Exchange(ctx, code)
		if err != nil {
			return fail("", "OIDC token alınamadı: "+err.Error())
		}
		rawIDToken, ok := token.Extra("id_token").(string)
		if !ok || strings.TrimSpace(rawIDToken) == "" {
			return fail("", "OIDC id_token dönmedi.")
		}
		idToken, err := provider.Verifier(&oidc.Config{ClientID: oauthConfig.ClientID}).Verify(ctx, rawIDToken)
		if err != nil {
			return fail("", "OIDC id_token doğrulanamadı: "+err.Error())
		}
		if !hmac.Equal([]byte(idToken.Nonce), []byte(expectedNonce)) {
			return fail("", "OIDC nonce doğrulaması başarısız.")
		}
		if idToken.AccessTokenHash != "" && token.AccessToken != "" {
			if err := idToken.VerifyAccessToken(token.AccessToken); err != nil {
				return fail("", "OIDC access token doğrulanamadı: "+err.Error())
			}
		}

		userInfo, err := oidcUserFromToken(ctx, provider, token, idToken)
		if err != nil {
			return fail("", "OIDC kullanıcı bilgisi alınamadı: "+err.Error())
		}

		email := strings.TrimSpace(userInfo.Email)
		if email == "" {
			return fail("", "OIDC email bilgisi dönmedi.")
		}

		if portal == oidcPortalAdmin {
			if len(deps) == 0 || deps[0].AdminRepo == nil {
				return fail(email, "Admin OIDC altyapısı hazır değil.")
			}
			if !oidcUserHasRole(userInfo, "admin") {
				setFlashCookie(c, flashDebugCookie, adminOIDCDebugText(userInfo))
				return fail(email, "OIDC hesabında admin rolü yok.")
			}
			admin, err := deps[0].AdminRepo.EnsureOIDCAdmin(c.Context(), email, userInfo.Name)
			if err != nil {
				if errors.Is(err, repositories.ErrAdminInactive) {
					return fail(email, "Bu admin hesabı pasif.")
				}
				return fail(email, "Admin hesabı açılamadı: "+err.Error())
			}
			logDealerActivity(c, activityRepo, nil, "admin", admin.Email, "admin.oidc_login", "success", "admin", admin.ID.String(), "Admin OIDC ile giriş yaptı.")
			setAdminSessionCookie(c, admin.Email, false)
			return redirectWithSuccess(c, "/admin", "OIDC ile giriş yapıldı.")
		}

		merchant, err := findOrCreateOIDCMerchant(c, service, userInfo)
		if err != nil {
			logDealerActivity(c, activityRepo, nil, "dealer", email, "dealer.oidc_callback", "failed", "auth", "", err.Error())
			return redirectWithError(c, "/merchant/login", "Üye işyeri hesabı açılamadı: "+err.Error())
		}
		if len(deps) > 0 {
			_ = provisionMerchantReserve(c.Context(), merchant.ID, deps[0])
		}
		logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "dealer.oidc_login", "success", "merchant", merchant.ID.String(), "Üye işyeri OIDC ile giriş yaptı.")
		setDealerSessionCookie(c, merchant.ID.String())
		return redirectWithSuccess(c, "/merchant/dashboard", "OIDC ile giriş yapıldı.")
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

func oidcPortalFromCookie(c fiber.Ctx) string {
	portal, err := verifyDealerSessionValue(c.Cookies(oidcPortalCookie))
	if err != nil {
		return oidcPortalMerchant
	}
	switch portal {
	case oidcPortalAdmin, oidcPortalMerchant:
		return portal
	default:
		return oidcPortalMerchant
	}
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
	data.OIDCDebug = flashCookieValue(c.Cookies(flashDebugCookie))
	clearFlashCookie(c, flashSuccessCookie)
	clearFlashCookie(c, flashErrorCookie)
	clearFlashCookie(c, flashDebugCookie)
}

func setFlashCookie(c fiber.Ctx, name string, value string) {
	if len(value) > 3000 {
		value = value[:3000] + "\n..."
	}
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
		var rawClaims map[string]any
		if err := idToken.Claims(&rawClaims); err == nil {
			mergeOIDCRoleSources(&claims, rawClaims, "id_token")
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
				if len(claims.Roles) == 0 {
					claims.Roles = extraClaims.Roles
				}
				if len(claims.Role) == 0 {
					claims.Role = extraClaims.Role
				}
				if len(claims.RoleURI) == 0 {
					claims.RoleURI = extraClaims.RoleURI
				}
				if len(claims.Groups) == 0 {
					claims.Groups = extraClaims.Groups
				}
				if len(claims.Permissions) == 0 {
					claims.Permissions = extraClaims.Permissions
				}
			}
			var rawClaims map[string]any
			if err := userInfo.Claims(&rawClaims); err == nil {
				mergeOIDCRoleSources(&claims, rawClaims, "userinfo")
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

func mergeOIDCRoleSources(userInfo *oidcUserInfo, rawClaims map[string]any, prefix string) {
	if userInfo == nil || len(rawClaims) == 0 {
		return
	}
	for _, key := range []string{"roles", "role", "groups", "permissions", "http://schemas.microsoft.com/ws/2008/06/identity/claims/role"} {
		addOIDCRoleSource(userInfo, prefix+"."+key, rawClaims[key])
	}
	if realm, ok := rawClaims["realm_access"].(map[string]any); ok {
		addOIDCRoleSource(userInfo, prefix+".realm_access.roles", realm["roles"])
	}
	if resources, ok := rawClaims["resource_access"].(map[string]any); ok {
		for client, value := range resources {
			if resource, ok := value.(map[string]any); ok {
				addOIDCRoleSource(userInfo, prefix+".resource_access."+client+".roles", resource["roles"])
			}
		}
	}
	for key, value := range rawClaims {
		lowerKey := strings.ToLower(key)
		if strings.Contains(lowerKey, "role") {
			addOIDCRoleSource(userInfo, prefix+"."+key, value)
		}
	}
}

func addOIDCRoleSource(userInfo *oidcUserInfo, source string, value any) {
	values := stringsFromOIDCClaim(value)
	if len(values) == 0 {
		return
	}
	if userInfo.RoleSources == nil {
		userInfo.RoleSources = make(map[string][]string)
	}
	seen := make(map[string]bool, len(userInfo.RoleSources[source])+len(values))
	for _, existing := range userInfo.RoleSources[source] {
		seen[existing] = true
	}
	for _, value := range values {
		if !seen[value] {
			userInfo.RoleSources[source] = append(userInfo.RoleSources[source], value)
			seen[value] = true
		}
	}
}

func stringsFromOIDCClaim(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return normalizeStringList(strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == ' '
		}))
	case []string:
		return normalizeStringList(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, stringsFromOIDCClaim(item)...)
		}
		return normalizeStringList(out)
	case map[string]any:
		if roles, ok := v["roles"]; ok {
			return stringsFromOIDCClaim(roles)
		}
	}
	return nil
}

func oidcUserHasRole(userInfo *oidcUserInfo, role string) bool {
	if userInfo == nil {
		return false
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return false
	}
	roleClaims := append(append(append(userInfo.Roles, userInfo.Role...), userInfo.RoleURI...), userInfo.Groups...)
	roleClaims = append(roleClaims, userInfo.Permissions...)
	for _, values := range userInfo.RoleSources {
		roleClaims = append(roleClaims, values...)
	}
	for _, value := range roleClaims {
		if strings.EqualFold(strings.TrimSpace(value), role) {
			return true
		}
	}
	return false
}

func adminOIDCDebugText(userInfo *oidcUserInfo) string {
	if userInfo == nil {
		return "OIDC debug\nKullanıcı bilgisi alınamadı."
	}
	lines := []string{
		"OIDC debug",
		"email: " + emptyDebugValue(userInfo.Email),
		"aranan rol: admin",
		"roles: " + formatOIDCClaimValues(userInfo.Roles),
		"role: " + formatOIDCClaimValues(userInfo.Role),
		"ms role: " + formatOIDCClaimValues(userInfo.RoleURI),
		"groups: " + formatOIDCClaimValues(userInfo.Groups),
		"permissions: " + formatOIDCClaimValues(userInfo.Permissions),
		"",
		"claim kaynakları:",
	}
	if len(userInfo.RoleSources) == 0 {
		lines = append(lines, "(rol claim'i bulunamadı)")
		return strings.Join(lines, "\n")
	}
	keys := make([]string, 0, len(userInfo.RoleSources))
	for key := range userInfo.RoleSources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, key+": "+formatOIDCClaimValues(userInfo.RoleSources[key]))
	}
	return strings.Join(lines, "\n")
}

func emptyDebugValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(boş)"
	}
	return value
}

func formatOIDCClaimValues(values []string) string {
	values = normalizeStringList(values)
	if len(values) == 0 {
		return "(boş)"
	}
	if len(values) > 25 {
		return strings.Join(values[:25], ", ") + fmt.Sprintf(" ... (+%d)", len(values)-25)
	}
	return strings.Join(values, ", ")
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

func setAdminSessionCookie(c fiber.Ctx, email string, rememberMe bool) {
	ttl := adminSessionDefaultTTL
	if rememberMe {
		ttl = adminSessionRememberTTL
	}
	expiresAt := time.Now().Add(ttl)
	c.Cookie(&fiber.Cookie{
		Name:     adminSessionCookie,
		Value:    signedDealerSessionValue(adminSessionPayload(email, expiresAt)),
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   int(ttl.Seconds()),
		Expires:  expiresAt,
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

func setAdminTempCookie(c fiber.Ctx, name string, adminID uuid.UUID, rememberMe bool, ttl time.Duration) {
	expiresAt := time.Now().Add(ttl)
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    signedDealerSessionValue(adminTempSessionPayload(adminID, rememberMe)),
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   int(ttl.Seconds()),
		Expires:  expiresAt,
		Secure:   strings.EqualFold(c.Protocol(), "https") || strings.EqualFold(c.Get("X-Forwarded-Proto"), "https"),
	})
}

func clearAdminTempCookie(c fiber.Ctx, name string) {
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

func RequireAdmin(adminRepo *repositories.AdminRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		if isPublicAdminPath(c.Path()) {
			return c.Next()
		}
		email, ok := verifyActiveAdminSession(c, adminRepo)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		c.Locals(adminSessionEmailLocal, email)
		return c.Next()
	}
}

func isPublicAdminPath(rawPath string) bool {
	path := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(rawPath), "/"))
	switch path {
	case "/admin/login", "/admin/logout", "/admin/auth/oidc/login", "/admin/2fa/setup", "/admin/2fa/verify":
		return true
	default:
		return false
	}
}

func verifyActiveAdminSession(c fiber.Ctx, adminRepo *repositories.AdminRepo) (string, bool) {
	payload, err := verifyDealerSessionValue(c.Cookies(adminSessionCookie))
	if err != nil || strings.TrimSpace(payload) == "" {
		clearAdminSessionCookie(c)
		return "", false
	}
	email, err := parseAdminSessionPayload(payload, time.Now())
	if err != nil || strings.TrimSpace(email) == "" {
		clearAdminSessionCookie(c)
		return "", false
	}
	if adminRepo == nil {
		clearAdminSessionCookie(c)
		return "", false
	}
	admin, err := adminRepo.FindByEmail(c.Context(), email)
	if err != nil || admin == nil || !admin.IsActive {
		clearAdminSessionCookie(c)
		return "", false
	}
	return admin.Email, true
}

func requireAdmin(c fiber.Ctx) (string, bool) {
	email, ok := c.Locals(adminSessionEmailLocal).(string)
	if !ok {
		return "", false
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", false
	}
	return email, true
}

func adminRememberRequested(c fiber.Ctx) bool {
	switch strings.ToLower(strings.TrimSpace(c.FormValue("remember_me"))) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

func adminSessionPayload(email string, expiresAt time.Time) string {
	payload := adminSessionPayloadData{
		Email:     strings.ToLower(strings.TrimSpace(email)),
		ExpiresAt: expiresAt.Unix(),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return payload.Email
	}
	return string(encoded)
}

func parseAdminSessionPayload(value string, now time.Time) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("empty admin session")
	}
	if !strings.HasPrefix(value, "{") {
		return "", errors.New("invalid admin session payload")
	}
	var payload adminSessionPayloadData
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return "", err
	}
	email := strings.ToLower(strings.TrimSpace(payload.Email))
	if email == "" {
		return "", errors.New("empty admin email")
	}
	if payload.ExpiresAt <= 0 {
		return "", errors.New("missing admin session expiry")
	}
	if !now.Before(time.Unix(payload.ExpiresAt, 0)) {
		return "", errors.New("expired admin session")
	}
	return email, nil
}

func adminTempSessionPayload(adminID uuid.UUID, rememberMe bool) string {
	payload := adminTempSessionPayloadData{
		AdminID:    adminID.String(),
		RememberMe: rememberMe,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return payload.AdminID
	}
	return string(encoded)
}

func parseAdminTempSessionPayload(value string) (uuid.UUID, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return uuid.Nil, false, errors.New("empty admin temp session")
	}
	if !strings.HasPrefix(value, "{") {
		id, err := uuid.Parse(value)
		return id, false, err
	}
	var payload adminTempSessionPayloadData
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return uuid.Nil, false, err
	}
	id, err := uuid.Parse(strings.TrimSpace(payload.AdminID))
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, payload.RememberMe, nil
}

func requireDealerSession(c fiber.Ctx) (string, bool) {
	merchantID, err := verifyDealerSessionValue(c.Cookies(dealerSessionCookie))
	if err != nil || strings.TrimSpace(merchantID) == "" {
		return "", false
	}
	return merchantID, true
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
	return redirectWithError(c, "/merchant/login", "Devam etmek için giriş yapmalısın.")
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
		RegisterURL:      "/merchant/register",
		LoginURL:         "/merchant/login",
		OnboardingURL:    "/merchant/onboarding",
		DashboardURL:     "/merchant/dashboard",
		TreasuryURL:      "/merchant/dashboard/treasury",
		ActivityURL:      "/merchant/dashboard/activity",
		TransactionsURL:  "/merchant/dashboard/transactions",
		UsersURL:         "/merchant/dashboard/users",
		WithdrawalsURL:   "/merchant/dashboard/withdrawals",
		RescanURL:        "/merchant/dashboard/rescan",
		DomainsPanelURL:  "/merchant/dashboard/domains",
		ProductsURL:      "/merchant/products",
		InvoicesURL:      "/merchant/invoices",
		ProductsPanelURL: "/merchant/dashboard/products",
		SettingsPanelURL: "/merchant/dashboard/settings",
		DomainsURL:       "/merchant/domains",
		LogoutURL:        "/merchant/logout",
		ActivePanel:      "treasury",
	}
}

func adminLoginPageData() DealerPageData {
	data := dealerPageData("Admin girişi", "admin-login")
	data.OIDCLoginURL = "/admin/auth/oidc/login"
	data.LoginURL = "/admin/login"
	data.RegisterURL = ""
	return data
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
	if strings.EqualFold(c.Path(), "/merchant/domains") {
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
	case "/admin/test-deposit":
		return "test-deposit"
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
		AdminTestDepositURL: "/admin/test-deposit",
	}
	return data
}

func renderAdminDashboardError(c fiber.Ctx, data DealerPageData, message string, err error) error {
	if err != nil {
		message += ": " + err.Error()
	}
	data.Error = message
	return c.Status(fiber.StatusInternalServerError).Render("dealer/admin_dashboard", data, "dealer/layout")
}

func parseAdminAssetSelection(registry *asset.Registry, value string) (asset.Asset, error) {
	if registry == nil {
		return nil, errors.New("asset registry hazır değil")
	}
	parts := strings.SplitN(strings.TrimSpace(value), "|", 2)
	if len(parts) != 2 {
		return nil, errors.New("geçerli asset seçmelisin")
	}
	chainRaw, identifier := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	chainInt, err := strconv.ParseInt(chainRaw, 10, 64)
	if err != nil {
		return nil, errors.New("geçerli chain seçmelisin")
	}
	selected, ok := registry.Get(constants.ChainID(chainInt), identifier)
	if !ok {
		return nil, errors.New("asset registry içinde bulunamadı")
	}
	return selected, nil
}

func tokenForSelectedAsset(selected asset.Asset) *string {
	if selected == nil || selected.IsNative() {
		return nil
	}
	token := strings.TrimSpace(selected.GetIdentifier())
	if token == "" {
		return nil
	}
	return &token
}

func dealerAssetOptions(registry *asset.Registry) []DealerAssetOption {
	if registry == nil {
		return nil
	}
	assets := registry.ListAll()
	options := make([]DealerAssetOption, 0, len(assets))
	for _, item := range assets {
		if item == nil {
			continue
		}
		chainName := constants.ChainName(item.GetChainID())
		token := ""
		tokenLabel := "native"
		if !item.IsNative() {
			token = item.GetIdentifier()
			tokenLabel = shortText(token, 8, 6)
		}
		label := fmt.Sprintf("%s / %s / %s / %d decimals", chainName, item.GetSymbol(), tokenLabel, item.GetDecimals())
		options = append(options, DealerAssetOption{
			Value:    fmt.Sprintf("%d|%s", item.GetChainID(), item.GetIdentifier()),
			Label:    label,
			Chain:    chainName,
			Symbol:   item.GetSymbol(),
			Token:    token,
			Decimals: item.GetDecimals(),
		})
	}
	return options
}

func deliverAdminTransactionWebhook(ctx context.Context, deps DealerDeps, domain models.Domain, txModel models.Transaction) error {
	if deps.WebhookDeliveryRepo == nil {
		return errors.New("webhook delivery kuyruğu hazır değil")
	}
	_, _, err := deps.WebhookDeliveryRepo.EnqueueTransaction(ctx, domain, txModel)
	return err
}

func deliverAdminPaymentWebhookIfMatched(ctx context.Context, deps DealerDeps, txModel models.Transaction) (bool, error) {
	if deps.PaymentRepo == nil || deps.WebhookDeliveryRepo == nil {
		return false, nil
	}
	session, changed, err := deps.PaymentRepo.MarkPaidByTransaction(ctx, txModel)
	if err != nil || !changed || session == nil {
		return false, err
	}
	_, _, err = deps.WebhookDeliveryRepo.EnqueuePayment(ctx, session.Domain, *session)
	return true, err
}

func dealerWebhookDeliveryBoundary(deps DealerDeps) webhooksvc.WebhookDeliveryBoundary {
	return webhooksvc.WebhookDeliveryBoundary{
		Queue:    deps.WebhookDeliveryRepo,
		Notifier: deps.Notifier,
		FindDomain: func(ctx context.Context, id uuid.UUID) (*models.Domain, error) {
			if deps.DomainService == nil {
				return nil, errors.New("domain servisi hazır değil")
			}
			idString := id.String()
			return deps.DomainService.FindByID(types.DomainParams{
				Context:  ctx,
				DomainID: &idString,
			})
		},
		FindTransaction: func(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
			if deps.TransactionRepo == nil {
				return nil, errors.New("transaction repo hazır değil")
			}
			return deps.TransactionRepo.FindByID(ctx, id)
		},
		FindPayment: func(ctx context.Context, id uuid.UUID) (*models.PaymentSession, error) {
			if deps.PaymentRepo == nil {
				return nil, errors.New("payment repo hazır değil")
			}
			return deps.PaymentRepo.FindByID(ctx, id)
		},
		MarkTransactionAttempt: func(ctx context.Context, uniqueHash string, delivered bool, err error) error {
			if deps.TransactionRepo == nil {
				return nil
			}
			return deps.TransactionRepo.MarkWebhookAttempt(ctx, uniqueHash, delivered, err)
		},
		MarkPaymentAttempt: func(ctx context.Context, id uuid.UUID, delivered bool, err error) error {
			if deps.PaymentRepo == nil {
				return nil
			}
			return deps.PaymentRepo.MarkWebhookAttempt(ctx, id, delivered, err)
		},
	}
}

func createAdminTransactionWebhookDelivery(ctx context.Context, deps DealerDeps, domain models.Domain, txModel models.Transaction) uuid.UUID {
	if deps.WebhookDeliveryRepo == nil || txModel.MerchantID == nil || txModel.DomainID == nil {
		return uuid.Nil
	}
	delivery, _, err := deps.WebhookDeliveryRepo.EnqueueTransaction(ctx, domain, txModel)
	if err != nil {
		return uuid.Nil
	}
	if delivery == nil {
		return uuid.Nil
	}
	return delivery.ID
}

func createAdminPaymentWebhookDelivery(ctx context.Context, deps DealerDeps, domain models.Domain, session models.PaymentSession) uuid.UUID {
	if deps.WebhookDeliveryRepo == nil {
		return uuid.Nil
	}
	delivery, _, err := deps.WebhookDeliveryRepo.EnqueuePayment(ctx, domain, session)
	if err != nil {
		return uuid.Nil
	}
	if delivery == nil {
		return uuid.Nil
	}
	return delivery.ID
}

func enqueueDealerLifecycleWebhook(ctx context.Context, deps DealerDeps, domain models.Domain, payload webhooksvc.LifecyclePayload) uuid.UUID {
	if deps.WebhookDeliveryRepo == nil {
		return uuid.Nil
	}
	delivery, _, err := deps.WebhookDeliveryRepo.EnqueueLifecycle(ctx, domain, payload)
	if err != nil || delivery == nil {
		return uuid.Nil
	}
	return delivery.ID
}

func enqueueDealerPayoutLifecycle(ctx context.Context, deps DealerDeps, request models.WithdrawalRequest, eventType string) uuid.UUID {
	if request.DomainID == nil || deps.DomainService == nil {
		return uuid.Nil
	}
	domainID := request.DomainID.String()
	domain, err := deps.DomainService.FindByID(types.DomainParams{
		Context:  ctx,
		DomainID: &domainID,
	})
	if err != nil {
		return uuid.Nil
	}
	payload := webhooksvc.NewPayoutPayload(eventType, request)
	return enqueueDealerLifecycleWebhook(ctx, deps, *domain, payload)
}

func enqueueDealerRefundLifecycle(ctx context.Context, deps DealerDeps, refund models.Refund, eventType string) uuid.UUID {
	if deps.DomainService == nil {
		return uuid.Nil
	}
	domainID := refund.DomainID.String()
	domain, err := deps.DomainService.FindByID(types.DomainParams{
		Context:  ctx,
		DomainID: &domainID,
	})
	if err != nil {
		return uuid.Nil
	}
	payload := webhooksvc.NewRefundPayload(eventType, refund)
	return enqueueDealerLifecycleWebhook(ctx, deps, *domain, payload)
}

func markAdminWebhookDeliveryAttempt(ctx context.Context, deps DealerDeps, deliveryID uuid.UUID, delivered bool, lastErr error) {
	if deps.WebhookDeliveryRepo == nil || deliveryID == uuid.Nil {
		return
	}
	_ = deps.WebhookDeliveryRepo.MarkAttempt(ctx, deliveryID, delivered, lastErr)
}

func dealerWebhookDeliveryViews(rows []models.WebhookDelivery) []DealerWebhookDeliveryView {
	views := make([]DealerWebhookDeliveryView, 0, len(rows))
	for _, row := range rows {
		deliveredAt := ""
		if row.DeliveredAt != nil {
			deliveredAt = formatPanelTime(*row.DeliveredAt)
		}
		nextRetryAt := ""
		if row.NextRetryAt != nil {
			nextRetryAt = formatPanelTime(*row.NextRetryAt)
		}
		originalDeliveryID := ""
		if row.OriginalDeliveryID != nil {
			originalDeliveryID = row.OriginalDeliveryID.String()
		}
		replayRequestedAt := ""
		if row.ReplayRequestedAt != nil {
			replayRequestedAt = formatPanelTime(*row.ReplayRequestedAt)
		}
		views = append(views, DealerWebhookDeliveryView{
			ID:                 row.ID.String(),
			EventID:            row.EventID,
			EventType:          row.EventType,
			TargetURL:          row.TargetURL,
			Status:             row.Status,
			Attempts:           row.Attempts,
			LastError:          webhooksvc.SanitizeDeliveryText(row.LastError),
			FailureCategory:    row.FailureCategory,
			NextRetryAt:        nextRetryAt,
			NextAction:         webhookDeliveryNextAction(row),
			OriginalDeliveryID: originalDeliveryID,
			ReplayCount:        row.ReplayCount,
			ReplayRequestedBy:  row.ReplayRequestedBy,
			ReplayRequestedAt:  replayRequestedAt,
			CreatedAt:          formatPanelTime(row.CreatedAt),
			UpdatedAt:          formatPanelTime(row.UpdatedAt),
			DeliveredAt:        deliveredAt,
		})
	}
	return views
}

func webhookDeliveryNextAction(row models.WebhookDelivery) string {
	if strings.TrimSpace(row.OperatorAction) != "" {
		return row.OperatorAction
	}
	switch row.Status {
	case models.WebhookDeliveryStatusDeadLetter:
		return "replay_or_investigate"
	case models.WebhookDeliveryStatusFailed:
		return "waiting_retry"
	case models.WebhookDeliveryStatusPending:
		return "delivery_pending"
	case models.WebhookDeliveryStatusProcessing:
		return "delivery_in_progress"
	default:
		return ""
	}
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
		walletKind := "Kullanıcı wallet"
		if wallet.HDAddressId == 0 || domainLabel == "_reserve_" {
			walletKind = "Reserve wallet"
		}
		ownerRef := walletOwnerRef(wallet)
		views = append(views, DealerWalletView{
			ID:            wallet.ID.String(),
			ShortID:       shortText(wallet.ID.String(), 8, 6),
			MerchantID:    wallet.MerchantID.String(),
			Merchant:      merchant,
			Label:         walletLabel(wallet),
			ProductID:     emptyDash(wallet.ProductID),
			UserID:        emptyDash(wallet.UserID),
			OwnerRef:      ownerRef,
			DomainID:      wallet.DomainID.String(),
			Domain:        domainLabel,
			WalletKind:    walletKind,
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
		view.PrevURL = paginationURL(basePath, page-1, limit)
	}
	if view.HasNext {
		view.NextURL = paginationURL(basePath, page+1, limit)
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
				URL:    paginationURL(basePath, p, limit),
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

func paginationURL(basePath string, page, limit int) string {
	separator := "?"
	if strings.Contains(basePath, "?") {
		separator = "&"
	}
	return fmt.Sprintf("%s%spage=%d&limit=%d", basePath, separator, page, limit)
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

func walletOwnerRef(wallet models.Wallet) string {
	productID := strings.TrimSpace(wallet.ProductID)
	userID := strings.TrimSpace(wallet.UserID)
	switch {
	case productID != "" && userID != "":
		return "User " + userID + " · Product " + productID
	case userID != "":
		return "User " + userID
	case productID != "":
		return "Product " + productID
	default:
		return "Reserve / sistem"
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
	return runtimeDealerSessionSecret
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
