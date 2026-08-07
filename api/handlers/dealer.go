package handlers

import (
	"bytes"
	"context"
	"core/api/middleware"
	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/contracts/erc20"
	"core/helpers"
	"core/models"
	"core/repositories"
	"core/services/chainresource"
	"core/services/networkops"
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
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/paginate"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

const dealerSessionCookie = "dealer_session"
const adminSessionCookie = "admin_session"
const (
	adminSessionEmailLocal = "admin_session_email"
	adminSessionRoleLocal  = "admin_session_role"
)
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
const adminHeaderStatsTTL = 3 * time.Second
const oidcPortalMerchant = "merchant"
const oidcPortalAdmin = "admin"

var runtimeDealerSessionSecret = "runtime-session-secret-" + uuid.NewString()

type adminHeaderStats struct {
	MerchantCount   int
	PaymentCount    int
	DepositCount    int
	WithdrawalCount int
	WalletCountAll  int
	ActivityCount   int
}

var adminHeaderStatsCache = struct {
	sync.Mutex
	stats     adminHeaderStats
	expiresAt time.Time
}{}

type DealerDeps struct {
	MerchantService             *services.MerchantService
	DomainRepo                  *repositories.DomainRepo
	DomainService               *services.DomainService
	WalletRepo                  *repositories.WalletRepo
	ProductRepo                 *repositories.ProductRepo
	PaymentRepo                 *repositories.PaymentRepo
	WithdrawalRepo              *repositories.WithdrawalRequestRepo
	RefundRepo                  *repositories.RefundRepo
	LedgerRepo                  *repositories.LedgerRepo
	SweepJobRepo                *repositories.SweepJobRepo
	OutboundRepo                *repositories.OutboundTransactionRepo
	TransactionRepo             *repositories.TransactionRepo
	ReconciliationRepo          *repositories.ReconciliationRepo
	WebhookDeliveryRepo         *repositories.WebhookDeliveryRepo
	MoneyEventOutboxRepo        *repositories.MoneyEventOutboxRepo
	MoneyEventInboxRepo         *repositories.MoneyEventInboxRepo
	WorkerLeaseRepo             *repositories.WorkerLeaseRepo
	ActivityLogRepo             *repositories.ActivityLogRepo
	OutboundPolicyRepo          *repositories.OutboundPolicyRepo
	AdminRepo                   *repositories.AdminRepo
	ChainStateRepo              *repositories.ChainStateRepo
	ProviderHealthRepo          *repositories.ProviderHealthRepo
	NetworkOperationalStateRepo *repositories.NetworkOperationalStateRepo
	WalletAddressLookupRepo     *repositories.WalletAddressLookupRepo
	AssetRegistry               *asset.Registry
	Blockchains                 *blockchain.ChainFactory
	TxRescanService             func() *txrescan.Service
	Notifier                    WebhookNotifier
	PriceOracle                 pricing.PriceOracle
}

// WebhookNotifier is the minimal interface the dealer handlers need from the webhook service.
type WebhookNotifier interface {
	Deliver(ctx context.Context, domain models.Domain, tx models.Transaction) error
	DeliverPayment(ctx context.Context, domain models.Domain, session models.PaymentSession) error
	DeliverRaw(ctx context.Context, domain models.Domain, eventType, eventID, eventVersion string, body []byte) error
}

type DealerPageData struct {
	Title                 string
	Active                string
	OIDCLoginURL          string
	OIDCProvider          string
	RegisterURL           string
	LoginURL              string
	OnboardingURL         string
	Error                 string
	Success               string
	MerchantID            string
	MerchantName          string
	MerchantEmail         string
	DashboardURL          string
	DomainsURL            string
	LogoutURL             string
	ActivePanel           string
	TreasuryURL           string
	ActivityURL           string
	ActivityAuditURL      string
	ActivityPaymentsURL   string
	ActivityDepositsURL   string
	TransactionsURL       string
	UsersURL              string
	WithdrawalsURL        string
	RescanURL             string
	IntegrationsURL       string
	DomainsPanelURL       string
	ProductsURL           string
	InvoicesURL           string
	ProductsPanelURL      string
	ProductsLinksURL      string
	SettingsPanelURL      string
	Domains               []DealerDomainView
	Wallets               []DealerWalletView
	WithdrawalWallets     []DealerWalletView
	WalletPage            DealerPaginationView
	PaymentPage           DealerPaginationView
	TransactionPage       DealerPaginationView
	AuditPage             DealerPaginationView
	ActivityPaymentPage   DealerPaginationView
	ActivityDepositPage   DealerPaginationView
	Withdrawals           []DealerWithdrawalView
	Products              []DealerProductView
	PaymentAssets         []DealerAssetOption
	Payments              []DealerPaymentView
	WithdrawalAssets      []DealerWithdrawalAssetView
	Balances              []DealerBalanceView
	TreasuryGroups        []DealerVaultAssetView
	ChainVaults           []DealerChainVaultView
	SettingsVisibleChains []DealerSettingsNetworkView
	SettingsHiddenChains  []DealerSettingsNetworkView
	Activities            []DealerActivityView
	AuditLogs             []DealerAuditLogView
	WalletCount           int
	DomainCount           int
	AssetCount            int
	NetworkCount          int
	ActivityCount         int
	DepositCount          int
	ProductCount          int
	PaymentCount          int
	WalletCountAll        int
	MerchantCount         int
	HasSession            bool
	Language              string
	PaymentLinkURL        string

	AdminMerchants          []DealerAdminMerchantView
	AdminWallets            []DealerWalletView
	AdminDeposits           []DealerActivityView
	AdminActivityLogs       []DealerAuditLogView
	AdminWebhooks           []DealerWebhookDeliveryView
	AdminReconciliationJobs []DealerReconciliationJobView
	AdminProviderHealth     []DealerProviderHealthView
	AdminNetworkStates      []DealerNetworkOperationalStateView
	AdminRefunds            []DealerRefundView
	AdminReadiness          []DealerReadinessLevelView
	AdminReadinessRaw       []DealerReadinessCheckView
	AdminMetricsGroups      []DealerMetricGroupView
	AdminMetricAlerts       []DealerMetricAlertView
	AdminMetricTabs         []DealerMetricTabView
	AdminAssets             []DealerAssetOption
	AdminRecoverChains      []DealerRecoverChainOption
	AdminTestDomains        []DealerTestDomainOption
	AdminTestPayments       []DealerTestPaymentView
	AdminVaults             []DealerVaultAssetView
	AdminSweepStats         []DealerSweepStatusView
	AdminSweepJobs          []DealerSweepJobView

	AdminPanel             string
	AdminOverviewURL       string
	AdminMerchantsURL      string
	AdminVaultURL          string
	AdminAssetsURL         string
	AdminPaymentsURL       string
	AdminDepositsURL       string
	AdminWithdrawalsURL    string
	AdminWalletsURL        string
	AdminActivityURL       string
	AdminSweepURL          string
	AdminRecoverURL        string
	AdminLinksURL          string
	AdminWebhooksURL       string
	AdminReconciliationURL string
	AdminProviderHealthURL string
	AdminNetworksURL       string
	AdminRefundsURL        string
	AdminReadinessURL      string
	AdminMetricsURL        string
	AdminRescanURL         string
	AdminTestsURL          string
	AdminTestDepositURL    string
	WithdrawalCount        int

	WalletSearch        string
	PaymentStatusFilter string
	ProductsTab         string
	ActivityTab         string
	PaymentStats        map[string]int64
	HideTestnets        bool
	HiddenChains        string
	RescanChains        []DealerRescanChainOption

	AdminPagination                 DealerPaginationView
	AdminMerchantFilter             string
	AdminSweepStatusFilter          string
	AdminSweepEligibleCount         int64
	AdminTOTPEnabled                bool
	AdminRole                       string
	AdminSecurityURL                string
	AdminLoginEmail                 string
	AdminRememberMe                 bool
	OIDCDebug                       string
	TOTPSecret                      string
	TOTPQRDataURL                   htmltemplate.URL
	AdminDepositFromFilter          string
	AdminDepositToFilter            string
	AdminDepositHashFilter          string
	AdminWithdrawalStatusFilter     string
	AdminWebhookStatusFilter        string
	AdminReconciliationStatusFilter string
	AdminRefundStatusFilter         string
	AdminRecoverChainFilter         string
	AdminRecoverAssetFilter         string
	AdminRescanResult               string
	AdminOutboundPolicy             DealerOutboundPolicyView
	AdminOutboundWhitelist          []DealerOutboundWhitelistView
	AdminMerchantDetail             DealerAdminMerchantView
	AdminMerchantDetailURL          string
	AdminMerchantDetailTab          string
	AdminMerchantDomainCount        int64
	AdminMerchantWalletCount        int64
	AdminMerchantPaymentCount       int64
	AdminMerchantPaymentStatus      string
	AdminReadinessReady             bool
	AdminReadinessCheckedAt         string
	AdminMetricsSummary             DealerMetricsSummaryView
	AdminMetricsActiveTab           string
	AdminMetricsRaw                 string
}

type DealerReadinessLevelView struct {
	Key         string
	Title       string
	Summary     string
	Status      string
	StatusClass string
	Items       []DealerReadinessCheckView
}

type DealerReadinessCheckView struct {
	Name          string
	Label         string
	Status        string
	StatusClass   string
	Owner         string
	EvidenceURL   string
	EvidenceLabel string
	Details       string
	Error         string
	Blocking      bool
	BlockingLabel string
	LastChecked   string
}

type DealerMetricsSummaryView struct {
	Endpoint         string
	CheckedAt        string
	TotalSeries      int
	TotalGroups      int
	HealthyCount     int
	WarningCount     int
	CriticalCount    int
	CollectionErrors int
	AttentionCount   int
	Status           string
	StatusClass      string
	Tone             string
	Headline         string
	Description      string
}

type DealerMetricGroupView struct {
	Key           string
	Title         string
	Summary       string
	Status        string
	StatusClass   string
	Tone          string
	TotalCount    int
	HealthyCount  int
	WarningCount  int
	CriticalCount int
	HealthPercent string
	Items         []DealerMetricView
	ProblemItems  []DealerMetricView
}

type DealerMetricView struct {
	Name        string
	DisplayName string
	Help        string
	Labels      string
	Value       string
	Status      string
	StatusClass string
	Tone        string
	Description string
	Action      string
	IsProblem   bool
}

type DealerMetricAlertView struct {
	Title       string
	Detail      string
	Action      string
	Metric      string
	Labels      string
	Value       string
	Group       string
	Status      string
	StatusClass string
	Tone        string
	Rank        int
}

type DealerMetricTabView struct {
	Key         string
	Label       string
	URL         string
	Count       int
	Status      string
	StatusClass string
	Tone        string
	Active      bool
}

type DealerDomainView struct {
	ID                  string
	DomainURL           string
	NotificationMode    string
	WebhookURL          string
	NATSURL             string
	NATSSubject         string
	WebhookSigningMode  string
	WebhookCatalogURL   string
	WebhookDocsURL      string
	WebhookLastStatus   string
	WebhookLastEvent    string
	WebhookLastAt       string
	WebhookLastAttempts uint
	KeyID               string
	APIKey              string
	APIScopes           string
	APISecretStatus     string
	APISecretRotatedAt  string
	RotateConfirm       string
	SigningExample      string
	IdempotencyExample  string
	HDAccountID         uint32
	CreatedAt           string
}

type DealerWalletView struct {
	ID             string
	ShortID        string
	MerchantID     string
	Merchant       string
	Label          string
	ProductID      string
	UserID         string
	OwnerRef       string
	DomainID       string
	Domain         string
	WalletKind     string
	CreatedAt      string
	Addresses      []DealerAddressView
	AddressCount   int
	AddressPreview string
	MissingChains  []DealerMissingChainView
	Balances       []DealerWalletBalanceRow
	BalanceCount   int
	BalancePreview string
}

type DealerRescanChainOption struct {
	Name    string
	Label   string
	ChainID string
	LogoURL string
	Meta    string
}

type DealerWalletBalanceRow struct {
	Chain        string
	ChainID      string
	Symbol       string
	Token        string
	AssetKey     string
	LogoURL      string
	Deposited    string
	Locked       string
	LockedRaw    string
	Available    string
	AvailableRaw string
	Decimals     uint8
}

type DealerMissingChainView struct {
	ChainName  string
	ChainLabel string
	WalletID   string
}

type DealerProductView struct {
	ID                string
	Name              string
	Description       string
	LinkType          string
	LinkTypeLabel     string
	Amount            string
	Currency          string
	AmountDisplay     string
	Language          string
	Merchant          string
	DomainID          string
	Domain            string
	PaymentURL        string
	LogoURL           string
	LogoText          string
	SuccessURL        string
	CancelURL         string
	X402Enabled       bool
	DefaultAssetValue string
	CreatedAt         string
}

type DealerPaymentView struct {
	ID                 string
	ShortID            string
	OrderID            string
	ProductID          string
	UserID             string
	Merchant           string
	Domain             string
	LinkType           string
	Amount             string
	AmountSort         string
	Currency           string
	AmountDisplay      string
	Status             string
	StatusLabel        string
	StatusClass        string
	WebhookStatus      string
	WebhookAttempts    uint
	CheckoutURL        string
	InvoiceURL         string
	SelectedAsset      string
	SelectedChain      string
	ChainLogoURL       string
	DepositAddress     string
	DepositAddressFull string
	TxHash             string
	TxHashShort        string
	CreatedAt          string
	CreatedSort        string
	SearchText         string
}

type DealerAdminMerchantView struct {
	ID        string
	Name      string
	Email     string
	Role      string
	IsActive  bool
	CreatedAt string
}

type DealerOutboundPolicyView struct {
	ID                   string
	WhitelistRequired    bool
	EmergencyFrozen      bool
	MaxAmountRaw         string
	VelocityLimitRaw     string
	VelocityWindowHours  int64
	VelocityWindowLabel  string
	UpdatedBy            string
	UpdatedAt            string
	ConfigurationSummary string
}

type DealerOutboundWhitelistView struct {
	ID        string
	Scope     string
	Chain     string
	Token     string
	Address   string
	Label     string
	IsActive  bool
	UpdatedBy string
	UpdatedAt string
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
	Chain       string
	Address     string
	ExplorerURL string
}

type DealerWithdrawalView struct {
	ID              string
	ShortID         string
	MerchantID      string
	ShortMerchantID string
	MerchantName    string
	WalletID        string
	ShortWalletID   string
	Chain           string
	ChainLogoURL    string
	ToAddress       string
	ToAddressShort  string
	AmountRaw       string
	AmountDisplay   string
	Symbol          string
	Token           string
	Note            string
	Status          string
	StatusLabel     string
	StatusClass     string
	TxHash          string
	TxHashShort     string
	Error           string
	RequestedBy     string
	ReviewedBy      string
	CreatedAt       string
	CreatedSort     string
	SearchText      string
}

type DealerWebhookDeliveryView struct {
	ID                 string
	EventID            string
	EventType          string
	EventVersion       string
	MerchantID         string
	DomainID           string
	ResourceType       string
	ResourceID         string
	Sequence           int64
	IdempotencyKey     string
	TargetURL          string
	Status             string
	Attempts           uint
	LastError          string
	FailureCategory    string
	NextRetryAt        string
	NextAction         string
	PayloadPreview     string
	LatencyEvidence    string
	OriginalDeliveryID string
	ReplayCount        uint
	ReplayRequestedBy  string
	ReplayRequestedAt  string
	CreatedAt          string
	UpdatedAt          string
	DeliveredAt        string
}

type DealerReconciliationJobView struct {
	ID                string
	ShortID           string
	Reason            string
	Status            string
	StatusClass       string
	Severity          string
	Owner             string
	Chain             string
	Scope             string
	ResourceType      string
	ResourceID        string
	AffectedResources string
	EvidencePreview   string
	Outcome           string
	Error             string
	NextAction        string
	OpenedAt          string
	UpdatedAt         string
	ResolvedAt        string
	NextRunAt         string
	Attempts          uint
	ChainEvidence     string
	LedgerEvidence    string
	LifecycleEvidence string
	WebhookEvidence   string
	BroadcastEvidence string
	AuditTimeline     string
}

type DealerProviderHealthView struct {
	Chain             string
	ChainID           string
	ProviderLabel     string
	ProviderHash      string
	Status            string
	StatusClass       string
	Reachable         bool
	LatestHeight      int64
	HeadHash          string
	LatencyMS         int64
	Lag               int64
	StaleIndicator    string
	Selected          bool
	FailoverReason    string
	ErrorCategory     string
	ErrorDetail       string
	FailureCount      int
	CheckedAt         string
	FallbackPolicy    string
	ReadinessEvidence string
}

type DealerNetworkOperationalStateView struct {
	Chain             string
	ChainID           string
	ChainSlug         string
	ChainLogoURL      string
	Mode              string
	ModeLabel         string
	ModeClass         string
	Reason            string
	UpdatedBy         string
	UpdatedAt         string
	BlocksDeposits    bool
	BlocksWithdrawals bool
	Testnet           bool
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
	Value         string
	AssetKey      string
	Label         string
	ChainID       string
	Chain         string
	ChainLogoURL  string
	Symbol        string
	Name          string
	Token         string
	DisplayToken  string
	Identifier    string
	IdentifierTag string
	Type          string
	TypeLabel     string
	Decimals      uint8
	LogoURL       string
	IsNative      bool
}

type DealerTestDomainOption struct {
	ID         string
	MerchantID string
	Merchant   string
	Domain     string
	Label      string
}

type DealerTestPaymentView struct {
	ID             string
	ShortID        string
	OrderID        string
	Merchant       string
	Domain         string
	LinkType       string
	LinkTypeLabel  string
	WalletID       string
	ShortWalletID  string
	AssetValue     string
	AssetLabel     string
	AmountDisplay  string
	TestAmount     string
	DepositAddress string
	CheckoutURL    string
	Status         string
	StatusLabel    string
	StatusClass    string
	CreatedAt      string
}

type DealerRecoverChainOption struct {
	ChainID    string
	Chain      string
	LogoURL    string
	AssetCount int
}

type DealerWithdrawalAssetView struct {
	Value            string
	AssetKey         string
	Label            string
	Chain            string
	ChainLabel       string
	Symbol           string
	Token            string
	DisplayToken     string
	Decimals         uint8
	AvailableRaw     string
	AvailableDisplay string
	AvailableInput   string
}

type DealerSweepStatusView struct {
	Status string
	Label  string
	Count  int64
}

type DealerSweepJobView struct {
	ID                    string
	ShortID               string
	TransactionUniqueHash string
	TransactionHash       string
	WalletID              string
	ShortWalletID         string
	MerchantID            string
	ShortMerchantID       string
	Chain                 string
	ChainLogoURL          string
	Token                 string
	Asset                 string
	Status                string
	StatusLabel           string
	Attempts              uint
	MaxAttempts           uint
	PrefundAttempts       uint
	PrefundMaxAttempts    uint
	NextRunAt             string
	LockedUntil           string
	SweepTxHash           string
	LastError             string
	OperatorAction        string
	CreatedAt             string
	UpdatedAt             string
}

type DealerVaultAssetView struct {
	ID                string
	Symbol            string
	LogoURL           string
	SearchText        string
	NetworkCount      int
	VariantCount      int
	Details           []DealerVaultBalanceView
	VaultRaw          string
	VaultDisplay      string
	VaultSort         string
	AvailableRaw      string
	AvailableDisplay  string
	AvailableSort     string
	PendingRaw        string
	PendingDisplay    string
	PendingSort       string
	WithdrawalRaw     string
	WithdrawalDisplay string
	WithdrawalSort    string
	RefundRaw         string
	RefundDisplay     string
	RefundSort        string
	SweepRaw          string
	SweepDisplay      string
	SweepSort         string
	LockedRaw         string
	LockedDisplay     string
	LockedSort        string
}

type DealerVaultBalanceView struct {
	Chain             string
	ChainLogoURL      string
	Symbol            string
	Token             string
	DisplayToken      string
	LogoURL           string
	Decimals          uint8
	VaultRaw          string
	VaultDisplay      string
	VaultSort         string
	AvailableRaw      string
	AvailableDisplay  string
	AvailableSort     string
	PendingRaw        string
	PendingDisplay    string
	PendingSort       string
	WithdrawalRaw     string
	WithdrawalDisplay string
	WithdrawalSort    string
	RefundRaw         string
	RefundDisplay     string
	RefundSort        string
	SweepRaw          string
	SweepDisplay      string
	SweepSort         string
	LockedRaw         string
	LockedDisplay     string
	LockedSort        string
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

type DealerSettingsNetworkView struct {
	Key            string
	Chain          string
	ChainLogoURL   string
	Testnet        bool
	ExplicitHidden bool
	PolicyHidden   bool
}

type DealerActivityView struct {
	ID              string
	ShortID         string
	Type            string
	Chain           string
	ChainLogoURL    string
	Symbol          string
	LogoURL         string
	AmountRaw       string
	AmountDisplay   string
	AmountSort      string
	Status          string
	StatusLabel     string
	StatusClass     string
	Hash            string
	HashShort       string
	ExplorerURL     string
	FromAddress     string
	FromAddressFull string
	ToAddress       string
	ToAddressFull   string
	ProductID       string
	UserID          string
	WebhookStatus   string
	WebhookAttempts uint
	CreatedAt       string
	CreatedSort     string
	SearchText      string
}

type DealerAuditLogView struct {
	ID          string
	Event       string
	Status      string
	Actor       string
	ActorRole   string
	Decision    string
	Subject     string
	Description string
	Reason      string
	BeforeAfter string
	IPAddress   string
	UserAgent   string
	Method      string
	Path        string
	CreatedAt   string
	CreatedSort string
	CreatedISO  string
	IsOIDC      bool
	IsFailed    bool
}

type oidcUserInfo struct {
	Sub           string              `json:"sub"`
	Email         string              `json:"email"`
	EmailVerified *flexibleBool       `json:"email_verified"`
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
		return errors.New("OIDC email_verified claim'i geçerli bir boolean olmalı")
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		value = strings.TrimSpace(unquoted)
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return errors.New("OIDC email_verified claim'i geçerli bir boolean olmalı")
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

		if err := provisionMerchantReserve(c.Context(), merchant.ID, deps); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "dealer.reserve_provision", "failed", "merchant", merchant.ID.String(), "Reserve cüzdanı hazırlanamadı: "+err.Error())
			setDealerSessionCookie(c, merchant.ID.String())
			return redirectWithError(c, "/merchant/dashboard", "Hesap oluşturuldu ancak reserve cüzdanı hazırlanamadı. Lütfen tekrar deneyin.")
		}

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
		if dealerRefundsRouteRequested(c) {
			return redirectDealerRefundsRoute(c)
		}
		activePanel := currentDashboardPanel(c)
		productsTab := integrationsDashboardTab(c)
		activityTab := activityDashboardTab(c)
		isOverview := activePanel == "" || activePanel == "overview"
		needsDomains := isOverview || activePanel == "domains" || activePanel == "products"
		needsProducts := isOverview || activePanel == "domains" || activePanel == "products"
		needsPayments := isOverview || activePanel == "domains" || activePanel == "products" || activePanel == "activity"
		needsLedger := activePanel == "treasury" || activePanel == "withdrawals"

		var domains []models.Domain
		var err error
		if needsDomains {
			domains, err = deps.DomainService.ListByMerchant(c.Context(), merchant.ID)
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Domain listesi okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		var withdrawals []models.WithdrawalRequest
		if activePanel == "withdrawals" {
			withdrawals, err = deps.WithdrawalRepo.ListByMerchant(c.Context(), merchant.ID, 100)
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Çekim talepleri okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		depositPage := 1
		depositLimit := 1
		transactionPaginationBase := merchantDashboardActivityDepositsURL
		loadsTransactionRows := activePanel == "transactions" || (activePanel == "activity" && activityTab == "deposits")
		if loadsTransactionRows {
			depositLimit = merchantDashboardPageLimit(c)
			if activePanel == "transactions" {
				transactionPaginationBase = merchantDashboardTransactionsURL
			}
			if activePanel == "transactions" || activityTab == "deposits" {
				depositPage = max(1, parseQueryInt(c.Query("page"), 1))
			}
		}
		var transactions []models.Transaction
		var depositTotal int64
		if loadsTransactionRows || isOverview || activePanel == "activity" {
			transactions, depositTotal, err = deps.TransactionRepo.ListByMerchantPage(c.Context(), merchant.ID, depositPage, depositLimit)
			if err == nil {
				if lastPage := totalPagesFor(depositTotal, depositLimit); lastPage > 0 && depositPage > lastPage {
					depositPage = lastPage
					transactions, depositTotal, err = deps.TransactionRepo.ListByMerchantPage(c.Context(), merchant.ID, depositPage, depositLimit)
				}
			}
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "İşlem geçmişi okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		var products []models.Product
		if needsProducts && deps.ProductRepo != nil {
			products, err = deps.ProductRepo.ListByMerchant(c.Context(), merchant.ID, 100)
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Ürün listesi okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		paymentStatusFilter := strings.TrimSpace(c.Query("status"))
		paymentPage := 1
		paymentLimit := 1
		paymentPaginationBase := merchantDashboardLinksURL
		if activePanel == "activity" && activityTab == "payments" {
			paymentLimit = merchantDashboardPageLimit(c)
			paymentPage = max(1, parseQueryInt(c.Query("page"), 1))
			paymentPaginationBase = activityPaymentPaginationBase(paymentStatusFilter)
		} else if activePanel == "products" && productsTab == "links" {
			paymentPage = max(1, parseQueryInt(c.Query("page"), 1))
			paymentLimit = merchantDashboardPageLimit(c)
			paymentPaginationBase = merchantDashboardLinksURL
		}
		var payments []models.PaymentSession
		var paymentTotal int64
		if needsPayments && deps.PaymentRepo != nil {
			payments, paymentTotal, err = deps.PaymentRepo.ListByMerchantPage(c.Context(), merchant.ID, paymentStatusFilter, paymentPage, paymentLimit)
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Checkout listesi okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
			if lastPage := totalPagesFor(paymentTotal, paymentLimit); lastPage > 0 && paymentPage > lastPage {
				paymentPage = lastPage
				payments, paymentTotal, err = deps.PaymentRepo.ListByMerchantPage(c.Context(), merchant.ID, paymentStatusFilter, paymentPage, paymentLimit)
				if err != nil {
					data := dealerPageData("Üye işyeri paneli", "dashboard")
					fillDealerMerchant(&data, merchant)
					data.Error = "Checkout listesi okunamadı: " + err.Error()
					return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
				}
			}
		}
		var ledgerBalances []repositories.LedgerBalanceRow
		if needsLedger && deps.LedgerRepo != nil {
			ledgerBalances, err = deps.LedgerRepo.MerchantBalances(c.Context(), merchant.ID)
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Ledger bakiyesi okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		var auditLogs []models.ActivityLog
		auditPage := 1
		auditLimit := 1
		var auditTotal int64
		if activePanel == "activity" && activityTab == "audit" {
			auditLimit = merchantDashboardPageLimit(c)
			auditPage = max(1, parseQueryInt(c.Query("page"), 1))
		}
		if activePanel == "activity" && deps.ActivityLogRepo != nil {
			auditLogs, auditTotal, err = deps.ActivityLogRepo.ListPage(c.Context(), auditPage, auditLimit, &merchant.ID)
			if err == nil {
				if lastPage := totalPagesFor(auditTotal, auditLimit); lastPage > 0 && auditPage > lastPage {
					auditPage = lastPage
					auditLogs, auditTotal, err = deps.ActivityLogRepo.ListPage(c.Context(), auditPage, auditLimit, &merchant.ID)
				}
			}
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Activity log okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}

		const walletPageSize = 20
		walletPage := 1
		if activePanel == "users" {
			walletPage = max(1, parseQueryInt(c.Query("page"), 1))
		}
		walletSearch := strings.TrimSpace(c.Query("q"))
		walletLimit := walletPageSize
		if isOverview {
			walletLimit = 1
			walletSearch = ""
		}
		var wallets []models.Wallet
		var walletTotal int64
		if activePanel == "users" || isOverview {
			walletOffset := (walletPage - 1) * walletLimit
			wallets, walletTotal, err = deps.WalletRepo.SearchByMerchantPage(c.Context(), merchant.ID, walletSearch, walletLimit, walletOffset)
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Kullanıcı cüzdanları okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
			if lastPage := totalPagesFor(walletTotal, walletLimit); lastPage > 0 && walletPage > lastPage {
				walletPage = lastPage
				walletOffset = (walletPage - 1) * walletLimit
				wallets, walletTotal, err = deps.WalletRepo.SearchByMerchantPage(c.Context(), merchant.ID, walletSearch, walletLimit, walletOffset)
				if err != nil {
					data := dealerPageData("Üye işyeri paneli", "dashboard")
					fillDealerMerchant(&data, merchant)
					data.Error = "Kullanıcı cüzdanları okunamadı: " + err.Error()
					return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
				}
			}
		}
		var reserveWallets []models.Wallet
		if activePanel == "withdrawals" {
			reserveWallets, err = deps.WalletRepo.ListReserveByMerchant(c.Context(), merchant.ID)
			if err != nil {
				data := dealerPageData("Üye işyeri paneli", "dashboard")
				fillDealerMerchant(&data, merchant)
				data.Error = "Reserve cüzdanlar okunamadı: " + err.Error()
				return c.Status(fiber.StatusInternalServerError).Render("dealer/dashboard", data, "dealer/layout")
			}
		}
		var withdrawalBalanceMap map[uuid.UUID][]DealerWalletBalanceRow
		if activePanel == "withdrawals" {
			withdrawalBalanceMap = buildWithdrawalWalletBalanceMap(c.Context(), deps.LedgerRepo, reserveWallets, deps.AssetRegistry, merchant.HideTestnets, merchant.HiddenChains)
		}

		data := dealerPageData("Üye işyeri paneli", "dashboard")
		fillDealerMerchant(&data, merchant)
		data.ActivePanel = activePanel
		applyFlash(c, &data)
		var latestDeliveries map[uuid.UUID]models.WebhookDelivery
		var latestErr error
		if activePanel == "domains" {
			latestDeliveries, latestErr = dealerLatestWebhookDeliveries(c.Context(), deps, merchant.ID, domains)
		}
		data.Domains = dealerDomainViewsWithDeliveries(domains, latestDeliveries, latestErr != nil)
		if activePanel == "products" {
			data.PaymentAssets = dealerAssetOptions(deps.AssetRegistry)
			if productsTab == "products" {
				data.Products = dealerProductViews(c, products)
			}
		}
		if activePanel == "withdrawals" {
			data.Withdrawals = dealerWithdrawalViews(withdrawals)
			data.WithdrawalWallets = dealerWalletViewsWithBalances(reserveWallets, withdrawalBalanceMap)
		}
		if (activePanel == "activity" && activityTab == "payments") || (activePanel == "products" && productsTab == "links") {
			data.Payments = dealerPaymentViews(c, payments)
		}
		if needsLedger || isOverview {
			data.Balances = dealerLedgerBalanceViews(ledgerBalances, deps.AssetRegistry)
			data.Balances = dealerAllBalanceViews(deps.AssetRegistry, data.Balances)
			data.ChainVaults = dealerChainVaultViews(data.Balances)
		}
		if activePanel == "settings" {
			data.SettingsVisibleChains, data.SettingsHiddenChains = dealerSettingsNetworkViews(merchant.HideTestnets, merchant.HiddenChains)
		}
		if loadsTransactionRows {
			data.Activities = dealerActivityViews(transactions, deps.AssetRegistry, deps.Blockchains)
		}
		if activePanel == "activity" && activityTab == "audit" {
			data.AuditLogs = dealerAuditLogViews(auditLogs)
		}
		if activePanel == "users" {
			data.Wallets = dealerWalletViews(wallets)
		}
		usersBaseURL := "/merchant/dashboard/users"
		if walletSearch != "" {
			usersBaseURL += "?q=" + walletSearch
		}
		data.WalletPage = dealerPaginationView(walletPage, walletPageSize, walletTotal, usersBaseURL)
		data.PaymentPage = dealerPaginationView(paymentPage, paymentLimit, paymentTotal, paymentPaginationBase)
		data.TransactionPage = dealerPaginationView(depositPage, depositLimit, depositTotal, transactionPaginationBase)
		data.AuditPage = dealerPaginationView(auditPage, auditLimit, auditTotal, merchantDashboardActivityAuditURL)
		data.ActivityPaymentPage = dealerPaginationView(paymentPage, paymentLimit, paymentTotal, activityPaymentPaginationBase(paymentStatusFilter))
		data.ActivityDepositPage = dealerPaginationView(depositPage, depositLimit, depositTotal, merchantDashboardActivityDepositsURL)
		data.WalletSearch = walletSearch
		data.WalletCount = int(walletTotal)
		data.DomainCount = len(domains)
		data.ProductCount = len(products)
		data.PaymentCount = int(paymentTotal)
		data.WithdrawalCount = len(withdrawals)
		data.PaymentStatusFilter = paymentStatusFilter
		data.ProductsTab = productsTab
		data.ActivityTab = activityTab
		data.DepositCount = int(depositTotal)
		if activePanel == "activity" && activityTab == "payments" && deps.PaymentRepo != nil {
			paymentStats, err := deps.PaymentRepo.StatsByMerchant(c.Context(), merchant.ID)
			if err != nil {
				log.Printf("merchant dashboard payment stats merchant_id=%s error=%v", merchant.ID, err)
			} else {
				data.PaymentStats = paymentStats
			}
		}
		data.HideTestnets = merchant.HideTestnets
		data.HiddenChains = merchant.HiddenChains
		if activePanel != "settings" && (merchant.HideTestnets || merchant.HiddenChains != "") {
			data.Balances = filterBalancesBySettings(data.Balances, merchant.HideTestnets, merchant.HiddenChains)
			data.ChainVaults = filterVaultsBySettings(data.ChainVaults, merchant.HideTestnets, merchant.HiddenChains)
		}
		if activePanel == "withdrawals" {
			data.WithdrawalAssets = dealerWithdrawalAssetViews(data.Balances, deps.AssetRegistry)
		}
		if activePanel == "treasury" {
			data.TreasuryGroups = dealerTreasuryBalanceGroups(data.Balances, deps.AssetRegistry)
		}
		data.AssetCount = len(data.Balances)
		data.NetworkCount = len(data.ChainVaults)
		if activePanel == "settings" {
			data.NetworkCount = len(data.SettingsVisibleChains) + len(data.SettingsHiddenChains)
		}
		data.ActivityCount = int(auditTotal + depositTotal + paymentTotal)
		if strings.EqualFold(strings.TrimSpace(c.Get("X-Merchant-Navigation")), "partial") {
			c.Vary("X-Merchant-Navigation")
			return c.Render("dealer/dashboard", data)
		}
		return c.Render("dealer/dashboard", data, "dealer/layout")
	}
}

const (
	merchantDashboardActivityAuditURL    = "/merchant/dashboard/activity/audit"
	merchantDashboardActivityPaymentsURL = "/merchant/dashboard/activity/payments"
	merchantDashboardActivityDepositsURL = "/merchant/dashboard/activity/deposits"
	merchantDashboardTransactionsURL     = "/merchant/dashboard/transactions"
	merchantDashboardProductsURL         = "/merchant/dashboard/products/index"
	merchantDashboardLinksURL            = "/merchant/dashboard/products/links"
	merchantDashboardDomainsURL          = "/merchant/dashboard/domains"
	merchantDashboardIntegrationsURL     = merchantDashboardDomainsURL
	merchantDashboardRefundsRedirectURL  = merchantDashboardActivityPaymentsURL + "?status=paid"
)

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
		notificationMode := models.NormalizeDomainNotificationMode(c.FormValue("notification_mode"))
		webhookURL := strings.TrimSpace(c.FormValue("webhook_url"))
		webhookSecret := strings.TrimSpace(c.FormValue("webhook_secret"))
		natsURL := strings.TrimSpace(c.FormValue("nats_url"))
		natsSubject := strings.TrimSpace(c.FormValue("nats_subject"))
		merchantID := merchant.ID.String()
		params := types.DomainParams{
			Context:          c.Context(),
			MerchantID:       &merchantID,
			DomainURL:        &domainURL,
			NotificationMode: notificationMode,
			WebhookURL:       &webhookURL,
			WebhookSecret:    &webhookSecret,
			NATSURL:          &natsURL,
			NATSSubject:      &natsSubject,
		}
		if err := params.Validate(); err != nil {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "failed", "domain", domainURL, err.Error())
			return redirectWithError(c, merchantDashboardDomainsURL, err.Error())
		}
		if err := helpers.ValidateDomainHost(domainURL); err != nil {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "failed", "domain", domainURL, err.Error())
			return redirectWithError(c, merchantDashboardDomainsURL, "Geçersiz domain: "+err.Error())
		}
		if notificationMode == models.DomainNotificationWebhook {
			if err := helpers.ValidateWebhookURL(webhookURL); err != nil {
				logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "failed", "domain", domainURL, err.Error())
				return redirectWithError(c, merchantDashboardDomainsURL, "Geçersiz webhook URL: "+err.Error())
			}
		} else if err := helpers.ValidateNATSURL(natsURL); err != nil {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "failed", "domain", domainURL, err.Error())
			return redirectWithError(c, merchantDashboardDomainsURL, "Geçersiz NATS adresi: "+err.Error())
		}
		domain, err := domainService.Create(params)
		if err != nil {
			logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "failed", "domain", domainURL, err.Error())
			return redirectWithError(c, merchantDashboardDomainsURL, "Domain eklenemedi: "+err.Error())
		}
		subjectID := domainURL
		if domain != nil {
			subjectID = domain.ID.String()
		}
		logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "domain.create", "success", "domain", subjectID, "Domain ve webhook endpoint oluşturuldu.")
		return redirectWithSuccess(c, merchantDashboardDomainsURL, "Domain eklendi.")
	}
}

func HandleDealerProductCreate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}
		if deps.ProductRepo == nil {
			return redirectWithError(c, merchantDashboardProductsURL, "Product repository hazır değil.")
		}

		form, err := parseDealerProductForm(c, deps, merchant)
		if err != nil {
			return redirectWithError(c, merchantDashboardProductsURL, err.Error())
		}

		product := &models.Product{
			MerchantID:     merchant.ID,
			DomainID:       form.domain.ID,
			Name:           form.name,
			Description:    form.description,
			LinkType:       form.linkType,
			Amount:         form.amount,
			Currency:       form.currency,
			Language:       form.language,
			SuccessURL:     form.successURL,
			CancelURL:      form.cancelURL,
			X402Enabled:    form.x402Enabled,
			LogoURL:        form.logoURL,
			DefaultChainID: form.defaultChainID,
			DefaultSymbol:  form.defaultSymbol,
			DefaultToken:   form.defaultToken,
			IsActive:       true,
		}
		if err := deps.ProductRepo.Create(c.Context(), product); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "product.create", "failed", "product", form.name, err.Error())
			return redirectWithError(c, merchantDashboardProductsURL, "Ürün oluşturulamadı: "+err.Error())
		}
		activityMessage := "Payment link ürünü oluşturuldu."
		if models.IsDonationLinkType(product.LinkType) {
			activityMessage = "Donation payment link oluşturuldu."
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "product.create", "success", "product", product.ID.String(), activityMessage)
		link := baseURL(c) + "/payment-links/" + product.LinkToken
		return redirectWithSuccess(c, merchantDashboardProductsURL, "Payment link oluşturuldu: "+link)
	}
}

func HandleDealerProductUpdate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}
		if deps.ProductRepo == nil {
			return redirectWithError(c, merchantDashboardProductsURL, "Product repository hazır değil.")
		}

		productID, err := uuid.Parse(strings.TrimSpace(c.Params("id")))
		if err != nil {
			return redirectWithError(c, merchantDashboardProductsURL, "Geçersiz payment link.")
		}
		product, err := deps.ProductRepo.FindByID(c.Context(), productID)
		if err != nil || product.MerchantID != merchant.ID {
			return redirectWithError(c, merchantDashboardProductsURL, "Payment link bulunamadı.")
		}

		form, err := parseDealerProductForm(c, deps, merchant)
		if err != nil {
			return redirectWithError(c, merchantDashboardProductsURL, err.Error())
		}

		product.DomainID = form.domain.ID
		product.Name = form.name
		product.Description = form.description
		product.LinkType = form.linkType
		product.Amount = form.amount
		product.Currency = form.currency
		product.Language = form.language
		product.SuccessURL = form.successURL
		product.CancelURL = form.cancelURL
		product.X402Enabled = form.x402Enabled
		product.LogoURL = form.logoURL
		product.DefaultChainID = form.defaultChainID
		product.DefaultSymbol = form.defaultSymbol
		product.DefaultToken = form.defaultToken

		if err := deps.ProductRepo.Update(c.Context(), product); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "product.update", "failed", "product", product.ID.String(), err.Error())
			return redirectWithError(c, merchantDashboardProductsURL, "Payment link güncellenemedi: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "product.update", "success", "product", product.ID.String(), "Payment link güncellendi.")
		return redirectWithSuccess(c, merchantDashboardProductsURL, "Payment link güncellendi.")
	}
}

type dealerProductFormData struct {
	domain         *models.Domain
	name           string
	description    string
	linkType       string
	amount         string
	currency       string
	language       string
	successURL     string
	cancelURL      string
	x402Enabled    bool
	logoURL        string
	defaultChainID *int64
	defaultSymbol  string
	defaultToken   *string
}

func parseDealerProductForm(c fiber.Ctx, deps DealerDeps, merchant *models.Merchant) (dealerProductFormData, error) {
	var form dealerProductFormData
	if merchant == nil {
		return form, errors.New("Merchant oturumu bulunamadı.")
	}
	domainIDRaw := strings.TrimSpace(c.FormValue("domain_id"))
	domainID, err := uuid.Parse(domainIDRaw)
	if err != nil {
		return form, errors.New("Geçerli domain seçmelisin.")
	}
	domainIDString := domainID.String()
	domain, err := deps.DomainService.FindByID(types.DomainParams{
		Context:  c.Context(),
		DomainID: &domainIDString,
	})
	if err != nil || domain.MerchantID != merchant.ID {
		return form, errors.New("Domain bulunamadı.")
	}

	form.domain = domain
	form.name = strings.TrimSpace(c.FormValue("name"))
	form.description = strings.TrimSpace(c.FormValue("description"))
	form.amount = strings.TrimSpace(c.FormValue("amount"))
	form.currency = strings.ToUpper(strings.TrimSpace(c.FormValue("currency")))
	form.linkType = models.NormalizePaymentLinkType(c.FormValue("link_type"))
	form.language = normalizeLanguage(c.FormValue("language"))
	form.successURL = strings.TrimSpace(c.FormValue("success_url"))
	form.cancelURL = strings.TrimSpace(c.FormValue("cancel_url"))
	form.x402Enabled = parseBooleanFormValue(c.FormValue("x402_enabled"))
	form.logoURL = strings.TrimSpace(c.FormValue("logo_url"))
	defaultAsset := strings.TrimSpace(c.FormValue("default_asset"))
	if defaultAsset != "" {
		selectedAsset, err := parseAdminAssetSelection(deps.AssetRegistry, defaultAsset)
		if err != nil {
			return form, errors.New("Geçerli bir varsayılan asset seçmelisin.")
		}
		if err := networkops.RequireDeposits(c.Context(), deps.NetworkOperationalStateRepo, selectedAsset.GetChainID()); err != nil {
			return form, errors.New(dealerDepositAvailabilityError(err))
		}
		chainID := int64(selectedAsset.GetChainID())
		form.defaultChainID = &chainID
		form.defaultSymbol = strings.ToUpper(strings.TrimSpace(selectedAsset.GetSymbol()))
		form.defaultToken = tokenForSelectedAsset(selectedAsset)
	}
	if form.name == "" {
		return form, errors.New("Ürün adı zorunlu.")
	}
	if models.IsDonationLinkType(form.linkType) {
		if form.x402Enabled {
			return form, errors.New("x402 yalnızca sabit tutarlı payment link'lerde kullanılabilir.")
		}
		form.amount = "0"
		form.currency = ""
		return form, nil
	}
	if err := types.ValidatePositiveDecimal(form.amount); err != nil {
		return form, errors.New("Tutar pozitif decimal olmalı.")
	}
	if form.currency == "" {
		form.currency = "USD"
	}
	return form, nil
}

func parseBooleanFormValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func HandleDealerInvoiceCreate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}
		if deps.PaymentRepo == nil || deps.WalletRepo == nil {
			return redirectWithError(c, merchantDashboardLinksURL, "Invoice altyapısı hazır değil.")
		}

		domainIDRaw := strings.TrimSpace(c.FormValue("domain_id"))
		domainID, err := uuid.Parse(domainIDRaw)
		if err != nil {
			return redirectWithError(c, merchantDashboardLinksURL, "Geçerli domain seçmelisin.")
		}
		domainIDString := domainID.String()
		domain, err := deps.DomainService.FindByID(types.DomainParams{
			Context:  c.Context(),
			DomainID: &domainIDString,
		})
		if err != nil || domain.MerchantID != merchant.ID {
			return redirectWithError(c, merchantDashboardLinksURL, "Domain bulunamadı.")
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
		if assetValue := strings.TrimSpace(c.FormValue("asset")); assetValue != "" {
			selectedAsset, assetErr := parseAdminAssetSelection(deps.AssetRegistry, assetValue)
			if assetErr != nil {
				return redirectWithError(c, merchantDashboardLinksURL, assetErr.Error())
			}
			chainID := int64(selectedAsset.GetChainID())
			symbol := strings.ToUpper(strings.TrimSpace(selectedAsset.GetSymbol()))
			params.ChainID = &chainID
			params.Symbol = &symbol
			params.Token = optionalStringPtr(valueOrDefault(tokenForSelectedAsset(selectedAsset), ""))
		}
		if err := params.Validate(); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "invoice.create", "failed", "payment", orderID, err.Error())
			return redirectWithError(c, merchantDashboardLinksURL, "Invoice oluşturulamadı: "+err.Error())
		}
		if params.ChainID != nil {
			if err := networkops.RequireDeposits(c.Context(), deps.NetworkOperationalStateRepo, constants.ChainID(*params.ChainID)); err != nil {
				return redirectWithError(c, merchantDashboardLinksURL, dealerDepositAvailabilityError(err))
			}
		}
		now := time.Now()
		expiresAt := now.Add(paymentSessionTTL())
		var selection *paymentCreateAssetSelection
		if params.ChainID != nil {
			selection, err = preparePaymentCreateAssetSelection(c.Context(), deps.AssetRegistry, deps.PriceOracle, params, expiresAt, now)
			if err != nil {
				return redirectWithError(c, merchantDashboardLinksURL, "Asset seçilemedi: "+err.Error())
			}
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
			return redirectWithError(c, merchantDashboardLinksURL, "Wallet oluşturulamadı: "+err.Error())
		}

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
			return redirectWithError(c, merchantDashboardLinksURL, "Invoice oluşturulamadı: "+err.Error())
		}
		if selection != nil {
			if _, selectionErr := applyPaymentCreateAssetSelection(c.Context(), deps.PaymentRepo, session, *wallet, selection); selectionErr != nil {
				return redirectWithError(c, merchantDashboardLinksURL, "Asset seçilemedi: "+selectionErr.Error())
			}
		}

		checkoutURL := baseURL(c) + "/checkout/" + session.SessionToken
		invoiceURL := baseURL(c) + "/invoice/" + session.SessionToken
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "invoice.create", "success", "payment", session.ID.String(), "Dashboard invoice oluşturuldu.")
		return redirectWithSuccess(c, merchantDashboardLinksURL, "Invoice oluşturuldu: "+invoiceURL+" | Checkout: "+checkoutURL)
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
		productSnapshot, err := models.MarshalPaymentProductSnapshot(*product)
		if err != nil {
			return renderPaymentLinkError(c, "Ürün bilgisi hazırlanamadı: "+err.Error())
		}
		linkType := models.NormalizePaymentLinkType(product.LinkType)
		now := time.Now()
		expiresAt := paymentLinkSessionExpiresAt(linkType, now)
		var selection *paymentCreateAssetSelection
		if product.DefaultChainID != nil {
			chainID := *product.DefaultChainID
			if err := networkops.RequireDeposits(c.Context(), deps.NetworkOperationalStateRepo, constants.ChainID(chainID)); err != nil {
				return renderPaymentLinkError(c, dealerDepositAvailabilityError(err))
			}
			symbol := strings.ToUpper(strings.TrimSpace(product.DefaultSymbol))
			params := types.PaymentCreateParams{
				Context:  c.Context(),
				Amount:   stringPtr(product.Amount),
				Currency: stringPtr(product.Currency),
				ChainID:  &chainID,
				Symbol:   &symbol,
				Token:    optionalStringPtr(valueOrDefault(product.DefaultToken, "")),
			}
			selection, err = preparePaymentCreateAssetSelection(c.Context(), deps.AssetRegistry, deps.PriceOracle, params, expiresAtValue(expiresAt), now)
			if err != nil {
				return renderPaymentLinkError(c, "Varsayılan asset seçilemedi: "+err.Error())
			}
		}
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

		session := &models.PaymentSession{
			MerchantID:      product.MerchantID,
			DomainID:        product.DomainID,
			WalletID:        wallet.ID,
			OrderID:         orderID,
			ProductID:       productID,
			UserID:          userID,
			LinkType:        linkType,
			Amount:          product.Amount,
			Currency:        product.Currency,
			ProductSnapshot: productSnapshot,
			SuccessURL:      product.SuccessURL,
			CancelURL:       product.CancelURL,
			X402Enabled:     product.X402Enabled,
			Status:          models.PaymentStatusPending,
			ExpiresAt:       expiresAt,
		}
		if err := deps.PaymentRepo.Create(c.Context(), session); err != nil {
			return renderPaymentLinkError(c, "Checkout oluşturulamadı: "+err.Error())
		}
		if selection != nil {
			if _, selectionErr := applyPaymentCreateAssetSelection(c.Context(), deps.PaymentRepo, session, *wallet, selection); selectionErr != nil {
				return renderPaymentLinkError(c, "Varsayılan asset seçilemedi: "+selectionErr.Error())
			}
		}
		return c.Redirect().To("/checkout/" + session.SessionToken + "?lang=" + url.QueryEscape(language))
	}
}

func dealerDepositAvailabilityError(err error) string {
	if err == nil {
		return ""
	}
	var unavailable *networkops.UnavailableError
	if errors.As(err, &unavailable) {
		if reason := strings.TrimSpace(unavailable.Reason); reason != "" {
			return "Seçilen network geçici olarak ödeme kabul etmiyor: " + reason
		}
		return "Seçilen network geçici olarak ödeme kabul etmiyor."
	}
	return "Network kullanılabilirliği doğrulanamadı. Lütfen kısa süre sonra tekrar deneyin."
}

func expiresAtValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func paymentLinkSessionExpiresAt(linkType string, now time.Time) *time.Time {
	if models.IsDonationLinkType(linkType) {
		return nil
	}
	expiresAt := now.Add(paymentSessionTTL())
	return &expiresAt
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
			return redirectWithError(c, "/merchant/dashboard/withdrawals", "Geçerli wallet seçmelisin.")
		}
		wallet, err := deps.WalletRepo.FindByID(c.Context(), walletID)
		if err != nil || wallet.MerchantID != merchant.ID {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, "Wallet bulunamadı veya merchant ile eşleşmedi.")
			return redirectWithError(c, "/merchant/dashboard/withdrawals", "Wallet bulunamadı.")
		}
		if wallet.HDAddressId != 0 {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, "Sadece reserve (HD index 0) cüzdandan çekim yapılabilir.")
			return redirectWithError(c, "/merchant/dashboard/withdrawals", "Çekim sadece üye işyeri reserve cüzdanından yapılabilir.")
		}

		chain := strings.ToLower(strings.TrimSpace(c.FormValue("chain")))
		symbol := strings.TrimSpace(c.FormValue("symbol"))
		tokenAddress := strings.TrimSpace(c.FormValue("token_address"))
		if assetValue := strings.TrimSpace(c.FormValue("asset")); assetValue != "" {
			selectedAsset, err := parseAdminAssetSelection(deps.AssetRegistry, assetValue)
			if err != nil {
				logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, err.Error())
				return redirectWithError(c, "/merchant/dashboard/withdrawals", err.Error())
			}
			chain = constants.ChainName(selectedAsset.GetChainID())
			symbol = selectedAsset.GetSymbol()
			tokenAddress = ""
			if !selectedAsset.IsNative() {
				tokenAddress = selectedAsset.GetIdentifier()
			}
		}
		toAddress := strings.TrimSpace(c.FormValue("to_address"))
		amount := strings.TrimSpace(c.FormValue("amount"))
		if amount == "" {
			amount = strings.TrimSpace(c.FormValue("amount_raw"))
		}
		note := strings.TrimSpace(c.FormValue("note"))
		chain, token, symbol, decimals, err := resolveWithdrawalAsset(deps.AssetRegistry, chain, symbol, tokenAddress)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, err.Error())
			return redirectWithError(c, "/merchant/dashboard/withdrawals", err.Error())
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
			return redirectWithError(c, "/merchant/dashboard/withdrawals", err.Error())
		}

		requestID := uuid.New()
		domainID := wallet.DomainID
		idempotencyKey := strings.TrimSpace(c.Get("Idempotency-Key"))
		correlationID := dealerSignerCorrelationID(c, "withdrawal:"+requestID.String())
		if err := validateV1CreateMetadata(idempotencyKey, correlationID); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, err.Error())
			return redirectWithError(c, "/merchant/dashboard/withdrawals", err.Error())
		}
		request := &models.WithdrawalRequest{
			ID:             requestID,
			MerchantID:     merchant.ID,
			DomainID:       &domainID,
			WalletID:       wallet.ID,
			Chain:          *params.Chain,
			Token:          token,
			Symbol:         symbol,
			Decimals:       decimals,
			ToAddress:      *params.ToAddress,
			AmountRaw:      *params.AmountRaw,
			Note:           note,
			Status:         models.WithdrawalStatusPending,
			RequestedBy:    merchant.Email,
			IdempotencyKey: idempotencyKey,
			CorrelationID:  correlationID,
		}
		if err := enforceDealerOutboundPolicy(c.Context(), deps, outboundPolicyCheckFromWithdrawal("withdrawal.create", request, false)); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", request.ID.String(), err.Error())
			return redirectWithError(c, "/merchant/dashboard/withdrawals", err.Error())
		}
		if err := deps.WithdrawalRepo.CreateWithHold(c.Context(), request, deps.LedgerRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "failed", "withdrawal", walletIDRaw, err.Error())
			return redirectWithError(c, "/merchant/dashboard/withdrawals", "Çekim talebi oluşturulamadı: "+err.Error())
		}
		if deliveryID := enqueueDealerPayoutLifecycle(c.Context(), deps, *request, constants.WebhookEventPayoutRequestedV1); deps.WebhookDeliveryRepo != nil && deliveryID == uuid.Nil {
			openDealerOutboundLifecycleReconciliation(c.Context(), deps, request.Chain, &request.MerchantID, request.DomainID, "withdrawal", request.ID.String(), request.Status, "outbound_requested_event_enqueue_failed", "requested lifecycle enqueue failed", request.TxHash)
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "withdrawal.create", "success", "withdrawal", request.ID.String(), "Çekim talebi admin onayına gönderildi.")
		return redirectWithSuccess(c, "/merchant/dashboard/withdrawals", "Çekim talebi admin onayına gönderildi.")
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
		if domain.UsesNATS() {
			if deps.Notifier == nil {
				return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"success": false, "error": "Bildirim servisi hazır değil"})
			}
			eventID := "test-" + uuid.New().String()
			testPayload := map[string]interface{}{
				"event_id":      eventID,
				"event_type":    "test",
				"event_version": constants.WebhookEventVersionV1,
				"merchant_id":   merchant.ID.String(),
				"domain_id":     domain.ID.String(),
				"message":       "Bu bir test bildirimidir.",
				"sent_at":       time.Now().UTC().Format(time.RFC3339),
			}
			body, _ := json.Marshal(testPayload)
			if err := deps.Notifier.DeliverRaw(c.Context(), *domain, "test", eventID, constants.WebhookEventVersionV1, body); err != nil {
				return c.JSON(fiber.Map{"success": false, "error": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "status_code": 200, "response": "NATS subject'ine publish edildi."})
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
		defer func() { _ = resp.Body.Close() }()
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
			return redirectWithError(c, merchantDashboardDomainsURL, "Webhook URL ve secret boş olamaz")
		}
		if err := helpers.ValidateWebhookURL(webhookURL); err != nil {
			return redirectWithError(c, merchantDashboardDomainsURL, "Geçersiz webhook URL: "+err.Error())
		}

		if err := deps.DomainService.UpdateWebhook(c.Context(), domainUUID, merchant.ID, webhookURL, webhookSecret); err != nil {
			return redirectWithError(c, merchantDashboardDomainsURL, "Güncelleme hatası: "+err.Error())
		}

		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.update_webhook", "success", "domain", domainIDStr, "Webhook URL ve secret güncellendi.")
		return redirectWithSuccess(c, merchantDashboardDomainsURL, "Webhook başarıyla güncellendi.")
	}
}

func HandleDealerDomainUpdate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectDealerLogin(c)
		}
		if deps.DomainService == nil {
			return redirectWithError(c, merchantDashboardDomainsURL, "Domain servisi hazır değil.")
		}

		domainIDStr := strings.TrimSpace(c.Params("id"))
		domainUUID, err := uuid.Parse(domainIDStr)
		if err != nil {
			return redirectWithError(c, merchantDashboardDomainsURL, "Geçersiz domain ID.")
		}

		domain, err := deps.DomainService.FindByID(types.DomainParams{
			Context:  c.Context(),
			DomainID: &domainIDStr,
		})
		if err != nil || domain.MerchantID != merchant.ID {
			return redirectWithError(c, merchantDashboardDomainsURL, "Domain bulunamadı.")
		}

		domainURL := strings.TrimSpace(c.FormValue("domain_url"))
		notificationMode := models.NormalizeDomainNotificationMode(c.FormValue("notification_mode"))
		webhookURL := strings.TrimSpace(c.FormValue("webhook_url"))
		webhookSecret := strings.TrimSpace(c.FormValue("webhook_secret"))
		natsURL := strings.TrimSpace(c.FormValue("nats_url"))
		natsSubject := strings.TrimSpace(c.FormValue("nats_subject"))
		if domainURL == "" {
			return redirectWithError(c, merchantDashboardDomainsURL, "Domain boş olamaz.")
		}
		if notificationMode == models.DomainNotificationWebhook {
			if webhookURL == "" {
				return redirectWithError(c, merchantDashboardDomainsURL, "Webhook URL boş olamaz.")
			}
			if webhookSecret == "" && strings.TrimSpace(domain.WebhookSecret) == "" {
				return redirectWithError(c, merchantDashboardDomainsURL, "Webhook secret gerekli.")
			}
		} else if natsURL == "" {
			return redirectWithError(c, merchantDashboardDomainsURL, "NATS adresi boş olamaz.")
		}
		if err := helpers.ValidateDomainHost(domainURL); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.update", "failed", "domain", domainIDStr, err.Error())
			return redirectWithError(c, merchantDashboardDomainsURL, "Geçersiz domain: "+err.Error())
		}
		if notificationMode == models.DomainNotificationWebhook {
			if err := helpers.ValidateWebhookURL(webhookURL); err != nil {
				logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.update", "failed", "domain", domainIDStr, err.Error())
				return redirectWithError(c, merchantDashboardDomainsURL, "Geçersiz webhook URL: "+err.Error())
			}
		} else {
			if err := helpers.ValidateNATSURL(natsURL); err != nil {
				logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.update", "failed", "domain", domainIDStr, err.Error())
				return redirectWithError(c, merchantDashboardDomainsURL, "Geçersiz NATS adresi: "+err.Error())
			}
			if err := helpers.ValidateNATSSubject(natsSubject); err != nil {
				return redirectWithError(c, merchantDashboardDomainsURL, "Geçersiz NATS subject: "+err.Error())
			}
		}

		var secretPtr *string
		if webhookSecret != "" {
			secretPtr = &webhookSecret
		}
		if err := deps.DomainService.UpdateConfiguration(c.Context(), domainUUID, merchant.ID, domainURL, notificationMode, webhookURL, secretPtr, natsURL, natsSubject); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.update", "failed", "domain", domainIDStr, err.Error())
			return redirectWithError(c, merchantDashboardDomainsURL, "Domain güncellenemedi: "+err.Error())
		}

		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.update", "success", "domain", domainIDStr, "Domain bilgileri güncellendi.")
		return redirectWithSuccess(c, merchantDashboardDomainsURL, "Domain güncellendi.")
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
		if !dealerRotateAPISecretConfirmed(c, dealerRotateAPISecretConfirmation(domainIDStr)) {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.rotate_api_secret", "failed", "domain", domainIDStr, "rotation confirmation missing")
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Rotation confirmation required"})
		}
		apiSecret, err := deps.DomainService.RotateAPISecret(c.Context(), domainUUID, merchant.ID)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "domain.rotate_api_secret", "failed", "domain", domainIDStr, err.Error())
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
		hiddenChains, err := canonicalHiddenChains(c.FormValue("hidden_chains"))
		if err != nil {
			return redirectWithError(c, "/merchant/dashboard/settings", err.Error())
		}
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
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.manage", "failed", "admin", "", err.Error())
			return redirectWithError(c, "/admin", err.Error())
		}
		page, limit := adminDashboardPageParams(c)
		admins, err := deps.AdminRepo.List(c.Context())
		if err != nil {
			return redirectWithError(c, "/admin", "Admin listesi okunamadı: "+err.Error())
		}
		data := adminPageData(adminEmail, "admins")
		data.AdminPanel = "admins"
		adminHeaderStatsFor(c.Context(), deps).applyTo(&data)
		adminViews := adminListToMerchantViews(admins)
		data.AdminMerchants = paginateViewSlice(adminViews, page, limit)
		data.AdminPagination = dealerPaginationView(page, limit, int64(len(adminViews)), "/admin/admins")
		applyFlash(c, &data)
		return c.Render("dealer/admin_dashboard", data, "dealer/layout")
	}
}

func HandleAdminCreateAdmin(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.create", "failed", "admin", "", err.Error())
			return redirectWithError(c, "/admin", err.Error())
		}
		email := strings.TrimSpace(c.FormValue("email"))
		name := strings.TrimSpace(c.FormValue("name"))
		password := c.FormValue("password")
		role := strings.TrimSpace(c.FormValue("role"))
		if email == "" || password == "" {
			return redirectWithError(c, "/admin/admins", "E-posta ve şifre zorunlu.")
		}
		admin, err := deps.AdminRepo.CreateWithRole(c.Context(), email, name, password, role)
		if err != nil {
			return redirectWithError(c, "/admin/admins", "Admin oluşturulamadı: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.create", "success", "admin", admin.ID.String(), "Admin hesabı oluşturuldu.")
		return redirectWithSuccess(c, "/admin/admins", "Admin hesabı oluşturuldu.")
	}
}

func HandleAdminToggleAdmin(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.toggle", "failed", "admin", c.Params("id"), err.Error())
			return redirectWithError(c, "/admin", err.Error())
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/admins", "Geçersiz ID.")
		}
		admin, err := deps.AdminRepo.FindByID(c.Context(), id)
		if err != nil {
			return redirectWithError(c, "/admin/admins", "Admin bulunamadı.")
		}
		newActive := !admin.IsActive
		if !newActive && adminRoleCanMutateHighRisk(admin.Role) {
			privilegedCount, err := deps.AdminRepo.CountActivePrivileged(c.Context())
			if err != nil {
				return redirectWithError(c, "/admin/admins", "Privileged admin sayısı doğrulanamadı: "+err.Error())
			}
			if privilegedCount <= 1 {
				msg := "En az bir aktif owner/security admin kalmalı."
				logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.toggle", "failed", "admin", id.String(), msg)
				return redirectWithError(c, "/admin/admins", msg)
			}
		}
		if err := deps.AdminRepo.SetActive(c.Context(), id, newActive); err != nil {
			message := "Admin durumu güncellenemedi: " + err.Error()
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.toggle", "failed", "admin", id.String(), message)
			return redirectWithError(c, "/admin/admins", message)
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.toggle", "success", "admin", id.String(), fmt.Sprintf("Admin active=%t olarak güncellendi.", newActive))
		return redirectWithSuccess(c, "/admin/admins", "Admin durumu güncellendi.")
	}
}

func HandleAdminUpdateAdminRole(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.role_update", "failed", "admin", c.Params("id"), err.Error())
			return redirectWithError(c, "/admin", err.Error())
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/admins", "Geçersiz ID.")
		}
		admin, err := deps.AdminRepo.FindByID(c.Context(), id)
		if err != nil {
			return redirectWithError(c, "/admin/admins", "Admin bulunamadı.")
		}
		role := models.NormalizeAdminRole(c.FormValue("role"))
		if admin.IsActive && adminRoleCanMutateHighRisk(admin.Role) && !adminRoleCanMutateHighRisk(role) {
			privilegedCount, err := deps.AdminRepo.CountActivePrivileged(c.Context())
			if err != nil {
				return redirectWithError(c, "/admin/admins", "Privileged admin sayısı doğrulanamadı: "+err.Error())
			}
			if privilegedCount <= 1 {
				msg := "En az bir aktif owner/security admin kalmalı."
				logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.role_update", "failed", "admin", id.String(), msg)
				return redirectWithError(c, "/admin/admins", msg)
			}
		}
		if err := deps.AdminRepo.SetRole(c.Context(), id, role); err != nil {
			return redirectWithError(c, "/admin/admins", "Admin rolü güncellenemedi: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.role_update", "success", "admin", id.String(), "Admin rolü "+role+" olarak güncellendi.")
		return redirectWithSuccess(c, "/admin/admins", "Admin rolü güncellendi.")
	}
}

func HandleAdminResetTOTP(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.reset_totp", "failed", "admin", c.Params("id"), err.Error())
			return redirectWithError(c, "/admin", err.Error())
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/admins", "Geçersiz ID.")
		}
		// Clear TOTP so next login triggers re-setup.
		if err := deps.AdminRepo.DisableTOTP(c.Context(), id); err != nil {
			message := "2FA sıfırlanamadı: " + err.Error()
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.reset_totp", "failed", "admin", id.String(), message)
			return redirectWithError(c, "/admin/admins", message)
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.reset_totp", "success", "admin", id.String(), "Admin 2FA sıfırlandı.")
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
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.totp_setup", "success", "admin", admin.ID.String(), "Admin 2FA kurulumu başlatıldı.")
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
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.totp_disable", "success", "admin", admin.ID.String(), "Admin 2FA devre dışı bırakıldı.")
		return redirectWithSuccess(c, "/admin/security", "2FA başarıyla devre dışı bırakıldı.")
	}
}

func HandleAdminOutboundPolicyUpdate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "outbound_policy.update", "failed", "outbound_policy", "global", err.Error())
			return redirectWithError(c, "/admin/security", err.Error())
		}
		if deps.OutboundPolicyRepo == nil {
			return redirectWithError(c, "/admin/security", "Outbound policy deposu hazır değil.")
		}
		windowHours, err := strconv.ParseInt(strings.TrimSpace(c.FormValue("velocity_window_hours")), 10, 64)
		if err != nil || windowHours <= 0 {
			windowHours = 24
		}
		setting, err := deps.OutboundPolicyRepo.Upsert(c.Context(), repositories.OutboundPolicyUpdate{
			WhitelistRequired:  c.FormValue("whitelist_required") != "",
			EmergencyFrozen:    c.FormValue("emergency_frozen") != "",
			MaxAmountRaw:       strings.TrimSpace(c.FormValue("max_amount_raw")),
			VelocityLimitRaw:   strings.TrimSpace(c.FormValue("velocity_limit_raw")),
			VelocityWindowSecs: windowHours * 3600,
			ActorEmail:         adminEmail,
		})
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "outbound_policy.update", "failed", "outbound_policy", "global", err.Error())
			return redirectWithError(c, "/admin/security", "Policy güncellenemedi: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "outbound_policy.update", "success", "outbound_policy", setting.ID.String(), "Global outbound policy güncellendi.")
		return redirectWithSuccess(c, "/admin/security", "Outbound policy güncellendi.")
	}
}

// HandleAdminNetworkOperationalStateUpdate changes whether a network accepts
// deposits, withdrawals, both, or neither. The database is the sole source of
// truth so the change takes effect for every gateway instance.
func HandleAdminNetworkOperationalStateUpdate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}

		chainIDRaw := strings.TrimSpace(c.FormValue("chain_id"))
		fail := func(message string) error {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "network_operational_state.update", "failed", "network", chainIDRaw, message)
			return redirectWithError(c, "/admin/networks", message)
		}

		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			return fail(err.Error())
		}
		if deps.NetworkOperationalStateRepo == nil {
			return fail("Network operasyon durum deposu hazır değil.")
		}

		parsedChainID, err := strconv.ParseInt(chainIDRaw, 10, 64)
		if err != nil {
			return fail("Geçersiz network ID.")
		}
		chainID := constants.ChainID(parsedChainID)
		if !constants.IsSupportedChainID(chainID) {
			return fail("Desteklenmeyen network ID.")
		}

		mode := models.NormalizeNetworkOperationalMode(models.NetworkOperationalMode(c.FormValue("mode")))
		if !models.IsValidNetworkOperationalMode(mode) {
			return fail("Geçersiz network operasyon modu.")
		}
		reason := strings.TrimSpace(c.FormValue("reason"))
		if len([]rune(reason)) > 500 {
			return fail("Bakım açıklaması en fazla 500 karakter olabilir.")
		}
		if mode != models.NetworkOperationalModeActive && reason == "" {
			return fail("Network akışını kapatırken açıklama zorunludur.")
		}
		candidate := models.NetworkOperationalState{
			ChainID:   chainID,
			Mode:      mode,
			Reason:    reason,
			UpdatedBy: adminEmail,
		}
		if err := candidate.Validate(); err != nil {
			return fail("Network operasyon durumu doğrulanamadı: " + err.Error())
		}

		previous, err := deps.NetworkOperationalStateRepo.GetByChain(c.Context(), chainID)
		if err != nil {
			return fail("Mevcut network durumu okunamadı: " + err.Error())
		}
		updated, err := deps.NetworkOperationalStateRepo.Upsert(c.Context(), repositories.NetworkOperationalStateUpdate{
			ChainID:   chainID,
			Mode:      mode,
			Reason:    reason,
			UpdatedBy: adminEmail,
		})
		if err != nil {
			return fail("Network operasyon durumu güncellenemedi: " + err.Error())
		}

		beforeMode := string(models.NetworkOperationalModeActive)
		if previous != nil {
			beforeMode = string(previous.Mode)
		}
		afterMode := string(mode)
		if updated != nil {
			afterMode = string(updated.Mode)
		}
		description := fmt.Sprintf("%s ağı %s moduna alındı.", chainLabel(chainID), afterMode)
		if reason != "" {
			description += " Açıklama: " + reason
		}
		logDealerDecisionActivity(c, deps.ActivityLogRepo, nil, nil, "admin", adminEmail, "network_operational_state.update", "success", "network", chainIDRaw, description, beforeMode, afterMode)
		return redirectWithSuccess(c, "/admin/networks", chainLabel(chainID)+" operasyon modu güncellendi.")
	}
}

func HandleAdminOutboundWhitelistCreate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "outbound_whitelist.create", "failed", "outbound_whitelist", "", err.Error())
			return redirectWithError(c, "/admin/security", err.Error())
		}
		if deps.OutboundPolicyRepo == nil {
			return redirectWithError(c, "/admin/security", "Outbound policy deposu hazır değil.")
		}
		token := strings.TrimSpace(c.FormValue("token"))
		var tokenPtr *string
		if token != "" {
			tokenPtr = &token
		}
		entry, err := deps.OutboundPolicyRepo.AddWhitelist(c.Context(), repositories.OutboundWhitelistCreate{
			Scope: repositories.OutboundPolicyScope{
				Chain: strings.TrimSpace(c.FormValue("chain")),
				Token: tokenPtr,
			},
			Address:    strings.TrimSpace(c.FormValue("address")),
			Label:      strings.TrimSpace(c.FormValue("label")),
			ActorEmail: adminEmail,
		})
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "outbound_whitelist.create", "failed", "outbound_whitelist", "", err.Error())
			return redirectWithError(c, "/admin/security", "Whitelist kaydı eklenemedi: "+err.Error())
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "outbound_whitelist.create", "success", "outbound_whitelist", entry.ID.String(), "Whitelist adresi eklendi.")
		return redirectWithSuccess(c, "/admin/security", "Whitelist adresi eklendi.")
	}
}

func HandleAdminOutboundWhitelistToggle(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "outbound_whitelist.toggle", "failed", "outbound_whitelist", c.Params("id"), err.Error())
			return redirectWithError(c, "/admin/security", err.Error())
		}
		if deps.OutboundPolicyRepo == nil {
			return redirectWithError(c, "/admin/security", "Outbound policy deposu hazır değil.")
		}
		id, err := uuid.Parse(strings.TrimSpace(c.Params("id")))
		if err != nil {
			return redirectWithError(c, "/admin/security", "Geçersiz whitelist kaydı.")
		}
		active := c.FormValue("active") == "true"
		if err := deps.OutboundPolicyRepo.SetWhitelistActive(c.Context(), id, active, adminEmail); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "outbound_whitelist.toggle", "failed", "outbound_whitelist", id.String(), err.Error())
			return redirectWithError(c, "/admin/security", "Whitelist güncellenemedi.")
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "outbound_whitelist.toggle", "success", "outbound_whitelist", id.String(), "Whitelist durumu güncellendi.")
		return redirectWithSuccess(c, "/admin/security", "Whitelist durumu güncellendi.")
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
			Role:      models.EffectiveAdminRole(a.Role),
			IsActive:  a.IsActive,
			CreatedAt: formatPanelTime(a.CreatedAt),
		})
	}
	return views
}

func dealerOutboundPolicyView(setting *models.OutboundPolicySetting) DealerOutboundPolicyView {
	if setting == nil {
		return DealerOutboundPolicyView{
			VelocityWindowHours:  24,
			VelocityWindowLabel:  "24 saat",
			ConfigurationSummary: "Default-off",
		}
	}
	hours := setting.VelocityWindowSecs / 3600
	if hours <= 0 {
		hours = 24
	}
	summaryParts := make([]string, 0, 4)
	if setting.EmergencyFrozen {
		summaryParts = append(summaryParts, "freeze aktif")
	}
	if setting.WhitelistRequired {
		summaryParts = append(summaryParts, "whitelist zorunlu")
	}
	if strings.TrimSpace(setting.MaxAmountRaw) != "" {
		summaryParts = append(summaryParts, "max "+setting.MaxAmountRaw)
	}
	if strings.TrimSpace(setting.VelocityLimitRaw) != "" {
		summaryParts = append(summaryParts, "velocity "+setting.VelocityLimitRaw)
	}
	if len(summaryParts) == 0 {
		summaryParts = append(summaryParts, "Default-off")
	}
	return DealerOutboundPolicyView{
		ID:                   setting.ID.String(),
		WhitelistRequired:    setting.WhitelistRequired,
		EmergencyFrozen:      setting.EmergencyFrozen,
		MaxAmountRaw:         setting.MaxAmountRaw,
		VelocityLimitRaw:     setting.VelocityLimitRaw,
		VelocityWindowHours:  hours,
		VelocityWindowLabel:  fmt.Sprintf("%d saat", hours),
		UpdatedBy:            setting.UpdatedBy,
		UpdatedAt:            formatPanelTime(setting.UpdatedAt),
		ConfigurationSummary: strings.Join(summaryParts, " · "),
	}
}

func dealerOutboundWhitelistViews(rows []models.OutboundAddressWhitelist) []DealerOutboundWhitelistView {
	views := make([]DealerOutboundWhitelistView, 0, len(rows))
	for _, row := range rows {
		scope := "global"
		if row.DomainID != nil {
			scope = "domain:" + row.DomainID.String()
		} else if row.MerchantID != nil {
			scope = "merchant:" + row.MerchantID.String()
		}
		token := "native"
		if row.Token != nil && strings.TrimSpace(*row.Token) != "" {
			token = *row.Token
		}
		views = append(views, DealerOutboundWhitelistView{
			ID:        row.ID.String(),
			Scope:     scope,
			Chain:     emptyDefault(row.Chain, "all"),
			Token:     token,
			Address:   row.Address,
			Label:     row.Label,
			IsActive:  row.IsActive,
			UpdatedBy: row.UpdatedBy,
			UpdatedAt: formatPanelTime(row.UpdatedAt),
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

		page, limit := adminDashboardPageParams(c)

		var currentAdmin *models.Admin
		if deps.AdminRepo != nil {
			if admin, err := deps.AdminRepo.FindByEmail(c.Context(), adminEmail); err == nil {
				currentAdmin = admin
				data.AdminRole = models.EffectiveAdminRole(admin.Role)
			}
		}

		adminHeaderStatsFor(c.Context(), deps).applyTo(&data)

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

		case "vault":
			rows, err := deps.LedgerRepo.PlatformBalances(c.Context())
			if err != nil {
				return renderAdminDashboardError(c, data, "Vault bakiyeleri okunamadı", err)
			}
			vaultViews := dealerVaultBalanceViews(rows, deps.AssetRegistry)
			data.AdminVaults = paginateViewSlice(vaultViews, page, limit)
			data.AdminPagination = dealerPaginationView(page, limit, int64(len(vaultViews)), "/admin/vault")

		case "assets":
			assetViews := dealerAssetOptions(deps.AssetRegistry)
			data.AdminAssets = paginateViewSlice(assetViews, page, limit)
			data.AdminPagination = dealerPaginationView(page, limit, int64(len(assetViews)), "/admin/assets")

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
			data.AdminDeposits = dealerActivityViews(rows, deps.AssetRegistry, deps.Blockchains)
			depositBase := buildDepositFilterURL(fromFilter, toFilter, hashFilter)
			data.AdminPagination = dealerPaginationView(page, limit, total, depositBase)

		case "withdrawals":
			statusFilter := normalizeAdminWithdrawalStatusFilter(c.Query("status"))
			data.AdminWithdrawalStatusFilter = statusFilter
			rows, total, err := deps.WithdrawalRepo.ListPage(c.Context(), page, limit, statusFilter)
			if err != nil {
				return renderAdminDashboardError(c, data, "Çekim listesi okunamadı", err)
			}
			data.Withdrawals = dealerWithdrawalViews(rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, buildAdminWithdrawalPaginationBase(statusFilter))

		case "wallets":
			rows, total, err := deps.WalletRepo.ListPage(c.Context(), page, limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Wallet listesi okunamadı", err)
			}
			balanceMap := buildWalletBalanceMap(c.Context(), deps.LedgerRepo, rows, deps.AssetRegistry)
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
			data.AdminPagination = dealerPaginationView(page, limit, total, buildAdminActivityPaginationBase(merchantFilter))

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

		case "reconciliation":
			statusFilter := strings.TrimSpace(c.Query("status"))
			data.AdminReconciliationStatusFilter = statusFilter
			rows, total, err := deps.ReconciliationRepo.ListPage(c.Context(), page, limit, statusFilter)
			if err != nil {
				return renderAdminDashboardError(c, data, "Reconciliation job listesi okunamadı", err)
			}
			data.AdminReconciliationJobs = dealerReconciliationJobViews(rows)
			reconciliationBase := "/admin/reconciliation"
			if statusFilter != "" {
				reconciliationBase += "?status=" + statusFilter
			}
			data.AdminPagination = dealerPaginationView(page, limit, total, reconciliationBase)

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

		case "readiness":
			data.AdminReadinessReady, data.AdminReadinessCheckedAt, data.AdminReadiness, data.AdminReadinessRaw = dealerAdminReadinessView(c.Context(), deps)
			data.AdminPagination = dealerPaginationView(1, limit, int64(len(data.AdminReadinessRaw)), "/admin/readiness")

		case "metrics":
			data.AdminMetricsSummary, data.AdminMetricsGroups, data.AdminMetricAlerts, data.AdminMetricTabs, data.AdminMetricsActiveTab, data.AdminMetricsRaw = dealerAdminMetricsView(c.Context(), deps, strings.TrimSpace(c.Query("tab")))
			data.AdminPagination = dealerPaginationView(1, limit, int64(data.AdminMetricsSummary.TotalSeries), "/admin/metrics")

		case "provider-health":
			rows, err := deps.ProviderHealthRepo.ListLatest(c.Context())
			if err != nil {
				return renderAdminDashboardError(c, data, "Provider health snapshotları okunamadı", err)
			}
			data.AdminProviderHealth = dealerProviderHealthViews(rows)
			data.AdminPagination = dealerPaginationView(1, limit, int64(len(data.AdminProviderHealth)), "/admin/provider-health")

		case "networks":
			if deps.NetworkOperationalStateRepo == nil {
				return renderAdminDashboardError(c, data, "Network operasyon durum deposu hazır değil", errors.New("network operational state repository is nil"))
			}
			rows, err := deps.NetworkOperationalStateRepo.ListAll(c.Context())
			if err != nil {
				return renderAdminDashboardError(c, data, "Network operasyon durumları okunamadı", err)
			}
			data.AdminNetworkStates = dealerNetworkOperationalStateViews(rows)

		case "sweep":
			if deps.SweepJobRepo != nil {
				statusFilter := strings.TrimSpace(c.Query("status"))
				data.AdminSweepStatusFilter = statusFilter
				counts, err := deps.SweepJobRepo.CountByStatus(c.Context(),
					models.SweepJobStatusPending,
					models.SweepJobStatusProcessing,
					models.SweepJobStatusFailed,
					models.SweepJobStatusDeadLetter,
					models.SweepJobStatusSucceeded,
				)
				if err != nil {
					return renderAdminDashboardError(c, data, "Sweep job durumları okunamadı", err)
				}
				data.AdminSweepStats = dealerSweepStatusViews(counts)
				eligibleCount, err := deps.SweepJobRepo.CountMissingFinalizedTransactions(c.Context())
				if err != nil {
					return renderAdminDashboardError(c, data, "Sweep adayları okunamadı", err)
				}
				data.AdminSweepEligibleCount = eligibleCount
				rows, total, err := deps.SweepJobRepo.ListPage(c.Context(), page, limit, statusFilter)
				if err != nil {
					return renderAdminDashboardError(c, data, "Sweep job listesi okunamadı", err)
				}
				data.AdminSweepJobs = dealerSweepJobViews(rows)
				sweepBase := "/admin/sweep"
				if statusFilter != "" {
					sweepBase += "?status=" + url.QueryEscape(statusFilter)
				}
				data.AdminPagination = dealerPaginationView(page, limit, total, sweepBase)
			}

		case "recover":
			formWallets, err := deps.WalletRepo.List(c.Context(), 500)
			if err != nil {
				return renderAdminDashboardError(c, data, "Recover funds wallet listesi okunamadı", err)
			}
			data.AdminWallets = dealerWalletViews(formWallets)
			data.AdminAssets = dealerAssetOptions(deps.AssetRegistry)
			data.AdminRecoverChains = dealerRecoverChainOptions(deps.AssetRegistry)
			recoverChainValue := dealerRecoverChainFilter(deps.AssetRegistry, firstNonEmpty(c.Params("chain_id"), c.Query("chain")))
			data.AdminRecoverChainFilter = recoverChainValue
			recoverAssetValue := adminRecoverAssetValueFromRequest(deps.AssetRegistry, c)
			data.AdminRecoverAssetFilter = recoverAssetValue
			if recoverAssetValue == "" {
				data.AdminPagination = dealerPaginationView(1, limit, 0, "/admin/recover")
				break
			}
			selectedAsset, err := parseAdminAssetSelection(deps.AssetRegistry, recoverAssetValue)
			if err != nil {
				data.AdminRecoverChainFilter = recoverChainValue
				data.AdminRecoverAssetFilter = ""
				data.AdminPagination = dealerPaginationView(1, limit, 0, "/admin/recover")
				data.Error = err.Error()
				break
			}
			data.AdminRecoverChainFilter = fmt.Sprintf("%d", selectedAsset.GetChainID())
			walletIDs, walletTotal, err := deps.LedgerRepo.WalletIDsWithPositiveAvailableBalance(c.Context(), selectedAsset.GetChainID(), tokenForSelectedAsset(selectedAsset), page, limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Recover bakiye listesi okunamadı", err)
			}
			wallets, err := deps.WalletRepo.ListByIDs(c.Context(), walletIDs)
			if err != nil {
				return renderAdminDashboardError(c, data, "Recover wallet listesi okunamadı", err)
			}
			balanceMap := buildWalletBalanceMap(c.Context(), deps.LedgerRepo, wallets, deps.AssetRegistry)
			data.WithdrawalWallets = filterDealerWalletViewsToAsset(dealerWalletViewsWithBalances(wallets, balanceMap), selectedAsset)
			data.AdminPagination = dealerPaginationView(page, limit, walletTotal, adminRecoverPaginationBase(recoverAssetValue))

		case "tests":
			data.AdminTestDomains = dealerAdminTestDomainOptions(c.Context(), deps, 200)

		case "test-deposit":
			wallets, err := deps.WalletRepo.List(c.Context(), 500)
			if err != nil {
				return renderAdminDashboardError(c, data, "Test deposit wallet listesi okunamadı", err)
			}
			data.AdminWallets = dealerWalletViews(wallets)
			data.AdminAssets = dealerAssetOptions(deps.AssetRegistry)
			if deps.PaymentRepo != nil {
				payments, err := deps.PaymentRepo.ListTestableCheckoutSessions(c.Context(), 25)
				if err != nil {
					return renderAdminDashboardError(c, data, "Test checkout listesi okunamadı", err)
				}
				data.AdminTestPayments = dealerTestPaymentViews(c, payments)
			}

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
			if currentAdmin == nil {
				admin, err := deps.AdminRepo.FindByEmail(c.Context(), adminEmail)
				if err != nil {
					return renderAdminDashboardError(c, data, "Admin güvenlik ayarları okunamadı", err)
				}
				currentAdmin = admin
				data.AdminRole = models.EffectiveAdminRole(admin.Role)
			}
			data.AdminTOTPEnabled = currentAdmin.TOTPEnabled
			data.AdminOutboundPolicy = dealerOutboundPolicyView(nil)
			if deps.OutboundPolicyRepo != nil {
				if setting, err := deps.OutboundPolicyRepo.GetGlobal(c.Context()); err == nil {
					data.AdminOutboundPolicy = dealerOutboundPolicyView(setting)
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return renderAdminDashboardError(c, data, "Outbound policy ayarları okunamadı", err)
				}
				rows, total, err := deps.OutboundPolicyRepo.ListWhitelistPage(c.Context(), page, limit)
				if err != nil {
					return renderAdminDashboardError(c, data, "Whitelist listesi okunamadı", err)
				}
				data.AdminOutboundWhitelist = dealerOutboundWhitelistViews(rows)
				data.AdminPagination = dealerPaginationView(page, limit, total, "/admin/security")
			}

		default: // overview
			recentRows, total, err := deps.TransactionRepo.ListPage(c.Context(), page, limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Son depositler okunamadı", err)
			}
			data.AdminDeposits = dealerActivityViews(recentRows, deps.AssetRegistry, deps.Blockchains)
			data.AdminPagination = dealerPaginationView(page, limit, total, "/admin")
		}

		return c.Render("dealer/admin_dashboard", data, "dealer/layout")
	}
}

func HandleAdminMerchantDetail(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}

		merchantID, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/merchants", "Geçersiz merchant ID.")
		}
		merchant, err := deps.MerchantService.Repo().FindAnyByID(c.Context(), merchantID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return redirectWithError(c, "/admin/merchants", "Merchant bulunamadı.")
			}
			return redirectWithError(c, "/admin/merchants", "Merchant okunamadı: "+err.Error())
		}

		data := adminPageData(adminEmail, "merchant-detail")
		applyFlash(c, &data)
		if deps.AdminRepo != nil {
			if admin, err := deps.AdminRepo.FindByEmail(c.Context(), adminEmail); err == nil {
				data.AdminRole = models.EffectiveAdminRole(admin.Role)
			}
		}
		adminHeaderStatsFor(c.Context(), deps).applyTo(&data)

		merchantViews := dealerAdminMerchantViews([]models.Merchant{*merchant})
		if len(merchantViews) > 0 {
			data.AdminMerchantDetail = merchantViews[0]
		}
		data.AdminMerchantDetailURL = "/admin/merchants/" + merchantID.String()
		data.AdminMerchantDetailTab = adminMerchantDetailTab(c)

		page, limit := adminDashboardPageParams(c)
		domains, err := deps.DomainService.ListByMerchant(c.Context(), merchantID)
		if err != nil {
			return renderAdminDashboardError(c, data, "Merchant domain listesi okunamadı", err)
		}
		data.AdminMerchantDomainCount = int64(len(domains))
		_, walletTotal, err := deps.WalletRepo.ListByMerchantPage(c.Context(), merchantID, 1, 0)
		if err != nil {
			return renderAdminDashboardError(c, data, "Merchant wallet sayısı okunamadı", err)
		}
		data.AdminMerchantWalletCount = walletTotal
		_, paymentTotal, err := deps.PaymentRepo.ListByMerchantPage(c.Context(), merchantID, "", 1, 1)
		if err != nil {
			return renderAdminDashboardError(c, data, "Merchant payment sayısı okunamadı", err)
		}
		data.AdminMerchantPaymentCount = paymentTotal

		switch data.AdminMerchantDetailTab {
		case "wallets":
			rows, total, err := deps.WalletRepo.ListByMerchantPage(c.Context(), merchantID, limit, (page-1)*limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Merchant wallet listesi okunamadı", err)
			}
			balanceMap := buildWalletBalanceMap(c.Context(), deps.LedgerRepo, rows, deps.AssetRegistry)
			data.AdminWallets = dealerWalletViewsWithBalances(rows, balanceMap)
			data.AdminPagination = dealerPaginationView(page, limit, total, adminMerchantDetailPaginationBase(merchantID, "wallets", ""))

		case "payments":
			statusFilter := normalizeAdminPaymentStatusFilter(c.Query("status"))
			data.AdminMerchantPaymentStatus = statusFilter
			rows, total, err := deps.PaymentRepo.ListByMerchantPage(c.Context(), merchantID, statusFilter, page, limit)
			if err != nil {
				return renderAdminDashboardError(c, data, "Merchant payment listesi okunamadı", err)
			}
			data.Payments = dealerPaymentViews(c, rows)
			data.AdminPagination = dealerPaginationView(page, limit, total, adminMerchantDetailPaginationBase(merchantID, "payments", statusFilter))

		default:
			data.Domains = paginateViewSlice(dealerDomainViews(domains), page, limit)
			data.AdminPagination = dealerPaginationView(page, limit, int64(len(domains)), adminMerchantDetailPaginationBase(merchantID, "domains", ""))
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
		if deps.WebhookDeliveryRepo == nil {
			return redirectWithError(c, "/admin/webhooks", "Webhook replay servisi hazır değil.")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "failed", "webhook_delivery", c.Params("id"), "Geçersiz replay isteği.")
			return redirectWithError(c, "/admin/webhooks", "Geçersiz webhook delivery.")
		}
		confirmReplay := strings.TrimSpace(firstNonEmpty(c.FormValue("confirm_replay"), c.Get("X-Gateway-Replay-Confirm")))
		if confirmReplay != "replay:"+id.String() {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "failed", "webhook_delivery", id.String(), "Replay confirmation missing.")
			return redirectWithError(c, "/admin/webhooks", "Webhook replay için confirmation gerekli.")
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

		// Delivery must only be performed by the lease-owning retry worker. Sending
		// here immediately after enqueue races ClaimDue and can publish the same
		// replay twice (especially for HTTP webhooks, which have no broker dedupe).
		message := "Webhook yeniden gönderim kuyruğuna alındı."
		switch {
		case delivery.PaymentID != nil:
			message = "Payment webhook yeniden gönderim kuyruğuna alındı."
		case delivery.TransactionID != nil:
			message = "Transaction webhook yeniden gönderim kuyruğuna alındı."
		case strings.TrimSpace(delivery.PayloadJSON) != "":
			message = "Lifecycle webhook yeniden gönderim kuyruğuna alındı."
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "webhook.replay", "success", "webhook_delivery", id.String(), message)
		return redirectWithSuccess(c, "/admin/webhooks", message)
	}
}

func HandleAdminMoneyEventOutboxRetry(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if deps.MoneyEventOutboxRepo == nil {
			return redirectWithError(c, "/admin/webhooks", "Money event outbox servisi hazır değil.")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "money_event_outbox.retry", "failed", "money_event_outbox", c.Params("id"), "Geçersiz outbox retry isteği.")
			return redirectWithError(c, "/admin/webhooks", "Geçersiz money event outbox kaydı.")
		}
		confirmRetry := strings.TrimSpace(firstNonEmpty(c.FormValue("confirm_retry"), c.Get("X-Gateway-Retry-Confirm")))
		if confirmRetry != "retry:"+id.String() {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "money_event_outbox.retry", "failed", "money_event_outbox", id.String(), "Retry confirmation missing.")
			return redirectWithError(c, "/admin/webhooks", "Money event retry için confirmation gerekli.")
		}
		requeued, err := deps.MoneyEventOutboxRepo.RequeueRelay(c.Context(), id)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "money_event_outbox.retry", "failed", "money_event_outbox", id.String(), "Outbox retry başarısız.")
			return redirectWithError(c, "/admin/webhooks", "Money event outbox retry başarısız.")
		}
		if !requeued {
			return redirectWithError(c, "/admin/webhooks", "Kayıt retry edilebilir failed/dead-letter durumda değil.")
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "money_event_outbox.retry", "success", "money_event_outbox", id.String(), "Outbox kaydı yeniden kuyruğa alındı.")
		return redirectWithSuccess(c, "/admin/webhooks", "Money event yeniden kuyruğa alındı.")
	}
}

func HandleAdminSweepLiveBalance(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, ok := requireAdmin(c); !ok {
			return adminLiveBalanceError(c, fiber.StatusUnauthorized, "Admin girişi gerekli.")
		}
		if deps.WalletRepo == nil || deps.Blockchains == nil || deps.AssetRegistry == nil {
			return adminLiveBalanceError(c, fiber.StatusServiceUnavailable, "Canlı bakiye altyapısı hazır değil.")
		}

		walletID, err := uuid.Parse(strings.TrimSpace(c.Query("wallet_id")))
		if err != nil {
			return adminLiveBalanceError(c, fiber.StatusBadRequest, "Geçerli wallet seçmelisin.")
		}
		selectedAsset, err := parseAdminAssetSelection(deps.AssetRegistry, c.Query("asset"))
		if err != nil {
			return adminLiveBalanceError(c, fiber.StatusBadRequest, err.Error())
		}

		wallet, err := deps.WalletRepo.FindByID(c.Context(), walletID)
		if err != nil {
			return adminLiveBalanceError(c, fiber.StatusNotFound, "Wallet bulunamadı.")
		}
		chainID := selectedAsset.GetChainID()
		address := strings.TrimSpace(repositories.WalletAddressForChainID(*wallet, chainID))
		if address == "" {
			return adminLiveBalanceError(c, fiber.StatusBadRequest, "Seçili wallet için "+constants.ChainName(chainID)+" adresi yok.")
		}

		chain, err := deps.Blockchains.GetChainByID(chainID)
		if err != nil {
			return adminLiveBalanceError(c, fiber.StatusBadRequest, "Chain hazır değil: "+err.Error())
		}
		if !chain.ValidateAddress(address) {
			return adminLiveBalanceError(c, fiber.StatusBadRequest, "Wallet adresi seçili chain için geçersiz.")
		}

		ctx, cancel := context.WithTimeout(c.Context(), 15*time.Second)
		defer cancel()

		raw, err := adminLiveBalanceRawForAsset(ctx, chain, address, selectedAsset)
		if err != nil {
			return adminLiveBalanceError(c, fiber.StatusBadGateway, err.Error())
		}
		networkFeeRaw := "0"
		transferableRaw := raw
		networkFeeError := ""
		feeCtx, feeCancel := context.WithTimeout(ctx, 4*time.Second)
		defer feeCancel()
		if fee, err := adminRecoverNativeFeeRaw(feeCtx, chain, selectedAsset); err != nil {
			networkFeeError = err.Error()
			if selectedAsset.IsNative() {
				transferableRaw = "0"
			}
		} else if fee != nil && fee.Sign() > 0 {
			networkFeeRaw = fee.String()
			value, ok := new(big.Int).SetString(raw, 10)
			if ok {
				net := new(big.Int).Sub(value, fee)
				if net.Sign() > 0 {
					transferableRaw = net.String()
				} else {
					transferableRaw = "0"
				}
			} else {
				transferableRaw = "0"
			}
		}

		return c.JSON(fiber.Map{
			"result":               "success",
			"wallet_id":            walletID.String(),
			"address":              address,
			"explorer_url":         addressExplorerURL(deps.Blockchains, chainID, address),
			"chain":                constants.ChainName(chainID),
			"chain_id":             int64(chainID),
			"symbol":               selectedAsset.GetSymbol(),
			"decimals":             selectedAsset.GetDecimals(),
			"balance":              formatTokenAmount(raw, selectedAsset.GetDecimals()),
			"balance_raw":          raw,
			"network_fee":          formatTokenAmount(networkFeeRaw, selectedAsset.GetDecimals()),
			"network_fee_raw":      networkFeeRaw,
			"network_fee_error":    networkFeeError,
			"transferable":         formatTokenAmount(transferableRaw, selectedAsset.GetDecimals()),
			"transferable_raw":     transferableRaw,
			"fee_deducted_native":  selectedAsset.IsNative() && networkFeeRaw != "0",
			"transferable_is_zero": transferableRaw == "0",
		})
	}
}

func HandleAdminSweepEnqueue(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.sweep_enqueue", "failed", "sweep_job", "", err.Error())
			return redirectWithError(c, "/admin/sweep", err.Error())
		}
		if deps.SweepJobRepo == nil {
			return redirectWithError(c, "/admin/sweep", "Sweep job altyapısı hazır değil.")
		}
		limit := parseQueryInt(c.FormValue("limit"), 1000)
		if limit < 1 || limit > 1000 {
			limit = 1000
		}
		created, err := deps.SweepJobRepo.EnqueueMissingFinalizedTransactions(c.Context(), limit)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.sweep_enqueue", "failed", "sweep_job", "", err.Error())
			return redirectWithError(c, "/admin/sweep", "Sweep job oluşturulamadı: "+err.Error())
		}
		message := fmt.Sprintf("%d sweep job kuyruğa alındı.", len(created))
		if len(created) == 0 {
			message = "Yeni sweep job adayı yok; kuyruk güncel."
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.sweep_enqueue", "success", "sweep_job", "", message)
		return redirectWithSuccess(c, "/admin/sweep", message)
	}
}

func HandleAdminRecoverFunds(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.recover_funds", "failed", "wallet", "", err.Error())
			return redirectWithError(c, "/admin", err.Error())
		}
		if deps.WalletRepo == nil || deps.Blockchains == nil || deps.WithdrawalRepo == nil || deps.LedgerRepo == nil || deps.OutboundRepo == nil {
			return redirectWithError(c, "/admin/recover", "Recover funds altyapısı hazır değil.")
		}
		recoverAssetValue := strings.TrimSpace(c.FormValue("asset"))
		recoverURL := adminRecoverPaginationBase(recoverAssetValue)
		walletID := strings.TrimSpace(c.FormValue("wallet_id"))
		selectedAsset, err := parseAdminAssetSelection(deps.AssetRegistry, recoverAssetValue)
		if err != nil {
			return redirectWithError(c, "/admin/recover", err.Error())
		}
		selectedChainID := selectedAsset.GetChainID()
		chain := constants.ChainName(selectedChainID)
		if chain == "" {
			return redirectWithError(c, recoverURL, fmt.Sprintf("unsupported chain: %d", selectedAsset.GetChainID()))
		}
		chainObj, err := deps.Blockchains.GetChainByID(selectedChainID)
		if err != nil {
			return redirectWithError(c, recoverURL, "Chain hazır değil: "+err.Error())
		}
		token := tokenForSelectedAsset(selectedAsset)
		assetLabel := strings.TrimSpace(selectedAsset.GetSymbol())
		if assetLabel == "" {
			assetLabel = chain
		}
		decimals := selectedAsset.GetDecimals()

		sourceWalletID, err := uuid.Parse(walletID)
		if err != nil {
			return redirectWithError(c, recoverURL, "Geçerli source wallet seçmelisin.")
		}
		sourceWallet, err := deps.WalletRepo.FindByID(c.Context(), sourceWalletID)
		if err != nil {
			return redirectWithError(c, recoverURL, "Source wallet bulunamadı: "+err.Error())
		}
		hasPositiveBalance, err := recoverWalletHasRecoverableAssetBalance(c.Context(), deps.LedgerRepo, *sourceWallet, selectedAsset)
		if err != nil {
			return redirectWithError(c, recoverURL, "Source wallet bakiyesi okunamadı: "+err.Error())
		}
		if !hasPositiveBalance {
			return redirectWithError(c, recoverURL, "Source wallet seçili asset için 0'dan büyük kullanılabilir veya sweep-locked bakiyeye sahip değil.")
		}
		toAddress := strings.TrimSpace(c.FormValue("to_address"))
		destinationWalletID := strings.TrimSpace(c.FormValue("destination_wallet_id"))
		destinationLabel := strings.TrimSpace(toAddress)
		if destinationWalletID != "" {
			destinationID, err := uuid.Parse(destinationWalletID)
			if err != nil {
				return redirectWithError(c, recoverURL, "Geçerli hedef wallet seçmelisin.")
			}
			if destinationID == sourceWalletID {
				return redirectWithError(c, recoverURL, "Source ve hedef wallet aynı olamaz.")
			}
			destinationWallet, err := deps.WalletRepo.FindByID(c.Context(), destinationID)
			if err != nil {
				return redirectWithError(c, recoverURL, "Hedef wallet bulunamadı: "+err.Error())
			}
			toAddress = strings.TrimSpace(repositories.WalletAddressForChainID(*destinationWallet, selectedChainID))
			if toAddress == "" {
				return redirectWithError(c, recoverURL, "Hedef wallet için "+constants.ChainName(selectedChainID)+" adresi yok.")
			}
			destinationLabel = destinationWallet.ID.String()
		}
		amountRaw := strings.TrimSpace(c.FormValue("amount_raw"))
		amountMissing := amountRaw == "" || amountRaw == "0"
		if amountMissing {
			msg := "manual sweep requires an explicit amount for ledger reservation"
			if deps.ActivityLogRepo != nil {
				logDealerActivity(c, deps.ActivityLogRepo, &sourceWallet.MerchantID, "admin", adminEmail, "admin.recover_funds", "failed", "wallet", walletID, msg)
			}
			return redirectWithError(c, recoverURL, "Recover funds için amount_raw zorunlu; "+msg+".")
		}
		grossAmountRaw := amountRaw
		feeCtx, feeCancel := context.WithTimeout(c.Context(), 15*time.Second)
		netAmountRaw, networkFeeRaw, err := adminRecoverNetAmountRaw(feeCtx, chainObj, selectedAsset, amountRaw)
		feeCancel()
		if err != nil {
			return redirectWithError(c, recoverURL, err.Error())
		}
		amountRaw = netAmountRaw

		if walletID == "" || chain == "" || toAddress == "" {
			return redirectWithError(c, recoverURL, "Source wallet, asset ve hedef wallet/adres zorunlu.")
		}

		params := types.TransferParams{
			Context:   c.Context(),
			WalletID:  &walletID,
			Chain:     &chain,
			Token:     token,
			ToAddress: &toAddress,
		}
		params.AmountRaw = &amountRaw

		if err := params.ValidateWithdraw(); err != nil {
			return redirectWithError(c, recoverURL, err.Error())
		}
		if err := requireOutboundMakerChecker(adminEmail, adminEmail); err != nil {
			if deps.ActivityLogRepo != nil {
				logDealerActivity(c, deps.ActivityLogRepo, &sourceWallet.MerchantID, "admin", adminEmail, "admin.recover_funds", "failed", "wallet", walletID, err.Error())
			}
			return redirectWithError(c, recoverURL, err.Error())
		}
		domainID := sourceWallet.DomainID
		note := "admin recover funds to " + destinationLabel
		if networkFeeRaw != "0" {
			note += " (gross_raw=" + grossAmountRaw + " network_fee_raw=" + networkFeeRaw + ")"
		}
		request := &models.WithdrawalRequest{
			MerchantID:  sourceWallet.MerchantID,
			DomainID:    &domainID,
			WalletID:    sourceWallet.ID,
			Chain:       *params.Chain,
			Token:       token,
			Symbol:      assetLabel,
			Decimals:    decimals,
			ToAddress:   *params.ToAddress,
			AmountRaw:   *params.AmountRaw,
			Note:        note,
			Status:      models.WithdrawalStatusPending,
			RequestedBy: adminEmail,
			CorrelationID: dealerSignerCorrelationID(c,
				"admin_recover_funds:"+sourceWallet.ID.String()),
		}
		if err := enforceDealerOutboundPolicy(c.Context(), deps, outboundPolicyCheckFromWithdrawal("admin.recover_funds", request, false)); err != nil {
			if deps.ActivityLogRepo != nil {
				logDealerActivity(c, deps.ActivityLogRepo, &sourceWallet.MerchantID, "admin", adminEmail, "admin.recover_funds", "failed", "wallet", walletID, err.Error())
			}
			return redirectWithError(c, recoverURL, err.Error())
		}
		if err := deps.WithdrawalRepo.CreateRecoverWithHold(c.Context(), request, deps.LedgerRepo); err != nil {
			if deps.ActivityLogRepo != nil {
				logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "admin.recover_funds", "failed", "wallet", walletID, err.Error())
			}
			return redirectWithError(c, recoverURL, "Ledger rezervasyonu başarısız: "+err.Error())
		}

		approvedRequest, outboundTx, err := deps.WithdrawalRepo.ApproveForOutbound(c.Context(), request.ID, adminEmail, deps.LedgerRepo, deps.OutboundRepo)
		if err != nil {
			if deps.ActivityLogRepo != nil {
				logDealerActivity(c, deps.ActivityLogRepo, &sourceWallet.MerchantID, "admin", adminEmail, "admin.recover_funds", "failed", "withdrawal", request.ID.String(), err.Error())
			}
			if approvedRequest != nil && approvedRequest.Status == models.WithdrawalStatusProcessing {
				openDealerOutboundLifecycleReconciliation(c.Context(), deps, approvedRequest.Chain, &approvedRequest.MerchantID, approvedRequest.DomainID, "withdrawal", approvedRequest.ID.String(), approvedRequest.Status, "outbound_broadcast_uncertain", approvedRequest.Error, approvedRequest.TxHash)
			}
			if approvedRequest != nil && approvedRequest.Status == models.WithdrawalStatusFailed {
				enqueueDealerPayoutLifecycle(c.Context(), deps, *approvedRequest, constants.WebhookEventPayoutFailedV1)
			}
			return redirectWithError(c, recoverURL, "Transfer başarısız: "+err.Error())
		}
		if deps.ActivityLogRepo != nil {
			resourceID := request.ID.String()
			if outboundTx != nil {
				resourceID = outboundTx.ID.String()
			}
			logDealerActivity(c, deps.ActivityLogRepo, &sourceWallet.MerchantID, "admin", adminEmail, "admin.recover_funds", "success", "outbound_transaction", resourceID, assetLabel+" -> "+destinationLabel+" yayın kuyruğuna alındı.")
		}
		message := "Recover funds transferi yayın kuyruğuna alındı (" + assetLabel + ")."
		if networkFeeRaw != "0" {
			message = "Recover funds transferi yayın kuyruğuna alındı (" + assetLabel + "). Network fee raw düşüldü: " + networkFeeRaw + "."
		}
		return redirectWithSuccess(c, recoverURL, message)
	}
}

func adminLiveBalanceError(c fiber.Ctx, status int, message string) error {
	return c.Status(status).JSON(fiber.Map{
		"result":  "error",
		"message": message,
	})
}

func dealerSignerCorrelationID(c fiber.Ctx, fallback string) string {
	requestID := middleware.RequestIDFromCtx(c)
	fallback = strings.TrimSpace(fallback)
	if requestID == "" {
		return fallback
	}
	if fallback == "" {
		return requestID
	}
	return requestID + ":" + fallback
}

func requireOutboundMakerChecker(requestedBy string, actorEmail string) error {
	if !outboundMakerCheckerRequired() {
		return nil
	}
	requestedBy = strings.TrimSpace(requestedBy)
	actorEmail = strings.TrimSpace(actorEmail)
	if requestedBy == "" || actorEmail == "" {
		return errors.New("maker-checker policy requires requester and approver identity")
	}
	if strings.EqualFold(requestedBy, actorEmail) {
		return errors.New("maker-checker policy rejects self approval")
	}
	return nil
}

func outboundMakerCheckerRequired() bool {
	for _, key := range []string{"OUTBOUND_MAKER_CHECKER_REQUIRED", "REQUIRE_OUTBOUND_MAKER_CHECKER"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func adminBalanceResultForAddress(results []models.BalanceResult, address string) (models.BalanceResult, bool) {
	for _, result := range results {
		if strings.EqualFold(strings.TrimSpace(result.Address), strings.TrimSpace(address)) {
			return result, true
		}
	}
	if len(results) == 1 {
		return results[0], true
	}
	return models.BalanceResult{}, false
}

func adminLiveBalanceRawForAsset(ctx context.Context, chain blockchain.Chain, address string, selected asset.Asset) (string, error) {
	if chain == nil {
		return "", errors.New("chain hazır değil")
	}
	if selected == nil {
		return "", errors.New("asset seçimi geçersiz")
	}
	if selected.IsNative() && selected.GetChainType() == asset.ChainEVM {
		return adminLiveEVMNativeBalanceRaw(ctx, chain, address)
	}
	if !selected.IsNative() && selected.GetChainType() == asset.ChainEVM {
		return adminLiveEVMTokenBalanceRaw(ctx, chain, address, selected)
	}

	results := chain.BatchBalances(ctx, []string{address}, 1)
	result, ok := adminBalanceResultForAddress(results, address)
	if !ok {
		return "", errors.New("Chain bakiye sonucu dönmedi.")
	}
	if result.Error != nil {
		return "", fmt.Errorf("Chain bakiye sorgusu başarısız: %w", result.Error)
	}
	return adminLiveBalanceRaw(result.Balance, selected)
}

func adminLiveEVMNativeBalanceRaw(ctx context.Context, chain blockchain.Chain, address string) (string, error) {
	if !common.IsHexAddress(address) {
		return "", errors.New("wallet adresi seçili chain için geçersiz")
	}
	var lastErr error
	for _, rpcURL := range chain.RPCs() {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}
		client, err := ethclient.DialContext(ctx, rpcURL)
		if err != nil {
			lastErr = err
			continue
		}
		balance, err := client.BalanceAt(ctx, common.HexToAddress(address), nil)
		client.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return ensurePositiveBigInt(balance).String(), nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("Chain bakiye sorgusu başarısız: native balance yanıtı okunamadı: %w", lastErr)
	}
	return "", errors.New("Chain bakiye sorgusu başarısız: RPC endpoint tanımlı değil")
}

func adminLiveEVMTokenBalanceRaw(ctx context.Context, chain blockchain.Chain, address string, selected asset.Asset) (string, error) {
	tokenAddress := strings.TrimSpace(asset.TokenAddress(selected))
	if tokenAddress == "" || !common.IsHexAddress(tokenAddress) {
		return "", fmt.Errorf("seçili EVM token kontratı geçersiz: %s", selected.GetSymbol())
	}
	if !common.IsHexAddress(address) {
		return "", errors.New("wallet adresi seçili chain için geçersiz")
	}

	var lastErr error
	for _, rpcURL := range chain.RPCs() {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}
		client, err := ethclient.DialContext(ctx, rpcURL)
		if err != nil {
			lastErr = err
			continue
		}
		caller, err := erc20.NewERC20Caller(common.HexToAddress(tokenAddress), client)
		if err != nil {
			client.Close()
			lastErr = err
			continue
		}
		balance, err := caller.BalanceOf(&bind.CallOpts{Context: ctx}, common.HexToAddress(address))
		client.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return ensurePositiveBigInt(balance).String(), nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("Chain bakiye sorgusu başarısız: %s balanceOf yanıtı okunamadı", selected.GetSymbol())
	}
	return "", errors.New("Chain bakiye sorgusu başarısız: RPC endpoint tanımlı değil")
}

func ensurePositiveBigInt(value *big.Int) *big.Int {
	if value == nil || value.Sign() < 0 {
		return big.NewInt(0)
	}
	return value
}

func adminLiveBalanceRaw(balance string, selected asset.Asset) (string, error) {
	if selected == nil {
		return "", errors.New("asset seçimi geçersiz")
	}
	components := adminParseBalanceComponents(balance)
	value := ""
	if selected.IsNative() {
		for _, symbol := range adminNativeBalanceSymbols(selected) {
			if candidate, ok := components[symbol]; ok {
				value = candidate
				break
			}
		}
		if value == "" {
			value = components[""]
		}
	} else if candidate, ok := components[strings.ToUpper(strings.TrimSpace(selected.GetSymbol()))]; ok {
		value = candidate
	}
	if value == "" {
		return "", fmt.Errorf("seçili asset için canlı bakiye dönmedi: %s", selected.GetSymbol())
	}
	raw, ok := adminBalanceValueToRaw(value, selected.GetDecimals())
	if !ok {
		return "", fmt.Errorf("chain bakiye değeri okunamadı: %s", value)
	}
	return raw, nil
}

func adminParseBalanceComponents(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}
	}
	components := make(map[string]string)
	parts := strings.Split(raw, "|")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			if len(parts) == 1 {
				components[""] = part
			}
			continue
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			components[key] = value
		}
	}
	return components
}

func adminNativeBalanceSymbols(selected asset.Asset) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 3)
	add := func(symbol string) {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol == "" {
			return
		}
		if _, ok := seen[symbol]; ok {
			return
		}
		seen[symbol] = struct{}{}
		out = append(out, symbol)
	}
	add(selected.GetSymbol())
	switch selected.GetChainID() {
	case constants.Bitcoin:
		add("BTC")
		add("BITCOIN")
	case constants.Ethereum:
		add("ETH")
		add("ETHEREUM")
	case constants.TRON, constants.TRONTestnet:
		add("TRX")
		add("TRON")
	case constants.Solana:
		add("SOL")
		add("SOLANA")
	}
	return out
}

func adminBalanceValueToRaw(value string, decimals uint8) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") {
		return "", false
	}
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		amount, ok := new(big.Int).SetString(value[2:], 16)
		if !ok || amount.Sign() < 0 {
			return "", false
		}
		return amount.String(), true
	}
	if !strings.Contains(value, ".") {
		amount, ok := new(big.Int).SetString(value, 10)
		if !ok || amount.Sign() < 0 {
			return "", false
		}
		return amount.String(), true
	}

	decimal, ok := new(big.Rat).SetString(value)
	if !ok || decimal.Sign() < 0 {
		return "", false
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	decimal.Mul(decimal, new(big.Rat).SetInt(scale))
	if decimal.Denom().Cmp(big.NewInt(1)) != 0 {
		return "", false
	}
	return new(big.Int).Set(decimal.Num()).String(), true
}

const adminEVMNativeTransferGasLimit uint64 = 21_000

func adminRecoverNetAmountRaw(ctx context.Context, chain blockchain.Chain, selected asset.Asset, grossRaw string) (string, string, error) {
	grossRaw = strings.TrimSpace(grossRaw)
	gross, ok := new(big.Int).SetString(grossRaw, 10)
	if !ok || gross.Sign() <= 0 {
		return "", "", errors.New("amount_raw pozitif integer olmalı")
	}
	fee, err := adminRecoverNativeFeeRaw(ctx, chain, selected)
	if err != nil {
		return "", "", err
	}
	if fee == nil || fee.Sign() <= 0 {
		return gross.String(), "0", nil
	}
	net := new(big.Int).Sub(gross, fee)
	if net.Sign() <= 0 {
		return "", "", fmt.Errorf("amount_raw network fee sonrası sıfır/negatif kalıyor: gross=%s fee=%s", gross.String(), fee.String())
	}
	return net.String(), fee.String(), nil
}

func adminRecoverNativeFeeRaw(ctx context.Context, chain blockchain.Chain, selected asset.Asset) (*big.Int, error) {
	if selected == nil || !selected.IsNative() {
		return big.NewInt(0), nil
	}
	switch selected.GetChainID() {
	case constants.TRON, constants.TRONTestnet:
		fee, err := chainresource.TronNativeSweepFeeSUN()
		if err != nil {
			return nil, err
		}
		return big.NewInt(fee), nil
	case constants.Solana:
		fee, err := chainresource.SolanaTransferFeeLamports()
		if err != nil {
			return nil, err
		}
		return new(big.Int).SetUint64(fee), nil
	case constants.Bitcoin:
		feeRate, err := chainresource.BitcoinFeeRateSatPerVByte()
		if err != nil {
			return nil, err
		}
		const estimatedNativeTransferVSize = int64(10 + 68 + 31*2)
		return big.NewInt(estimatedNativeTransferVSize * feeRate), nil
	default:
		if isEVMChain(selected.GetChainID()) {
			return adminRecoverEVMNativeFeeRaw(ctx, chain)
		}
		return big.NewInt(0), nil
	}
}

func adminRecoverEVMNativeFeeRaw(ctx context.Context, chain blockchain.Chain) (*big.Int, error) {
	if chain == nil {
		return nil, errors.New("chain hazır değil")
	}
	var lastErr error
	for _, rpcURL := range chain.RPCs() {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}
		client, err := ethclient.DialContext(ctx, rpcURL)
		if err != nil {
			lastErr = fmt.Errorf("%s %s gas RPC bağlantısı başarısız: %w", chain.Name(), rpcURL, err)
			continue
		}
		gasPrice, err := client.SuggestGasPrice(ctx)
		client.Close()
		if err != nil {
			lastErr = fmt.Errorf("%s %s gas price okunamadı: %w", chain.Name(), rpcURL, err)
			continue
		}
		if err := chainresource.ValidateEVMGasPolicy(chain.Name(), "admin.recover_funds", gasPrice, adminEVMNativeTransferGasLimit); err != nil {
			return nil, err
		}
		return new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(adminEVMNativeTransferGasLimit)), nil
	}
	if lastErr == nil {
		lastErr = errors.New("EVM gas fee için RPC endpoint bulunamadı")
	}
	return nil, lastErr
}

func HandleAdminTestPaymentLinkCreate(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if deps.ProductRepo == nil || deps.DomainService == nil {
			return redirectWithError(c, "/admin/tests", "Test payment link altyapısı hazır değil.")
		}

		domainID, err := uuid.Parse(strings.TrimSpace(c.FormValue("domain_id")))
		if err != nil {
			return redirectWithError(c, "/admin/tests", "Geçerli domain seçmelisin.")
		}
		domainIDString := domainID.String()
		domain, err := deps.DomainService.FindByID(types.DomainParams{
			Context:  c.Context(),
			DomainID: &domainIDString,
		})
		if err != nil || domain == nil || strings.TrimSpace(domain.DomainURL) == "_reserve_" {
			return redirectWithError(c, "/admin/tests", "Test link için gerçek domain bulunamadı.")
		}

		linkType := models.NormalizePaymentLinkType(c.FormValue("link_type"))
		name := strings.TrimSpace(c.FormValue("name"))
		if name == "" {
			if models.IsDonationLinkType(linkType) {
				name = "Admin Test Donation"
			} else {
				name = "Admin Test Payment"
			}
		}
		amount := strings.TrimSpace(c.FormValue("amount"))
		currency := strings.ToUpper(strings.TrimSpace(c.FormValue("currency")))
		if models.IsDonationLinkType(linkType) {
			amount = "0"
			currency = ""
		} else {
			if amount == "" {
				amount = "10"
			}
			if err := types.ValidatePositiveDecimal(amount); err != nil {
				return redirectWithError(c, "/admin/tests", "Payment link tutarı pozitif decimal olmalı.")
			}
			if currency == "" {
				currency = "USD"
			}
		}

		product := &models.Product{
			MerchantID:  domain.MerchantID,
			DomainID:    domain.ID,
			Name:        name,
			Description: "Admin test link",
			LinkType:    linkType,
			Amount:      amount,
			Currency:    currency,
			Language:    normalizeLanguage(c.FormValue("language")),
			SuccessURL:  strings.TrimSpace(c.FormValue("success_url")),
			CancelURL:   strings.TrimSpace(c.FormValue("cancel_url")),
			IsActive:    true,
		}
		if err := deps.ProductRepo.Create(c.Context(), product); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &domain.MerchantID, "admin", adminEmail, "test_link.create", "failed", "product", name, err.Error())
			return redirectWithError(c, "/admin/tests", "Test payment link oluşturulamadı: "+err.Error())
		}

		link := baseURL(c) + "/payment-links/" + product.LinkToken
		logDealerActivity(c, deps.ActivityLogRepo, &domain.MerchantID, "admin", adminEmail, "test_link.create", "success", "product", product.ID.String(), "Admin test payment link oluşturuldu.")
		return redirectWithSuccess(c, "/admin/tests", "Test link oluşturuldu: "+link)
	}
}

func HandleAdminTestDeposit(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if adminTestPaymentOutcome(c) == "fail" {
			return handleAdminTestPaymentFailure(c, deps, adminEmail)
		}
		if deps.WalletRepo == nil || deps.TransactionRepo == nil || deps.LedgerRepo == nil || deps.Notifier == nil {
			return redirectWithError(c, "/admin/test-deposit", "Test deposit altyapısı hazır değil.")
		}

		var matchedSession *models.PaymentSession
		var wallet *models.Wallet
		var selectedAsset asset.Asset
		var toAddress string
		var amount string
		var amountRaw string

		paymentSessionIDRaw := strings.TrimSpace(firstNonEmpty(c.FormValue("payment_session_id"), c.FormValue("session_id")))
		if paymentSessionIDRaw != "" {
			if deps.PaymentRepo == nil {
				return redirectWithError(c, "/admin/test-deposit", "Payment repository hazır değil.")
			}
			paymentSessionID, err := uuid.Parse(paymentSessionIDRaw)
			if err != nil {
				return redirectWithError(c, "/admin/test-deposit", "Geçerli checkout session seçmelisin.")
			}
			matchedSession, err = deps.PaymentRepo.FindByID(c.Context(), paymentSessionID)
			if err != nil {
				return redirectWithError(c, "/admin/test-deposit", "Checkout session bulunamadı: "+err.Error())
			}
			selectedAsset, err = adminPaymentSessionAsset(deps.AssetRegistry, *matchedSession)
			if err != nil {
				return redirectWithError(c, "/admin/test-deposit", err.Error())
			}
			wallet, err = deps.WalletRepo.FindByID(c.Context(), matchedSession.WalletID)
			if err != nil {
				return redirectWithError(c, "/admin/test-deposit", "Checkout wallet bulunamadı: "+err.Error())
			}
			toAddress = strings.TrimSpace(matchedSession.DepositAddress)
			if toAddress == "" {
				return redirectWithError(c, "/admin/test-deposit", "Checkout için önce asset seçilip deposit address üretilmeli.")
			}
			amount = strings.TrimSpace(c.FormValue("amount"))
			if !models.IsDonationLinkType(matchedSession.LinkType) && positiveTokenAmountRaw(matchedSession.ExpectedAmountRaw) {
				amountRaw = matchedSession.ExpectedAmountRaw
				amount = formatTokenAmount(amountRaw, matchedSession.SelectedDecimals)
			} else {
				if amount == "" {
					amount = "1"
				}
				amountRaw, err = types.DecimalToRaw(amount, selectedAsset.GetDecimals())
				if err != nil {
					return redirectWithError(c, "/admin/test-deposit", "Tutar geçersiz: "+err.Error())
				}
			}
			if !positiveTokenAmountRaw(amountRaw) {
				return redirectWithError(c, "/admin/test-deposit", "Test deposit tutarı pozitif olmalı.")
			}
		} else {
			walletID, err := uuid.Parse(strings.TrimSpace(c.FormValue("wallet_id")))
			if err != nil {
				return redirectWithError(c, "/admin/test-deposit", "Geçerli wallet seçmelisin.")
			}
			wallet, err = deps.WalletRepo.FindByID(c.Context(), walletID)
			if err != nil {
				return redirectWithError(c, "/admin/test-deposit", "Wallet bulunamadı: "+err.Error())
			}

			selectedAsset, err = parseAdminAssetSelection(deps.AssetRegistry, c.FormValue("asset"))
			if err != nil {
				return redirectWithError(c, "/admin/test-deposit", err.Error())
			}
			toAddress = repositories.WalletAddressForChainID(*wallet, selectedAsset.GetChainID())
			if strings.TrimSpace(toAddress) == "" {
				return redirectWithError(c, "/admin/test-deposit", "Seçilen wallet için "+constants.ChainName(selectedAsset.GetChainID())+" adresi yok.")
			}

			amount = strings.TrimSpace(c.FormValue("amount"))
			amountRaw, err = types.DecimalToRaw(amount, selectedAsset.GetDecimals())
			if err != nil {
				return redirectWithError(c, "/admin/test-deposit", "Tutar geçersiz: "+err.Error())
			}
		}

		token := tokenForSelectedAsset(selectedAsset)
		symbol := strings.ToUpper(strings.TrimSpace(selectedAsset.GetSymbol()))
		chainID := selectedAsset.GetChainID()
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
		if matchedSession != nil {
			checkoutPath := checkoutLocalizedURL(matchedSession.SessionToken, "/pay", adminTestCheckoutLang(c), "")
			if adminTestReturnToCheckout(c) {
				return c.Redirect().To(checkoutPath)
			}
			return redirectWithSuccess(c, "/admin/test-deposit", "Checkout test deposit işlendi ve session başarıya taşındı. Checkout: "+baseURL(c)+checkoutPath+" Tx: "+hash)
		}
		return redirectWithSuccess(c, "/admin/test-deposit", "Test deposit işlendi, bakiye yüklendi ve webhook kuyruğa alındı. Tx: "+hash)
	}
}

func handleAdminTestPaymentFailure(c fiber.Ctx, deps DealerDeps, adminEmail string) error {
	if deps.PaymentRepo == nil || deps.WebhookDeliveryRepo == nil {
		return redirectWithError(c, "/admin/test-deposit", "Payment fail test altyapısı hazır değil.")
	}
	paymentSessionIDRaw := strings.TrimSpace(firstNonEmpty(c.FormValue("payment_session_id"), c.FormValue("session_id")))
	paymentSessionID, err := uuid.Parse(paymentSessionIDRaw)
	if err != nil {
		return redirectWithError(c, "/admin/test-deposit", "Fail için geçerli checkout session seçmelisin.")
	}
	reason := strings.TrimSpace(c.FormValue("failure_reason"))
	if reason == "" {
		reason = "Admin test payment failure"
	}
	session, enqueuePaymentWebhook, err := deps.PaymentRepo.MarkFailedForTest(c.Context(), paymentSessionID, reason)
	if err != nil {
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "test_payment.fail", "failed", "payment", paymentSessionID.String(), err.Error())
		return redirectWithError(c, "/admin/test-deposit", "Checkout fail yapılamadı: "+err.Error())
	}
	if session == nil || session.ID == uuid.Nil || session.Status != models.PaymentStatusFailed {
		return redirectWithError(c, "/admin/test-deposit", "Bu checkout fail testi için uygun değil.")
	}

	var enqueueErrors []string
	if enqueuePaymentWebhook {
		paymentDeliveryCtx, cancelPaymentDelivery := context.WithTimeout(context.Background(), 20*time.Second)
		if _, _, err := deps.WebhookDeliveryRepo.EnqueuePayment(paymentDeliveryCtx, session.Domain, *session); err != nil {
			enqueueErrors = append(enqueueErrors, "payment webhook: "+err.Error())
		}
		cancelPaymentDelivery()
	}

	checkoutPath := checkoutLocalizedURL(session.SessionToken, "/pay", adminTestCheckoutLang(c), "")
	if len(enqueueErrors) > 0 {
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "test_payment.fail_webhook", "failed", "payment", session.ID.String(), strings.Join(enqueueErrors, " | "))
		if !adminTestReturnToCheckout(c) {
			return redirectWithError(c, "/admin/test-deposit", "Checkout fail işlendi, ancak "+strings.Join(enqueueErrors, " | "))
		}
	} else {
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "test_payment.fail", "success", "payment", session.ID.String(), "Checkout test payment failed yapıldı.")
	}
	if adminTestReturnToCheckout(c) {
		return c.Redirect().To(checkoutPath)
	}
	return redirectWithSuccess(c, "/admin/test-deposit", "Checkout test payment failed yapıldı. Checkout: "+baseURL(c)+checkoutPath)
}

func adminTestPaymentOutcome(c fiber.Ctx) string {
	switch strings.ToLower(strings.TrimSpace(firstNonEmpty(c.FormValue("test_outcome"), c.FormValue("outcome")))) {
	case "fail", "failed", "failure", "error":
		return "fail"
	default:
		return "success"
	}
}

func adminTestReturnToCheckout(c fiber.Ctx) bool {
	return strings.EqualFold(strings.TrimSpace(c.FormValue("return_to")), "checkout")
}

func adminTestCheckoutLang(c fiber.Ctx) string {
	return normalizeLanguage(firstNonEmpty(c.FormValue("lang"), c.Query("lang")))
}

func HandleAdminWithdrawalApprove(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "withdrawal.approve", "failed", "withdrawal", c.Params("id"), err.Error())
			return redirectWithError(c, "/admin", err.Error())
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/withdrawals", "Geçersiz talep.")
		}
		request, err := deps.WithdrawalRepo.Find(c.Context(), id)
		if err != nil {
			return redirectWithError(c, "/admin/withdrawals", "Pending talep bulunamadı.")
		}
		switch request.Status {
		case models.WithdrawalStatusPending:
		case models.WithdrawalStatusProcessing, models.WithdrawalStatusFinalized, models.WithdrawalStatusApproved:
			logDealerActivity(c, deps.ActivityLogRepo, &request.MerchantID, "admin", adminEmail, "withdrawal.approve", "success", "withdrawal", id.String(), "Çekim onayı zaten işlenmiş.")
			return redirectWithSuccess(c, "/admin/withdrawals", "Çekim onayı zaten işlenmiş.")
		default:
			logDealerActivity(c, deps.ActivityLogRepo, &request.MerchantID, "admin", adminEmail, "withdrawal.approve", "failed", "withdrawal", id.String(), "Pending talep bulunamadı.")
			return redirectWithError(c, "/admin/withdrawals", "Pending talep bulunamadı.")
		}
		if err := requireOutboundMakerChecker(request.RequestedBy, adminEmail); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &request.MerchantID, "admin", adminEmail, "withdrawal.approve", "failed", "withdrawal", id.String(), err.Error())
			return redirectWithError(c, "/admin/withdrawals", err.Error())
		}
		if err := enforceDealerOutboundPolicy(c.Context(), deps, outboundPolicyCheckFromWithdrawal("withdrawal.approve", request, true)); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &request.MerchantID, "admin", adminEmail, "withdrawal.approve", "failed", "withdrawal", id.String(), err.Error())
			return redirectWithError(c, "/admin/withdrawals", err.Error())
		}
		approvedRequest, outboundTx, err := deps.WithdrawalRepo.ApproveForOutbound(c.Context(), id, adminEmail, deps.LedgerRepo, deps.OutboundRepo)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &request.MerchantID, "admin", adminEmail, "withdrawal.approve", "failed", "withdrawal", id.String(), err.Error())
			if approvedRequest != nil && approvedRequest.Status == models.WithdrawalStatusProcessing {
				openDealerOutboundLifecycleReconciliation(c.Context(), deps, approvedRequest.Chain, &approvedRequest.MerchantID, approvedRequest.DomainID, "withdrawal", approvedRequest.ID.String(), approvedRequest.Status, "outbound_broadcast_uncertain", approvedRequest.Error, approvedRequest.TxHash)
			}
			if approvedRequest != nil && approvedRequest.Status == models.WithdrawalStatusFailed {
				enqueueDealerPayoutLifecycle(c.Context(), deps, *approvedRequest, constants.WebhookEventPayoutFailedV1)
			}
			return redirectWithError(c, "/admin/withdrawals", "Transfer başarısız: "+err.Error())
		}
		afterStatus := models.WithdrawalStatusProcessing
		if approvedRequest != nil {
			afterStatus = approvedRequest.Status
		}
		resourceID := id.String()
		if outboundTx != nil {
			resourceID = outboundTx.ID.String()
		}
		logDealerDecisionActivity(c, deps.ActivityLogRepo, &request.MerchantID, request.DomainID, "admin", adminEmail, "withdrawal.approve", "success", "outbound_transaction", resourceID, "Çekim onaylandı ve yayın kuyruğuna alındı.", request.Status, afterStatus)
		return redirectWithSuccess(c, "/admin/withdrawals", "Çekim onaylandı ve yayın kuyruğuna alındı.")
	}
}

func HandleAdminWithdrawalReject(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "withdrawal.reject", "failed", "withdrawal", c.Params("id"), err.Error())
			return redirectWithError(c, "/admin", err.Error())
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/withdrawals", "Geçersiz talep.")
		}
		request, findErr := deps.WithdrawalRepo.Find(c.Context(), id)
		reason := strings.TrimSpace(c.FormValue("reason"))
		if reason == "" {
			reason = "Admin tarafından reddedildi."
		}
		if findErr != nil || request == nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "withdrawal.reject", "failed", "withdrawal", id.String(), "Pending talep bulunamadı.")
			return redirectWithError(c, "/admin/withdrawals", "Pending talep bulunamadı.")
		}
		switch request.Status {
		case models.WithdrawalStatusPending:
		case models.WithdrawalStatusRejected:
			logDealerActivity(c, deps.ActivityLogRepo, &request.MerchantID, "admin", adminEmail, "withdrawal.reject", "success", "withdrawal", id.String(), "Çekim talebi zaten reddedilmiş.")
			return redirectWithSuccess(c, "/admin/withdrawals", "Çekim talebi zaten reddedilmiş.")
		default:
			logDealerActivity(c, deps.ActivityLogRepo, &request.MerchantID, "admin", adminEmail, "withdrawal.reject", "failed", "withdrawal", id.String(), "Pending talep bulunamadı.")
			return redirectWithError(c, "/admin/withdrawals", "Pending talep bulunamadı.")
		}
		if err := deps.WithdrawalRepo.MarkRejected(c.Context(), id, adminEmail, reason); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "withdrawal.reject", "failed", "withdrawal", id.String(), err.Error())
			return redirectWithError(c, "/admin/withdrawals", "Talep reddedilemedi: "+err.Error())
		}
		if updated, err := deps.WithdrawalRepo.Find(c.Context(), id); err == nil && updated != nil {
			enqueueDealerPayoutLifecycle(c.Context(), deps, *updated, constants.WebhookEventPayoutRejectedV1)
			logDealerDecisionActivity(c, deps.ActivityLogRepo, &updated.MerchantID, updated.DomainID, "admin", adminEmail, "withdrawal.reject", "success", "withdrawal", id.String(), reason, request.Status, updated.Status)
		} else if findErr == nil && request != nil {
			logDealerDecisionActivity(c, deps.ActivityLogRepo, &request.MerchantID, request.DomainID, "admin", adminEmail, "withdrawal.reject", "success", "withdrawal", id.String(), reason, request.Status, models.WithdrawalStatusRejected)
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
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "refund.approve", "failed", "refund", c.Params("id"), err.Error())
			return redirectWithError(c, "/admin", err.Error())
		}
		if deps.RefundRepo == nil || deps.PaymentRepo == nil || deps.TransactionRepo == nil {
			return redirectWithError(c, "/admin/refunds", "Refund altyapısı hazır değil.")
		}

		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/refunds", "Geçersiz refund.")
		}
		refund, err := deps.RefundRepo.Find(c.Context(), id)
		if err != nil {
			return redirectWithError(c, "/admin/refunds", "Pending refund bulunamadı.")
		}
		switch refund.Status {
		case models.RefundStatusPending:
		case models.RefundStatusProcessing, models.RefundStatusSucceeded, models.RefundStatusApproved:
			logDealerActivity(c, deps.ActivityLogRepo, &refund.MerchantID, "admin", adminEmail, "refund.approve", "success", "refund", id.String(), "Refund onayı zaten işlenmiş.")
			return redirectWithSuccess(c, "/admin/refunds", "Refund onayı zaten işlenmiş.")
		default:
			logDealerActivity(c, deps.ActivityLogRepo, &refund.MerchantID, "admin", adminEmail, "refund.approve", "failed", "refund", id.String(), "Pending refund bulunamadı.")
			return redirectWithError(c, "/admin/refunds", "Pending refund bulunamadı.")
		}
		if err := requireOutboundMakerChecker(refund.RequestedBy, adminEmail); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &refund.MerchantID, "admin", adminEmail, "refund.approve", "failed", "refund", id.String(), err.Error())
			return redirectWithError(c, "/admin/refunds", err.Error())
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
		refund.Chain = constants.ChainName(*session.SelectedChainID)
		refund.Token = session.SelectedToken
		refund.Symbol = session.SelectedSymbol
		refund.Decimals = session.SelectedDecimals
		if err := enforceDealerOutboundPolicy(c.Context(), deps, outboundPolicyCheckFromRefund("refund.approve", refund, toAddress, true)); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &refund.MerchantID, "admin", adminEmail, "refund.approve", "failed", "refund", id.String(), err.Error())
			return redirectWithError(c, "/admin/refunds", err.Error())
		}

		reserveWallet, err := ensureDealerReserveWallet(c.Context(), refund.MerchantID, deps)
		if err != nil {
			return redirectWithError(c, "/admin/refunds", "Bayi reserve cüzdanı hazırlanamadı: "+err.Error())
		}
		claimedRefund, outboundTx, err := deps.RefundRepo.ClaimPendingWithHoldAndSourceForOutbound(c.Context(), id, adminEmail, *session, *reserveWallet, toAddress, deps.LedgerRepo, deps.OutboundRepo)
		if err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, &refund.MerchantID, "admin", adminEmail, "refund.approve", "failed", "refund", id.String(), err.Error())
			return redirectWithError(c, "/admin/refunds", "Refund başka bir işlem tarafından alınmış, artık pending değil veya ledger rezervasyonu yapılamadı: "+err.Error())
		}
		refundDomainID := refund.DomainID
		resourceID := id.String()
		if outboundTx != nil {
			resourceID = outboundTx.ID.String()
		}
		logDealerDecisionActivity(c, deps.ActivityLogRepo, &refund.MerchantID, &refundDomainID, "admin", adminEmail, "refund.approve", "success", "outbound_transaction", resourceID, "Refund yayın kuyruğuna alındı.", refund.Status, claimedRefund.Status)
		return redirectWithSuccess(c, "/admin/refunds", "Refund onaylandı ve yayın kuyruğuna alındı.")
	}
}

func HandleAdminRefundReject(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		if err := requirePrivilegedAdmin(c, deps.AdminRepo); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "refund.reject", "failed", "refund", c.Params("id"), err.Error())
			return redirectWithError(c, "/admin", err.Error())
		}
		if deps.RefundRepo == nil {
			return redirectWithError(c, "/admin/refunds", "Refund altyapısı hazır değil.")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return redirectWithError(c, "/admin/refunds", "Geçersiz refund.")
		}
		refund, findErr := deps.RefundRepo.Find(c.Context(), id)
		var merchantID *uuid.UUID
		if findErr == nil && refund != nil {
			merchantID = &refund.MerchantID
		}
		reason := strings.TrimSpace(c.FormValue("reason"))
		if reason == "" {
			reason = "Admin tarafından reddedildi."
		}
		if findErr != nil || refund == nil {
			logDealerActivity(c, deps.ActivityLogRepo, merchantID, "admin", adminEmail, "refund.reject", "failed", "refund", id.String(), "Pending refund bulunamadı.")
			return redirectWithError(c, "/admin/refunds", "Pending refund bulunamadı.")
		}
		switch refund.Status {
		case models.RefundStatusPending:
		case models.RefundStatusRejected:
			logDealerActivity(c, deps.ActivityLogRepo, merchantID, "admin", adminEmail, "refund.reject", "success", "refund", id.String(), "Refund talebi zaten reddedilmiş.")
			return redirectWithSuccess(c, "/admin/refunds", "Refund talebi zaten reddedilmiş.")
		default:
			logDealerActivity(c, deps.ActivityLogRepo, merchantID, "admin", adminEmail, "refund.reject", "failed", "refund", id.String(), "Pending refund bulunamadı.")
			return redirectWithError(c, "/admin/refunds", "Pending refund bulunamadı.")
		}
		if err := deps.RefundRepo.MarkRejected(c.Context(), id, adminEmail, reason); err != nil {
			logDealerActivity(c, deps.ActivityLogRepo, merchantID, "admin", adminEmail, "refund.reject", "failed", "refund", id.String(), err.Error())
			return redirectWithError(c, "/admin/refunds", "Refund reddedilemedi: "+err.Error())
		}
		afterStatus := models.RefundStatusRejected
		var domainID *uuid.UUID
		if updated, err := deps.RefundRepo.Find(c.Context(), id); err == nil {
			enqueueDealerRefundLifecycle(c.Context(), deps, *updated, constants.WebhookEventRefundRejectedV1)
			merchantID = &updated.MerchantID
			domain := updated.DomainID
			domainID = &domain
			afterStatus = updated.Status
		}
		logDealerDecisionActivity(c, deps.ActivityLogRepo, merchantID, domainID, "admin", adminEmail, "refund.reject", "success", "refund", id.String(), reason, refund.Status, afterStatus)
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
			logDealerActivity(c, activityRepo, nil, actorKind, "", callbackEvent, "failed", "auth", "", "OIDC kullanıcı bilgisi doğrulanamadı: "+err.Error())
			return redirectWithError(c, loginPath, "OIDC kullanıcı bilgisi doğrulanamadı.")
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
			if err := provisionMerchantReserve(c.Context(), merchant.ID, deps[0]); err != nil {
				logDealerActivity(c, activityRepo, &merchant.ID, "dealer", merchant.Email, "dealer.reserve_provision", "failed", "merchant", merchant.ID.String(), "OIDC reserve cüzdanı hazırlanamadı: "+err.Error())
				setDealerSessionCookie(c, merchant.ID.String())
				return redirectWithError(c, "/merchant/dashboard", "Giriş tamamlandı ancak reserve cüzdanı hazırlanamadı. Lütfen tekrar deneyin.")
			}
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
			var extraClaims oidcUserInfo
			if err := userInfo.Claims(&extraClaims); err != nil {
				return nil, errors.New("OIDC userinfo claim'leri geçersiz")
			}
			if claims.Name == "" {
				claims.Name = extraClaims.Name
			}
			if claims.Email == "" {
				claims.Email = extraClaims.Email
			}
			if claims.Sub == "" {
				claims.Sub = extraClaims.Sub
			}
			if claims.EmailVerified == nil || (extraClaims.EmailVerified != nil && !bool(*extraClaims.EmailVerified)) {
				claims.EmailVerified = extraClaims.EmailVerified
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
	if err := requireOIDCVerifiedEmail(&claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

func requireOIDCVerifiedEmail(userInfo *oidcUserInfo) error {
	if userInfo == nil || userInfo.EmailVerified == nil {
		return errors.New("OIDC email doğrulama bilgisi dönmedi")
	}
	if !bool(*userInfo.EmailVerified) {
		return errors.New("OIDC email adresi doğrulanmamış")
	}
	return nil
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
		email, role, ok := verifyActiveAdminSession(c, adminRepo)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		c.Locals(adminSessionEmailLocal, email)
		c.Locals(adminSessionRoleLocal, role)
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

func verifyActiveAdminSession(c fiber.Ctx, adminRepo *repositories.AdminRepo) (string, string, bool) {
	payload, err := verifyDealerSessionValue(c.Cookies(adminSessionCookie))
	if err != nil || strings.TrimSpace(payload) == "" {
		clearAdminSessionCookie(c)
		return "", "", false
	}
	email, err := parseAdminSessionPayload(payload, time.Now())
	if err != nil || strings.TrimSpace(email) == "" {
		clearAdminSessionCookie(c)
		return "", "", false
	}
	if adminRepo == nil {
		clearAdminSessionCookie(c)
		return "", "", false
	}
	admin, err := adminRepo.FindByEmail(c.Context(), email)
	if err != nil || admin == nil || !admin.IsActive {
		clearAdminSessionCookie(c)
		return "", "", false
	}
	return admin.Email, models.EffectiveAdminRole(admin.Role), true
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

func adminRoleCanMutateHighRisk(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "admin", models.AdminRoleOwner, models.AdminRoleSecurity:
		return true
	default:
		return false
	}
}

func requirePrivilegedAdmin(c fiber.Ctx, adminRepo *repositories.AdminRepo) error {
	adminEmail, ok := requireAdmin(c)
	if !ok || adminRepo == nil {
		return errors.New("Bu işlem için yetkiniz yok.")
	}
	admin, err := adminRepo.FindByEmail(c.Context(), adminEmail)
	if err != nil || admin == nil || !admin.IsActive {
		return errors.New("Bu işlem için yetkiniz yok.")
	}
	c.Locals(adminSessionRoleLocal, models.EffectiveAdminRole(admin.Role))
	if !adminRoleCanMutateHighRisk(admin.Role) {
		return errors.New("Bu işlem için güvenlik yetkisi gerekli.")
	}
	return nil
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
		Title:               title,
		Active:              active,
		OIDCLoginURL:        oidcURL,
		OIDCProvider:        provider,
		RegisterURL:         "/merchant/register",
		LoginURL:            "/merchant/login",
		OnboardingURL:       "/merchant/onboarding",
		DashboardURL:        "/merchant/dashboard",
		TreasuryURL:         "/merchant/dashboard/treasury",
		ActivityURL:         "/merchant/dashboard/activity",
		ActivityAuditURL:    merchantDashboardActivityAuditURL,
		ActivityPaymentsURL: merchantDashboardActivityPaymentsURL,
		ActivityDepositsURL: merchantDashboardActivityDepositsURL,
		TransactionsURL:     "/merchant/dashboard/transactions",
		UsersURL:            "/merchant/dashboard/users",
		WithdrawalsURL:      "/merchant/dashboard/withdrawals",
		RescanURL:           "/merchant/dashboard/rescan",
		IntegrationsURL:     merchantDashboardIntegrationsURL,
		DomainsPanelURL:     merchantDashboardDomainsURL,
		ProductsURL:         "/merchant/products",
		InvoicesURL:         "/merchant/invoices",
		ProductsPanelURL:    merchantDashboardProductsURL,
		ProductsLinksURL:    merchantDashboardLinksURL,
		SettingsPanelURL:    "/merchant/dashboard/settings",
		DomainsURL:          "/merchant/domains",
		LogoutURL:           "/merchant/logout",
		ActivePanel:         "overview",
		RescanChains:        dealerRescanChainOptions(),
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
	case "", "overview", "dashboard", "home":
		return "overview"
	case "treasury":
		return "treasury"
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
	case "products", "integrations":
		return "products"
	case "users":
		return "users"
	case "settings":
		return "settings"
	default:
		return "overview"
	}
}

func dealerRefundsRouteRequested(c fiber.Ctx) bool {
	section := strings.ToLower(strings.TrimSpace(c.Params("section")))
	if section == "refunds" {
		return true
	}
	path := strings.ToLower(strings.TrimSuffix(c.Path(), "/"))
	return strings.HasSuffix(path, "/dashboard/refunds")
}

func redirectDealerRefundsRoute(c fiber.Ctx) error {
	return redirectWithError(c, merchantDashboardRefundsRedirectURL, "Merchant refund paneli henüz ayrı bir yüzey değil; refund kanıtları Payments aktivitesinde ve admin Refunds karar akışında izlenir.")
}

func currentDashboardPanel(c fiber.Ctx) string {
	path := strings.ToLower(strings.TrimSuffix(c.Path(), "/"))
	if path == "/merchant/domains" || path == "/dealer/domains" {
		return "domains"
	}
	return dashboardPanel(c.Params("section"))
}

func integrationsDashboardTab(c fiber.Ctx) string {
	tab := strings.ToLower(strings.TrimSpace(c.Params("subsection")))
	if tab == "" {
		path := strings.ToLower(strings.TrimSuffix(c.Path(), "/"))
		switch {
		case strings.HasSuffix(path, "/dashboard/domains") || strings.HasSuffix(path, "/domains"):
			tab = "domains"
		case strings.HasSuffix(path, "/dashboard/products/links"):
			tab = "links"
		case strings.HasSuffix(path, "/dashboard/products/index") || strings.HasSuffix(path, "/dashboard/products"):
			tab = "products"
		}
	}
	switch tab {
	case "index", "products":
		return "products"
	case "links":
		return "links"
	case "domains":
		return "domains"
	default:
		return "products"
	}
}

func activityDashboardTab(c fiber.Ctx) string {
	tab := strings.ToLower(strings.TrimSpace(c.Params("subsection")))
	if tab == "" {
		tab = strings.ToLower(strings.TrimSpace(c.Query("tab")))
	}
	if tab == "" && strings.TrimSpace(c.Query("status")) != "" {
		tab = "payments"
	}
	switch tab {
	case "payments", "payment":
		return "payments"
	case "deposits", "deposit", "transactions":
		return "deposits"
	case "audit", "logs", "logger":
		return "audit"
	default:
		return "deposits"
	}
}

func merchantDashboardPageLimit(c fiber.Ctx) int {
	limit := parseQueryInt(c.Query("limit"), 20)
	if limit < 1 || limit > 100 {
		return 20
	}
	return limit
}

func adminDashboardPageParams(c fiber.Ctx) (int, int) {
	page := 1
	limit := 50
	if info, ok := paginate.FromContext(c.Context()); ok && info != nil {
		page = info.Page
		limit = info.Limit
	} else {
		page = parseQueryInt(c.Query("page"), page)
		limit = parseQueryInt(c.Query("limit"), limit)
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = constants.DEFAULT_LIMIT
	}
	return page, limit
}

func activityPaymentPaginationBase(status string) string {
	base := merchantDashboardActivityPaymentsURL
	if status = strings.TrimSpace(status); status != "" {
		base += "?status=" + url.QueryEscape(status)
	}
	return base
}

func normalizeAdminWithdrawalStatusFilter(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.WithdrawalStatusPending:
		return models.WithdrawalStatusPending
	case models.WithdrawalStatusProcessing:
		return models.WithdrawalStatusProcessing
	case models.WithdrawalStatusApproved:
		return models.WithdrawalStatusApproved
	case models.WithdrawalStatusFinalized:
		return models.WithdrawalStatusFinalized
	case models.WithdrawalStatusRejected:
		return models.WithdrawalStatusRejected
	case models.WithdrawalStatusFailed:
		return models.WithdrawalStatusFailed
	default:
		return ""
	}
}

func buildAdminWithdrawalPaginationBase(status string) string {
	base := "/admin/withdrawals"
	if status = normalizeAdminWithdrawalStatusFilter(status); status != "" {
		base += "?status=" + url.QueryEscape(status)
	}
	return base
}

func buildAdminActivityPaginationBase(merchantID string) string {
	base := "/admin/activity"
	if merchantID = strings.TrimSpace(merchantID); merchantID != "" {
		base += "?merchant_id=" + url.QueryEscape(merchantID)
	}
	return base
}

func adminRecoverPaginationBase(assetValue string) string {
	if assetValue = strings.TrimSpace(assetValue); assetValue != "" {
		if parts := strings.SplitN(assetValue, "|", 2); len(parts) == 2 {
			chainID := strings.TrimSpace(parts[0])
			identifier := strings.TrimSpace(parts[1])
			if chainID != "" && identifier != "" {
				return "/admin/recover/" + url.PathEscape(chainID) + "/" + url.PathEscape(identifier)
			}
		}
		return "/admin/recover?asset=" + url.QueryEscape(assetValue)
	}
	return "/admin/recover"
}

func adminRecoverAssetValueFromRequest(registry *asset.Registry, c fiber.Ctx) string {
	queryValue := strings.TrimSpace(c.Query("asset"))
	if queryValue != "" {
		return queryValue
	}
	chainRaw := strings.TrimSpace(c.Params("chain_id"))
	assetRaw := strings.TrimSpace(c.Params("asset"))
	if chainRaw == "" || assetRaw == "" {
		return ""
	}
	chainInt, err := strconv.ParseInt(chainRaw, 10, 64)
	if err != nil {
		return ""
	}
	identifier, err := url.PathUnescape(assetRaw)
	if err != nil {
		identifier = assetRaw
	}
	if registry != nil {
		if selected, ok := registry.Get(constants.ChainID(chainInt), identifier); ok {
			return fmt.Sprintf("%d|%s", selected.GetChainID(), selected.GetIdentifier())
		}
	}
	return fmt.Sprintf("%d|%s", chainInt, strings.TrimSpace(identifier))
}

func adminMerchantDetailTab(c fiber.Ctx) string {
	switch strings.ToLower(strings.TrimSpace(c.Query("tab"))) {
	case "wallets":
		return "wallets"
	case "payments":
		return "payments"
	default:
		return "domains"
	}
}

func adminMerchantDetailPaginationBase(merchantID uuid.UUID, tab string, status string) string {
	tab = adminMerchantDetailTabValue(tab)
	base := "/admin/merchants/" + merchantID.String() + "?tab=" + url.QueryEscape(tab)
	if tab == "payments" {
		if status = strings.TrimSpace(status); status != "" {
			base += "&status=" + url.QueryEscape(status)
		}
	}
	return base
}

func adminMerchantDetailTabValue(tab string) string {
	switch strings.ToLower(strings.TrimSpace(tab)) {
	case "wallets":
		return "wallets"
	case "payments":
		return "payments"
	default:
		return "domains"
	}
}

func normalizeAdminPaymentStatusFilter(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.PaymentStatusPending:
		return models.PaymentStatusPending
	case models.PaymentStatusAwaitingPayment:
		return models.PaymentStatusAwaitingPayment
	case models.PaymentStatusPaid:
		return models.PaymentStatusPaid
	case models.PaymentStatusCanceled:
		return models.PaymentStatusCanceled
	case models.PaymentStatusExpired:
		return models.PaymentStatusExpired
	case models.PaymentStatusFailed:
		return models.PaymentStatusFailed
	case models.PaymentStatusUnderpaid:
		return models.PaymentStatusUnderpaid
	case models.PaymentStatusOverpaid:
		return models.PaymentStatusOverpaid
	case models.PaymentStatusPartialPaid:
		return models.PaymentStatusPartialPaid
	default:
		return ""
	}
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
	case "/admin/vault":
		return "vault"
	case "/admin/assets":
		return "assets"
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
	case "/admin/reconciliation":
		return "reconciliation"
	case "/admin/refunds":
		return "refunds"
	case "/admin/readiness":
		return "readiness"
	case "/admin/metrics":
		return "metrics"
	case "/admin/provider-health":
		return "provider-health"
	case "/admin/networks":
		return "networks"
	case "/admin/rescan":
		return "rescan"
	case "/admin/tests":
		return "tests"
	case "/admin/sweep":
		return "sweep"
	case "/admin/recover":
		return "recover"
	case "/admin/test-deposit":
		return "test-deposit"
	case "/admin/admins":
		return "admins"
	case "/admin/security":
		return "security"
	case "/admin/links":
		return "links"
	default:
		if strings.HasPrefix(path, "/admin/recover/") {
			return "recover"
		}
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

		AdminPanel:             panel,
		AdminOverviewURL:       "/admin",
		AdminMerchantsURL:      "/admin/merchants",
		AdminVaultURL:          "/admin/vault",
		AdminAssetsURL:         "/admin/assets",
		AdminPaymentsURL:       "/admin/payments",
		AdminDepositsURL:       "/admin/deposits",
		AdminWithdrawalsURL:    "/admin/withdrawals",
		AdminWalletsURL:        "/admin/wallets",
		AdminActivityURL:       "/admin/activity",
		AdminSweepURL:          "/admin/sweep",
		AdminRecoverURL:        "/admin/recover",
		AdminSecurityURL:       "/admin/security",
		AdminLinksURL:          "/admin/links",
		AdminWebhooksURL:       "/admin/webhooks",
		AdminReconciliationURL: "/admin/reconciliation",
		AdminProviderHealthURL: "/admin/provider-health",
		AdminNetworksURL:       "/admin/networks",
		AdminRefundsURL:        "/admin/refunds",
		AdminReadinessURL:      "/admin/readiness",
		AdminMetricsURL:        "/admin/metrics",
		AdminRescanURL:         "/admin/rescan",
		AdminTestsURL:          "/admin/tests",
		AdminTestDepositURL:    "/admin/test-deposit",
		RescanChains:           dealerRescanChainOptions(),
	}
	return data
}

type dealerPromMetricSeries struct {
	name         string
	labels       string
	displayValue string
	help         string
	numeric      float64
	hasNumeric   bool
	rank         int
	status       string
	statusClass  string
}

func dealerAdminMetricsView(ctx context.Context, deps DealerDeps, requestedTab string) (DealerMetricsSummaryView, []DealerMetricGroupView, []DealerMetricAlertView, []DealerMetricTabView, string, string) {
	checkedAt := time.Now().UTC()
	metricsCtx, cancel := context.WithTimeout(ctx, v1ReadinessTimeout)
	defer cancel()

	raw := buildOperationalMetrics(metricsCtx, OperationalMetricsDeps{
		WebhookDeliveryRepo:     deps.WebhookDeliveryRepo,
		MoneyEventOutboxRepo:    deps.MoneyEventOutboxRepo,
		MoneyEventInboxRepo:     deps.MoneyEventInboxRepo,
		WorkerLeaseRepo:         deps.WorkerLeaseRepo,
		SweepJobRepo:            deps.SweepJobRepo,
		OutboundTransactionRepo: deps.OutboundRepo,
		WithdrawalRepo:          deps.WithdrawalRepo,
		RefundRepo:              deps.RefundRepo,
		ReconciliationRepo:      deps.ReconciliationRepo,
		ChainStateRepo:          deps.ChainStateRepo,
		ProviderHealthRepo:      deps.ProviderHealthRepo,
		WalletAddressLookupRepo: deps.WalletAddressLookupRepo,
		Blockchains:             deps.Blockchains,
	}, func() time.Time { return checkedAt })

	series := dealerParsePrometheusMetrics(raw)
	grouped := make(map[string][]dealerPromMetricSeries)
	collectionErrors := 0
	attentionCount := 0
	healthyCount := 0
	warningCount := 0
	criticalCount := 0
	maxRank := 0
	for _, item := range series {
		key := dealerMetricGroupKey(item.name)
		grouped[key] = append(grouped[key], item)
		if item.name == "gateway_metrics_collection_error" && item.hasNumeric && item.numeric > 0 {
			collectionErrors += int(item.numeric)
		}
		switch {
		case item.rank >= 2:
			criticalCount++
			attentionCount++
		case item.rank == 1:
			warningCount++
			attentionCount++
		default:
			healthyCount++
		}
		if item.rank > maxRank {
			maxRank = item.rank
		}
	}

	groupOrder := []string{"runtime", "operations", "money-events", "chain", "wallet-index", "collectors", "other"}
	groups := make([]DealerMetricGroupView, 0, len(grouped))
	alerts := make([]DealerMetricAlertView, 0, attentionCount)
	for _, key := range groupOrder {
		items := grouped[key]
		if len(items) == 0 {
			continue
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].rank != items[j].rank {
				return items[i].rank > items[j].rank
			}
			if items[i].name != items[j].name {
				return items[i].name < items[j].name
			}
			return items[i].labels < items[j].labels
		})
		groupRank := 0
		groupHealthy := 0
		groupWarning := 0
		groupCritical := 0
		views := make([]DealerMetricView, 0, len(items))
		problemViews := make([]DealerMetricView, 0)
		title, summaryText := dealerMetricGroupText(key)
		for _, item := range items {
			if item.rank > groupRank {
				groupRank = item.rank
			}
			switch {
			case item.rank >= 2:
				groupCritical++
			case item.rank == 1:
				groupWarning++
			default:
				groupHealthy++
			}
			description, action := dealerMetricNarrative(item)
			humanStatus := dealerMetricHumanStatus(item.status, item.rank)
			tone := dealerMetricRankTone(item.rank)
			view := DealerMetricView{
				Name:        item.name,
				DisplayName: dealerMetricDisplayName(item.name),
				Help:        item.help,
				Labels:      dealerMetricLabelsDisplay(item.labels),
				Value:       item.displayValue,
				Status:      humanStatus,
				StatusClass: item.statusClass,
				Tone:        tone,
				Description: description,
				Action:      action,
				IsProblem:   item.rank > 0,
			}
			views = append(views, view)
			if view.IsProblem {
				problemViews = append(problemViews, view)
			}
			if item.rank > 0 {
				alerts = append(alerts, DealerMetricAlertView{
					Title:       dealerMetricDisplayName(item.name),
					Detail:      dealerMetricAlertDetail(item, title, description),
					Action:      action,
					Metric:      item.name,
					Labels:      dealerMetricScope(item),
					Value:       item.displayValue,
					Group:       title,
					Status:      humanStatus,
					StatusClass: item.statusClass,
					Tone:        tone,
					Rank:        item.rank,
				})
			}
		}
		status, statusClass := dealerMetricRankStatus(groupRank)
		groups = append(groups, DealerMetricGroupView{
			Key:           key,
			Title:         title,
			Summary:       summaryText,
			Status:        status,
			StatusClass:   statusClass,
			Tone:          dealerMetricRankTone(groupRank),
			TotalCount:    len(items),
			HealthyCount:  groupHealthy,
			WarningCount:  groupWarning,
			CriticalCount: groupCritical,
			HealthPercent: dealerMetricHealthPercent(groupHealthy, len(items)),
			Items:         views,
			ProblemItems:  problemViews,
		})
	}
	sort.SliceStable(alerts, func(i, j int) bool {
		if alerts[i].Rank != alerts[j].Rank {
			return alerts[i].Rank > alerts[j].Rank
		}
		if alerts[i].Group != alerts[j].Group {
			return alerts[i].Group < alerts[j].Group
		}
		return alerts[i].Title < alerts[j].Title
	})
	activeTab := dealerMetricActiveTab(requestedTab, groups, alerts)
	tabs := dealerMetricTabs(activeTab, groups, alerts, len(series), maxRank)

	status, statusClass := dealerMetricRankStatus(maxRank)
	headline, description := dealerMetricsSummaryText(criticalCount, warningCount, collectionErrors)
	summary := DealerMetricsSummaryView{
		Endpoint:         "/metrics",
		CheckedAt:        checkedAt.Format(time.RFC3339),
		TotalSeries:      len(series),
		TotalGroups:      len(groups),
		HealthyCount:     healthyCount,
		WarningCount:     warningCount,
		CriticalCount:    criticalCount,
		CollectionErrors: collectionErrors,
		AttentionCount:   attentionCount,
		Status:           status,
		StatusClass:      statusClass,
		Tone:             dealerMetricRankTone(maxRank),
		Headline:         headline,
		Description:      description,
	}
	return summary, groups, alerts, tabs, activeTab, raw
}

func dealerMetricActiveTab(requested string, groups []DealerMetricGroupView, alerts []DealerMetricAlertView) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "issues" || requested == "raw" {
		return requested
	}
	for _, group := range groups {
		if requested == group.Key {
			return requested
		}
	}
	if len(alerts) > 0 {
		return "issues"
	}
	if len(groups) > 0 {
		return groups[0].Key
	}
	return "raw"
}

func dealerMetricTabs(active string, groups []DealerMetricGroupView, alerts []DealerMetricAlertView, totalSeries int, maxRank int) []DealerMetricTabView {
	tabs := make([]DealerMetricTabView, 0, len(groups)+2)
	issueRank := 0
	for _, alert := range alerts {
		if alert.Rank > issueRank {
			issueRank = alert.Rank
		}
	}
	issueStatus, issueStatusClass := dealerMetricRankStatus(issueRank)
	tabs = append(tabs, DealerMetricTabView{
		Key:         "issues",
		Label:       "Aksiyon",
		URL:         "/admin/metrics?tab=issues",
		Count:       len(alerts),
		Status:      issueStatus,
		StatusClass: issueStatusClass,
		Tone:        dealerMetricRankTone(issueRank),
		Active:      active == "issues",
	})
	for _, group := range groups {
		tabs = append(tabs, DealerMetricTabView{
			Key:         group.Key,
			Label:       dealerMetricTabLabel(group.Key, group.Title),
			URL:         "/admin/metrics?tab=" + url.QueryEscape(group.Key),
			Count:       group.CriticalCount + group.WarningCount,
			Status:      group.Status,
			StatusClass: group.StatusClass,
			Tone:        group.Tone,
			Active:      active == group.Key,
		})
	}
	rawStatus, rawStatusClass := dealerMetricRankStatus(maxRank)
	tabs = append(tabs, DealerMetricTabView{
		Key:         "raw",
		Label:       "Raw",
		URL:         "/admin/metrics?tab=raw",
		Count:       totalSeries,
		Status:      rawStatus,
		StatusClass: rawStatusClass,
		Tone:        "neutral",
		Active:      active == "raw",
	})
	return tabs
}

func dealerMetricTabLabel(key string, fallback string) string {
	switch key {
	case "runtime":
		return "Runtime"
	case "operations":
		return "Ops"
	case "money-events":
		return "Events"
	case "chain":
		return "Chain"
	case "wallet-index":
		return "Wallet"
	case "collectors":
		return "Collectors"
	default:
		return fallback
	}
}

func dealerParsePrometheusMetrics(raw string) []dealerPromMetricSeries {
	helpByName := make(map[string]string)
	series := make([]dealerPromMetricSeries, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# HELP ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "# HELP "))
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) == 2 {
				helpByName[parts[0]] = parts[1]
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		head, value, ok := dealerMetricLineParts(line)
		if !ok {
			continue
		}
		name, labels := dealerMetricNameAndLabels(head)
		numeric, err := strconv.ParseFloat(value, 64)
		hasNumeric := err == nil
		rank, status, statusClass := dealerMetricSeriesStatus(name, labels, numeric, hasNumeric)
		series = append(series, dealerPromMetricSeries{
			name:         name,
			labels:       labels,
			displayValue: dealerMetricDisplayValue(value, numeric, hasNumeric),
			help:         helpByName[name],
			numeric:      numeric,
			hasNumeric:   hasNumeric,
			rank:         rank,
			status:       status,
			statusClass:  statusClass,
		})
	}
	return series
}

func dealerMetricLineParts(line string) (string, string, bool) {
	if open := strings.Index(line, "{"); open >= 0 {
		close := dealerMetricLabelsEnd(line, open)
		if close < 0 {
			return "", "", false
		}
		head := strings.TrimSpace(line[:close+1])
		fields := strings.Fields(strings.TrimSpace(line[close+1:]))
		if len(fields) == 0 {
			return "", "", false
		}
		return head, fields[0], true
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", false
	}
	return fields[0], fields[1], true
}

func dealerMetricLabelsEnd(line string, open int) int {
	inQuote := false
	escaped := false
	for i := open + 1; i < len(line); i++ {
		switch line[i] {
		case '\\':
			escaped = !escaped
		case '"':
			if !escaped {
				inQuote = !inQuote
			}
			escaped = false
		case '}':
			if !inQuote {
				return i
			}
			escaped = false
		default:
			escaped = false
		}
	}
	return -1
}

func dealerMetricNameAndLabels(head string) (string, string) {
	start := strings.Index(head, "{")
	if start < 0 {
		return head, ""
	}
	name := strings.TrimSpace(head[:start])
	labels := strings.TrimSuffix(strings.TrimSpace(head[start+1:]), "}")
	return name, labels
}

func dealerMetricDisplayValue(raw string, numeric float64, hasNumeric bool) string {
	if !hasNumeric {
		return raw
	}
	if numeric == float64(int64(numeric)) {
		return strconv.FormatInt(int64(numeric), 10)
	}
	value := strconv.FormatFloat(numeric, 'f', 2, 64)
	value = strings.TrimRight(value, "0")
	value = strings.TrimRight(value, ".")
	if value == "" {
		return "0"
	}
	return value
}

func dealerMetricLabelsDisplay(labels string) string {
	labels = strings.TrimSpace(labels)
	if labels == "" {
		return "global"
	}
	labels = strings.ReplaceAll(labels, `","`, `", "`)
	labels = strings.ReplaceAll(labels, `",`, `", `)
	return labels
}

func dealerMetricRankTone(rank int) string {
	switch {
	case rank >= 2:
		return "critical"
	case rank == 1:
		return "warning"
	default:
		return "healthy"
	}
}

func dealerMetricsSummaryText(criticalCount int, warningCount int, collectionErrors int) (string, string) {
	switch {
	case criticalCount > 0:
		return "Aksiyon gereken metrikler var", fmt.Sprintf("%d kritik sinyal ve %d dikkat sinyali bulundu. Önce kırmızı kartları kapatın; collector hatası varsa alttaki raw çıktı yerine collector/reason etiketinden başlayın.", criticalCount, warningCount)
	case warningCount > 0:
		return "Sistem çalışıyor, takip gereken sinyaller var", fmt.Sprintf("%d metrik dikkat istiyor. Bunlar çoğunlukla backlog, failed retry veya degraded provider göstergeleri.", warningCount)
	case collectionErrors > 0:
		return "Metrik toplama hatası var", fmt.Sprintf("%d collector hatası raporlandı. Operasyon durumu eksik veriyle değerlendiriliyor.", collectionErrors)
	default:
		return "Operasyon metrikleri temiz", "Signer, queue, chain, provider ve wallet index sinyallerinde aksiyon gerektiren bir durum görünmüyor."
	}
}

func dealerMetricHealthPercent(healthy int, total int) string {
	if total <= 0 {
		return "0%"
	}
	percent := int((float64(healthy) / float64(total)) * 100)
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return strconv.Itoa(percent) + "%"
}

func dealerMetricHumanStatus(status string, rank int) string {
	if rank >= 2 {
		return "kritik"
	}
	if rank == 1 {
		switch status {
		case "degraded":
			return "degraded"
		case "failed":
			return "failed"
		case "backlog":
			return "backlog"
		default:
			return "dikkat"
		}
	}
	return "sağlıklı"
}

func dealerMetricDisplayName(name string) string {
	switch name {
	case "gateway_build_info":
		return "Build bilgisi"
	case "gateway_migration_strategy_ready":
		return "Migration strategy"
	case "gateway_production_signer_ready":
		return "Production signer gate"
	case "gateway_signer_adapter_ready":
		return "External signer adapter"
	case "gateway_metrics_collection_error":
		return "Metrics collector"
	case "gateway_webhook_delivery_backlog":
		return "Webhook delivery backlog"
	case "gateway_money_event_outbox_backlog":
		return "Money event outbox"
	case "gateway_money_event_inbox_backlog":
		return "Money event inbox"
	case "gateway_money_event_inbox_oldest_age_seconds":
		return "Inbox oldest age"
	case "gateway_money_event_inbox_attempts":
		return "Inbox retry attempts"
	case "gateway_worker_leases":
		return "Worker leases"
	case "gateway_sweep_job_backlog":
		return "Sweep jobs"
	case "gateway_withdrawal_backlog":
		return "Withdrawals"
	case "gateway_refund_backlog":
		return "Refunds"
	case "gateway_reconciliation_jobs":
		return "Reconciliation jobs"
	case "gateway_chain_worker_count":
		return "Chain workers"
	case "gateway_chain_last_processed_block":
		return "Last processed block"
	case "gateway_chain_last_confirmed_block":
		return "Last confirmed block"
	case "gateway_chain_state_age_seconds":
		return "Chain state age"
	case "gateway_provider_health":
		return "Provider health"
	case "gateway_provider_latest_height":
		return "Provider latest height"
	case "gateway_provider_lag_blocks":
		return "Provider lag"
	case "gateway_provider_response_latency_ms":
		return "Provider latency"
	case "gateway_provider_consecutive_failures":
		return "Provider failures"
	case "gateway_provider_failover_decision":
		return "Provider selection"
	case "gateway_wallet_address_lookup_rows":
		return "Wallet address index"
	default:
		return strings.TrimPrefix(name, "gateway_")
	}
}

func dealerMetricNarrative(item dealerPromMetricSeries) (string, string) {
	switch {
	case item.name == "gateway_metrics_collection_error" && item.hasNumeric && item.numeric > 0:
		return "Bir collector veri okuyamadı; bu ekran eksik veya yanıltıcı olabilir.", "collector ve reason etiketini kullanarak ilgili repo/query loglarını kontrol edin."
	case (item.name == "gateway_migration_strategy_ready" || item.name == "gateway_production_signer_ready" || item.name == "gateway_signer_adapter_ready") && item.hasNumeric && item.numeric < 1:
		return "Production gate hazır değil; outbound güvenlik varsayımları tamamlanmamış görünüyor.", "Signer mode, external custody adapter health ve production env ayarlarını doğrulayın."
	case item.name == "gateway_provider_health" && item.hasNumeric && item.numeric < 1:
		return "RPC provider tam sağlıklı değil; listener veya broadcast gecikmeleri görülebilir.", "Provider health ekranında aynı chain/provider için failover reason ve latency değerlerini inceleyin."
	case item.name == "gateway_provider_consecutive_failures" && item.hasNumeric && item.numeric > 0:
		return "Provider probe ardışık hata üretiyor.", "RPC endpoint, rate limit ve failover sıralamasını kontrol edin."
	case strings.Contains(item.labels, `status="dead_letter"`) && item.hasNumeric && item.numeric > 0:
		return "Manuel müdahale isteyen dead-letter kayıt var.", "İlgili queue/job ekranında kayıtları açıp hata sebebini kapatın veya yeniden kuyruğa alın."
	case strings.Contains(item.labels, `status="failed"`) && item.hasNumeric && item.numeric > 0:
		return "Retry edilecek başarısız kayıt var.", "Hata tekrar ediyorsa worker loglarını ve downstream servisleri kontrol edin."
	case strings.Contains(item.name, "backlog") && item.hasNumeric && item.numeric > 0:
		return "İşlenmeyi bekleyen aktif kayıt var.", "Worker lease, throughput ve en eski kayıt yaşını kontrol edin."
	default:
		return "Bu sinyal normal operasyon takibi için izleniyor.", "Aksiyon gerekmiyor."
	}
}

func dealerMetricAlertDetail(item dealerPromMetricSeries, group string, description string) string {
	scope := dealerMetricScope(item)
	if scope == "global" {
		return fmt.Sprintf("%s grubunda değer %s. %s", group, item.displayValue, description)
	}
	return fmt.Sprintf("%s grubunda %s için değer %s. %s", group, scope, item.displayValue, description)
}

func dealerMetricScope(item dealerPromMetricSeries) string {
	parts := make([]string, 0, 4)
	for _, key := range []string{"status", "chain", "provider", "collector", "reason", "mode"} {
		if value := dealerMetricLabelValue(item.labels, key); value != "" && value != "none" {
			parts = append(parts, key+"="+value)
		}
	}
	if len(parts) == 0 {
		return "global"
	}
	return strings.Join(parts, " · ")
}

func dealerMetricLabelValue(labels string, key string) string {
	needle := key + `="`
	start := strings.Index(labels, needle)
	if start < 0 {
		return ""
	}
	i := start + len(needle)
	var b strings.Builder
	escaped := false
	for ; i < len(labels); i++ {
		ch := labels[i]
		if escaped {
			switch ch {
			case 'n':
				b.WriteByte(' ')
			default:
				b.WriteByte(ch)
			}
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			break
		}
		b.WriteByte(ch)
	}
	return strings.TrimSpace(b.String())
}

func dealerMetricSeriesStatus(name string, labels string, numeric float64, hasNumeric bool) (int, string, string) {
	if !hasNumeric {
		return 1, "parse", "badge-amber"
	}
	if name == "gateway_metrics_collection_error" && numeric > 0 {
		return 2, "critical", "badge-red"
	}
	if (name == "gateway_migration_strategy_ready" || name == "gateway_production_signer_ready" || name == "gateway_signer_adapter_ready") && numeric < 1 {
		return 2, "blocked", "badge-red"
	}
	if name == "gateway_provider_health" && numeric < 1 {
		if numeric >= 0.5 {
			return 1, "degraded", "badge-amber"
		}
		return 2, "unhealthy", "badge-red"
	}
	if name == "gateway_provider_consecutive_failures" && numeric > 0 {
		return 1, "attention", "badge-amber"
	}
	if strings.Contains(labels, `status="dead_letter"`) && numeric > 0 {
		return 2, "dead_letter", "badge-red"
	}
	if strings.Contains(labels, `status="failed"`) && numeric > 0 {
		return 1, "failed", "badge-amber"
	}
	if strings.Contains(name, "backlog") && numeric > 0 {
		return 1, "backlog", "badge-amber"
	}
	return 0, "ok", "badge-green"
}

func dealerMetricRankStatus(rank int) (string, string) {
	switch {
	case rank >= 2:
		return "kritik", "badge-red"
	case rank == 1:
		return "dikkat", "badge-amber"
	default:
		return "sağlıklı", "badge-green"
	}
}

func dealerMetricGroupKey(name string) string {
	switch {
	case name == "gateway_metrics_collection_error":
		return "collectors"
	case strings.Contains(name, "money_event"):
		return "money-events"
	case strings.Contains(name, "webhook") || strings.Contains(name, "sweep") || strings.Contains(name, "withdrawal") || strings.Contains(name, "refund") || strings.Contains(name, "reconciliation") || strings.Contains(name, "worker_lease"):
		return "operations"
	case strings.Contains(name, "chain") || strings.Contains(name, "provider"):
		return "chain"
	case strings.Contains(name, "wallet_address"):
		return "wallet-index"
	case strings.Contains(name, "build") || strings.Contains(name, "migration") || strings.Contains(name, "signer"):
		return "runtime"
	default:
		return "other"
	}
}

func dealerMetricGroupText(key string) (string, string) {
	switch key {
	case "runtime":
		return "Runtime ve signer", "Build, migration strategy ve external custody signer readiness."
	case "operations":
		return "Operasyon backlog", "Webhook, sweep, withdrawal, refund, reconciliation ve worker lease sinyalleri."
	case "money-events":
		return "Money event pipeline", "Outbox/inbox backlog, retry age ve attempt dağılımı."
	case "chain":
		return "Chain ve provider", "Listener state, worker count ve RPC provider health göstergeleri."
	case "wallet-index":
		return "Wallet address index", "Normalize edilmiş wallet address lookup kapsamı."
	case "collectors":
		return "Collector errors", "Eksik repo veya başarısız query durumunda üretilen collector hataları."
	default:
		return "Diğer metrikler", "Gruplanmamış low-cardinality operasyon metrikleri."
	}
}

func dealerAdminReadinessView(ctx context.Context, deps DealerDeps) (bool, string, []DealerReadinessLevelView, []DealerReadinessCheckView) {
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	readinessCtx, cancel := context.WithTimeout(ctx, v1ReadinessTimeout)
	defer cancel()

	checks := v1RunReadinessChecks(readinessCtx, V1APIDeps{
		DomainRepo:              deps.DomainRepo,
		WalletRepo:              deps.WalletRepo,
		PaymentRepo:             deps.PaymentRepo,
		ProductRepo:             deps.ProductRepo,
		WithdrawalRepo:          deps.WithdrawalRepo,
		RefundRepo:              deps.RefundRepo,
		LedgerRepo:              deps.LedgerRepo,
		TransactionRepo:         deps.TransactionRepo,
		WebhookDeliveryRepo:     deps.WebhookDeliveryRepo,
		SweepJobRepo:            deps.SweepJobRepo,
		OutboundRepo:            deps.OutboundRepo,
		ReconciliationRepo:      deps.ReconciliationRepo,
		ActivityLogRepo:         deps.ActivityLogRepo,
		OutboundPolicyRepo:      deps.OutboundPolicyRepo,
		ProviderHealthRepo:      deps.ProviderHealthRepo,
		AssetRegistry:           deps.AssetRegistry,
		Blockchains:             deps.Blockchains,
		PriceOracle:             deps.PriceOracle,
		Notifier:                nil,
		WalletAddressLookupRepo: deps.WalletAddressLookupRepo,
		IdempotencyRepo:         nil,
		TxRescanService:         deps.TxRescanService,
	})
	ready := v1ReadinessOK(checks)
	raw := dealerReadinessRawChecks(checks, checkedAt)
	byName := make(map[string]DealerReadinessCheckView, len(raw))
	for _, check := range raw {
		byName[check.Name] = check
	}

	item := func(name string, label string, owner string, evidenceURL string, blocking bool) DealerReadinessCheckView {
		check, ok := byName[name]
		if !ok {
			check = dealerReadinessCheckView(types.V1ReadinessCheck{
				Name:  name,
				OK:    false,
				Error: "readiness check was not produced",
			}, checkedAt)
		}
		check.Label = label
		check.Owner = owner
		check.EvidenceURL = evidenceURL
		check.EvidenceLabel = readinessEvidenceLabel(evidenceURL)
		check.Blocking = blocking
		check.BlockingLabel = readinessBlockingLabel(blocking)
		return check
	}

	levels := []DealerReadinessLevelView{
		dealerReadinessLevel("controlled-beta", "Controlled beta", "Internal/beta launch can proceed only when core state, backlog, and portal evidence are inspectable.", []DealerReadinessCheckView{
			item("database", "Database access", "Platform Ops", "/admin", true),
			item("migration.strategy", "Migration evidence", "Platform Ops", "/api/v1/common/readiness", true),
			item("portal.jwt_secret", "Portal session secret", "Security", "/admin/security", true),
			item("webhook.delivery_backlog", "Webhook backlog", "Operations", "/admin/webhooks", false),
			item("sweep.job_backlog", "Sweep backlog", "Treasury Ops", "/admin/sweep", false),
			item("reconciliation.drift", "Reconciliation drift", "Finance Ops", "/admin/recover", true),
		}),
		dealerReadinessLevel("real-funds-production", "Real-funds production", "Real money launch requires production migration, signer, metrics, session secret, and chain registry evidence.", []DealerReadinessCheckView{
			item("migration.strategy", "Production migration gate", "Platform Ops", "/api/v1/common/readiness", true),
			item("signer.production", "Production signer policy", "Security", "/admin/security", true),
			item("metrics.access", "Metrics access protection", "SRE", "/metrics", true),
			item("portal.jwt_secret", "Stable portal JWT/session secret", "Security", "/admin/security", true),
			item("chain.registry", "Chain registry completeness", "Chain Ops", "/admin/readiness", true),
		}),
		dealerReadinessLevel("wallet-provider-custody", "Wallet-provider custody", "Custody launch cannot be claimed unless signer readiness and chain/provider health checks are clean.", []DealerReadinessCheckView{
			item("signer.production", "External custody signer readiness", "Security", "/admin/security", true),
			item("chain.registry", "Supported chain registry", "Chain Ops", "/admin/readiness", true),
			item("provider.health.aggregate", "Provider health snapshots", "Chain Ops", "/admin/readiness", true),
		}),
		dealerReadinessLevel("exchange-grade-tracking", "Exchange-grade tracking", "Exchange-grade operations require no dead-letter backlog and visible reconciliation/provider health evidence.", []DealerReadinessCheckView{
			item("webhook.delivery_backlog", "Webhook dead-letter backlog", "Operations", "/admin/webhooks", true),
			item("sweep.job_backlog", "Sweep dead-letter backlog", "Treasury Ops", "/admin/sweep", true),
			item("reconciliation.drift", "Open reconciliation drift", "Finance Ops", "/admin/recover", true),
			item("provider.health.aggregate", "Provider health aggregate", "Chain Ops", "/admin/readiness", true),
		}),
	}
	return ready, checkedAt, levels, raw
}

func dealerReadinessRawChecks(checks []types.V1ReadinessCheck, checkedAt string) []DealerReadinessCheckView {
	views := make([]DealerReadinessCheckView, 0, len(checks)+1)
	providerTotal := 0
	providerOK := 0
	providerFailures := make([]string, 0)
	for _, check := range checks {
		view := dealerReadinessCheckView(check, checkedAt)
		views = append(views, view)
		if strings.HasSuffix(check.Name, ".provider_health") {
			providerTotal++
			if check.OK {
				providerOK++
			} else {
				providerFailures = append(providerFailures, check.Name)
			}
		}
	}
	if providerTotal > 0 {
		aggregate := types.V1ReadinessCheck{
			Name:    "provider.health.aggregate",
			OK:      providerOK == providerTotal,
			Details: fmt.Sprintf("provider_health checks ok=%d total=%d", providerOK, providerTotal),
		}
		if providerOK != providerTotal {
			aggregate.Error = "failing provider checks: " + strings.Join(providerFailures, ", ")
		}
		views = append(views, dealerReadinessCheckView(aggregate, checkedAt))
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Status != views[j].Status {
			return views[i].Status < views[j].Status
		}
		return views[i].Name < views[j].Name
	})
	return views
}

func dealerReadinessCheckView(check types.V1ReadinessCheck, checkedAt string) DealerReadinessCheckView {
	status := "blocked"
	statusClass := "badge-red"
	if check.OK {
		status = "ready"
		statusClass = "badge-green"
	}
	return DealerReadinessCheckView{
		Name:          check.Name,
		Label:         readinessCheckLabel(check.Name),
		Status:        status,
		StatusClass:   statusClass,
		Owner:         readinessCheckOwner(check.Name),
		EvidenceURL:   readinessCheckEvidenceURL(check.Name),
		EvidenceLabel: readinessEvidenceLabel(readinessCheckEvidenceURL(check.Name)),
		Details:       check.Details,
		Error:         check.Error,
		Blocking:      readinessCheckBlocking(check.Name),
		BlockingLabel: readinessBlockingLabel(readinessCheckBlocking(check.Name)),
		LastChecked:   checkedAt,
	}
}

func dealerReadinessLevel(key string, title string, summary string, items []DealerReadinessCheckView) DealerReadinessLevelView {
	ready := true
	for _, item := range items {
		if item.Blocking && item.Status != "ready" {
			ready = false
			break
		}
	}
	status := "ready"
	statusClass := "badge-green"
	if !ready {
		status = "blocked"
		statusClass = "badge-red"
	}
	return DealerReadinessLevelView{
		Key:         key,
		Title:       title,
		Summary:     summary,
		Status:      status,
		StatusClass: statusClass,
		Items:       items,
	}
}

func readinessCheckLabel(name string) string {
	switch name {
	case "database":
		return "Database access"
	case "migration.strategy":
		return "Migration strategy"
	case "signer.production":
		return "Production signer"
	case "metrics.access":
		return "Metrics access"
	case "portal.jwt_secret":
		return "Portal JWT/session secret"
	case "webhook.delivery_backlog":
		return "Webhook backlog"
	case "sweep.job_backlog":
		return "Sweep backlog"
	case "reconciliation.drift":
		return "Reconciliation drift"
	case "chain.registry":
		return "Chain registry"
	case "provider.health.aggregate":
		return "Provider health aggregate"
	default:
		return strings.ReplaceAll(name, ".", " / ")
	}
}

func readinessCheckOwner(name string) string {
	switch {
	case strings.Contains(name, "signer"), strings.Contains(name, "jwt"), strings.Contains(name, "metrics"):
		return "Security"
	case strings.Contains(name, "webhook"), strings.Contains(name, "sweep"), strings.Contains(name, "reconciliation"):
		return "Operations"
	case strings.Contains(name, "chain"), strings.Contains(name, "provider"):
		return "Chain Ops"
	case strings.Contains(name, "migration"), strings.Contains(name, "database"):
		return "Platform Ops"
	default:
		return "Platform Ops"
	}
}

func readinessCheckEvidenceURL(name string) string {
	switch {
	case strings.Contains(name, "webhook"):
		return "/admin/webhooks"
	case strings.Contains(name, "sweep"):
		return "/admin/sweep"
	case strings.Contains(name, "reconciliation"):
		return "/admin/recover"
	case strings.Contains(name, "signer"), strings.Contains(name, "jwt"), strings.Contains(name, "metrics"):
		return "/admin/security"
	default:
		return "/api/v1/common/readiness"
	}
}

func readinessEvidenceLabel(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return "Evidence"
	}
	return url
}

func readinessCheckBlocking(name string) bool {
	switch name {
	case "webhook.delivery_backlog", "sweep.job_backlog":
		return false
	default:
		return true
	}
}

func readinessBlockingLabel(blocking bool) string {
	if blocking {
		return "blocking"
	}
	return "non-blocking"
}

func adminHeaderStatsFor(ctx context.Context, deps DealerDeps) adminHeaderStats {
	now := time.Now()
	adminHeaderStatsCache.Lock()
	if now.Before(adminHeaderStatsCache.expiresAt) {
		stats := adminHeaderStatsCache.stats
		adminHeaderStatsCache.Unlock()
		return stats
	}
	adminHeaderStatsCache.Unlock()

	var merchantTotal, paymentTotal, depositTotal, withdrawalTotal, walletTotal, activityTotal int64
	deps.MerchantService.Repo().DB().WithContext(ctx).Model(&models.Merchant{}).Where("deleted_at IS NULL").Count(&merchantTotal)
	deps.PaymentRepo.CountAll(ctx, &paymentTotal)
	deps.TransactionRepo.DB().WithContext(ctx).Model(&models.Transaction{}).Count(&depositTotal)
	deps.WithdrawalRepo.DB().WithContext(ctx).Model(&models.WithdrawalRequest{}).Count(&withdrawalTotal)
	deps.WalletRepo.DB().WithContext(ctx).Model(&models.Wallet{}).Count(&walletTotal)
	deps.ActivityLogRepo.DB().WithContext(ctx).Model(&models.ActivityLog{}).Count(&activityTotal)

	stats := adminHeaderStats{
		MerchantCount:   int(merchantTotal),
		PaymentCount:    int(paymentTotal),
		DepositCount:    int(depositTotal),
		WithdrawalCount: int(withdrawalTotal),
		WalletCountAll:  int(walletTotal),
		ActivityCount:   int(activityTotal),
	}

	adminHeaderStatsCache.Lock()
	adminHeaderStatsCache.stats = stats
	adminHeaderStatsCache.expiresAt = now.Add(adminHeaderStatsTTL)
	adminHeaderStatsCache.Unlock()
	return stats
}

func (stats adminHeaderStats) applyTo(data *DealerPageData) {
	data.MerchantCount = stats.MerchantCount
	data.PaymentCount = stats.PaymentCount
	data.DepositCount = stats.DepositCount
	data.WithdrawalCount = stats.WithdrawalCount
	data.WalletCountAll = stats.WalletCountAll
	data.ActivityCount = stats.ActivityCount
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

func adminPaymentSessionAsset(registry *asset.Registry, session models.PaymentSession) (asset.Asset, error) {
	if registry == nil {
		return nil, errors.New("asset registry hazır değil")
	}
	if session.SelectedChainID == nil {
		return nil, errors.New("Checkout için önce asset seçilmeli.")
	}
	chainID := *session.SelectedChainID
	candidates := []string{}
	if session.SelectedToken != nil {
		candidates = append(candidates, strings.TrimSpace(*session.SelectedToken))
	}
	candidates = append(candidates, strings.TrimSpace(session.SelectedSymbol))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if selected, ok := registry.Get(chainID, candidate); ok {
			return selected, nil
		}
		if selected, ok := registry.GetBySymbol(chainID, candidate); ok {
			return selected, nil
		}
	}
	if native, ok := registry.GetNative(chainID); ok && strings.EqualFold(native.GetSymbol(), session.SelectedSymbol) {
		return native, nil
	}
	return nil, errors.New("Checkout asset registry içinde bulunamadı.")
}

func firstNonEmptyString(values ...string) string {
	return firstNonEmpty(values...)
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
		token := strings.TrimSpace(item.GetIdentifier())
		tokenLabel := "native"
		identifier := token
		identifierTag := "Native symbol"
		if !item.IsNative() {
			tokenLabel = shortText(token, 8, 6)
			if tokenAddress := strings.TrimSpace(asset.TokenAddress(item)); tokenAddress != "" {
				identifier = tokenAddress
				identifierTag = "Contract address"
			} else if mintAddress := strings.TrimSpace(asset.MintAddress(item)); mintAddress != "" {
				identifier = mintAddress
				identifierTag = "Mint address"
			} else {
				identifierTag = "Identifier"
			}
		}
		label := fmt.Sprintf("%s / %s / %s / %d decimals", chainName, item.GetSymbol(), tokenLabel, item.GetDecimals())
		options = append(options, DealerAssetOption{
			Value:         fmt.Sprintf("%d|%s", item.GetChainID(), item.GetIdentifier()),
			AssetKey:      dealerAssetKey(item.GetChainID(), item.GetIdentifier()),
			Label:         label,
			ChainID:       fmt.Sprintf("%d", item.GetChainID()),
			Chain:         chainName,
			ChainLogoURL:  asset.ChainLogoURL(item.GetChainID()),
			Symbol:        item.GetSymbol(),
			Name:          item.GetName(),
			Token:         token,
			DisplayToken:  tokenLabel,
			Identifier:    identifier,
			IdentifierTag: identifierTag,
			Type:          asset.AssetTypeName(item.GetType()),
			TypeLabel:     adminAssetTypeLabel(item),
			Decimals:      item.GetDecimals(),
			LogoURL:       registry.LogoURL(item.GetSymbol()),
			IsNative:      item.IsNative(),
		})
	}
	return options
}

func adminAssetTypeLabel(item asset.Asset) string {
	if item == nil {
		return "unknown"
	}
	if item.IsNative() {
		return "Native"
	}
	switch item.GetType() {
	case asset.AssetERC20:
		return "ERC-20"
	case asset.AssetTRC20:
		return "TRC-20"
	case asset.AssetSPL:
		return "SPL"
	case asset.AssetUTXO:
		return "UTXO"
	default:
		return asset.AssetTypeName(item.GetType())
	}
}

func dealerAdminTestDomainOptions(ctx context.Context, deps DealerDeps, limit int) []DealerTestDomainOption {
	if deps.MerchantService == nil || deps.DomainService == nil || deps.MerchantService.Repo() == nil {
		return nil
	}
	merchants, err := deps.MerchantService.Repo().List(ctx, limit)
	if err != nil {
		return nil
	}
	options := make([]DealerTestDomainOption, 0)
	for _, merchant := range merchants {
		domains, err := deps.DomainService.ListByMerchant(ctx, merchant.ID)
		if err != nil {
			continue
		}
		merchantLabel := strings.TrimSpace(merchant.Name)
		if merchantLabel == "" {
			merchantLabel = strings.TrimSpace(merchant.Email)
		}
		if merchantLabel == "" {
			merchantLabel = shortText(merchant.ID.String(), 8, 6)
		}
		for _, domain := range domains {
			domainURL := strings.TrimSpace(domain.DomainURL)
			if domainURL == "" || domainURL == "_reserve_" {
				continue
			}
			options = append(options, DealerTestDomainOption{
				ID:         domain.ID.String(),
				MerchantID: merchant.ID.String(),
				Merchant:   merchantLabel,
				Domain:     domainURL,
				Label:      merchantLabel + " / " + domainURL,
			})
		}
	}
	sort.SliceStable(options, func(i, j int) bool {
		return options[i].Label < options[j].Label
	})
	return options
}

func dealerTestPaymentViews(c fiber.Ctx, payments []models.PaymentSession) []DealerTestPaymentView {
	views := make([]DealerTestPaymentView, 0, len(payments))
	base := baseURL(c)
	for _, session := range payments {
		if session.SelectedChainID == nil {
			continue
		}
		linkType := models.NormalizePaymentLinkType(session.LinkType)
		linkTypeLabel := "Payment"
		if models.IsDonationLinkType(linkType) {
			linkTypeLabel = "Donation"
		}
		statusLabel, statusClass := paymentStatusPresentation(session.Status)
		merchant := strings.TrimSpace(session.Merchant.Name)
		if merchant == "" {
			merchant = shortText(session.MerchantID.String(), 8, 6)
		}
		domain := strings.TrimSpace(session.Domain.DomainURL)
		if domain == "" {
			domain = shortText(session.DomainID.String(), 8, 6)
		}
		amountDisplay := "serbest tutar"
		testAmount := "1"
		if positiveTokenAmountRaw(session.ExpectedAmountRaw) {
			amountDisplay = formatTokenAmount(session.ExpectedAmountRaw, session.SelectedDecimals) + " " + session.SelectedSymbol
			testAmount = formatTokenAmount(session.ExpectedAmountRaw, session.SelectedDecimals)
		} else if !models.IsDonationLinkType(linkType) {
			amountDisplay = strings.TrimSpace(session.Amount + " " + session.Currency)
		}
		assetLabel := chainLabel(*session.SelectedChainID) + " / " + emptyDash(session.SelectedSymbol)
		if session.SelectedToken != nil && strings.TrimSpace(*session.SelectedToken) != "" {
			assetLabel += " / " + shortText(*session.SelectedToken, 8, 6)
		}
		views = append(views, DealerTestPaymentView{
			ID:             session.ID.String(),
			ShortID:        shortText(session.ID.String(), 8, 6),
			OrderID:        session.OrderID,
			Merchant:       merchant,
			Domain:         domain,
			LinkType:       linkType,
			LinkTypeLabel:  linkTypeLabel,
			WalletID:       session.WalletID.String(),
			ShortWalletID:  shortText(session.WalletID.String(), 8, 6),
			AssetValue:     adminPaymentSessionAssetValue(session),
			AssetLabel:     assetLabel,
			AmountDisplay:  amountDisplay,
			TestAmount:     testAmount,
			DepositAddress: shortText(session.DepositAddress, 12, 8),
			CheckoutURL:    base + checkoutLocalizedURL(session.SessionToken, "/pay", "tr", ""),
			Status:         session.Status,
			StatusLabel:    statusLabel,
			StatusClass:    statusClass,
			CreatedAt:      formatPanelTime(session.CreatedAt),
		})
	}
	return views
}

func adminPaymentSessionAssetValue(session models.PaymentSession) string {
	if session.SelectedChainID == nil {
		return ""
	}
	identifier := strings.TrimSpace(session.SelectedSymbol)
	if session.SelectedToken != nil && strings.TrimSpace(*session.SelectedToken) != "" {
		identifier = strings.TrimSpace(*session.SelectedToken)
	}
	if identifier == "" {
		return ""
	}
	return fmt.Sprintf("%d|%s", *session.SelectedChainID, identifier)
}

func dealerRecoverChainOptions(registry *asset.Registry) []DealerRecoverChainOption {
	if registry == nil {
		return nil
	}
	assets := registry.ListAll()
	byChain := make(map[constants.ChainID]*DealerRecoverChainOption)
	order := make([]constants.ChainID, 0)
	for _, item := range assets {
		if item == nil {
			continue
		}
		chainID := item.GetChainID()
		option, ok := byChain[chainID]
		if !ok {
			option = &DealerRecoverChainOption{
				ChainID: fmt.Sprintf("%d", chainID),
				Chain:   constants.ChainName(chainID),
				LogoURL: asset.ChainLogoURL(chainID),
			}
			byChain[chainID] = option
			order = append(order, chainID)
		}
		option.AssetCount++
	}
	sort.SliceStable(order, func(i, j int) bool {
		return byChain[order[i]].Chain < byChain[order[j]].Chain
	})

	options := make([]DealerRecoverChainOption, 0, len(order))
	for _, chainID := range order {
		options = append(options, *byChain[chainID])
	}
	return options
}

func dealerRecoverChainFilter(registry *asset.Registry, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, option := range dealerRecoverChainOptions(registry) {
		if option.ChainID == value {
			return value
		}
	}
	return ""
}

func dealerAssetKey(chainID constants.ChainID, identifier string) string {
	return fmt.Sprintf("%d|%s", chainID, strings.ToLower(strings.TrimSpace(identifier)))
}

func dealerSweepStatusViews(counts map[string]int64) []DealerSweepStatusView {
	statuses := []struct {
		status string
		label  string
	}{
		{models.SweepJobStatusPending, "Pending"},
		{models.SweepJobStatusProcessing, "Processing"},
		{models.SweepJobStatusFailed, "Failed"},
		{models.SweepJobStatusDeadLetter, "Dead letter"},
		{models.SweepJobStatusSucceeded, "Succeeded"},
	}
	out := make([]DealerSweepStatusView, 0, len(statuses))
	for _, item := range statuses {
		out = append(out, DealerSweepStatusView{
			Status: item.status,
			Label:  item.label,
			Count:  counts[item.status],
		})
	}
	return out
}

func dealerSweepJobViews(rows []models.SweepJob) []DealerSweepJobView {
	views := make([]DealerSweepJobView, 0, len(rows))
	for _, row := range rows {
		token := ""
		assetLabel := "native"
		if row.Token != nil && strings.TrimSpace(*row.Token) != "" {
			token = strings.TrimSpace(*row.Token)
			assetLabel = shortText(token, 8, 6)
		}
		nextRunAt := ""
		if row.NextRunAt != nil {
			nextRunAt = formatPanelTime(*row.NextRunAt)
		}
		lockedUntil := ""
		if row.LockedUntil != nil {
			lockedUntil = formatPanelTime(*row.LockedUntil)
		}
		views = append(views, DealerSweepJobView{
			ID:                    row.ID.String(),
			ShortID:               shortText(row.ID.String(), 8, 6),
			TransactionUniqueHash: row.TransactionUniqueHash,
			TransactionHash:       row.TransactionHash,
			WalletID:              row.WalletID.String(),
			ShortWalletID:         shortText(row.WalletID.String(), 8, 6),
			MerchantID:            row.MerchantID.String(),
			ShortMerchantID:       shortText(row.MerchantID.String(), 8, 6),
			Chain:                 constants.ChainName(row.ChainID),
			ChainLogoURL:          asset.ChainLogoURL(row.ChainID),
			Token:                 token,
			Asset:                 assetLabel,
			Status:                row.Status,
			StatusLabel:           strings.ReplaceAll(row.Status, "_", " "),
			Attempts:              row.Attempts,
			MaxAttempts:           row.MaxAttempts,
			PrefundAttempts:       row.PrefundAttempts,
			PrefundMaxAttempts:    row.PrefundMaxAttempts,
			NextRunAt:             nextRunAt,
			LockedUntil:           lockedUntil,
			SweepTxHash:           row.SweepTxHash,
			LastError:             sweepJobAdminError(row.LastError),
			OperatorAction:        row.OperatorAction,
			CreatedAt:             formatPanelTime(row.CreatedAt),
			UpdatedAt:             formatPanelTime(row.UpdatedAt),
		})
	}
	return views
}

func sweepJobAdminError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return webhooksvc.SanitizeDeliveryText(value)
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
	matchResult, err := deps.PaymentRepo.MatchFinalizedTransaction(ctx, txModel)
	if err != nil || matchResult == nil || matchResult.Session == nil {
		return false, err
	}
	session := matchResult.Session
	if deps.MoneyEventOutboxRepo != nil {
		ownedByOutbox, err := deps.MoneyEventOutboxRepo.HasAggregateEvent(ctx, "payment", session.ID.String(), session.WebhookEvent)
		if err != nil {
			return false, err
		}
		if ownedByOutbox {
			return true, nil
		}
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
	if deps.MoneyEventOutboxRepo != nil {
		event, findErr := deps.MoneyEventOutboxRepo.FindByEventID(ctx, payload.EventID)
		switch {
		case findErr == nil:
			// A canonical outbox row is durable success. Delivery creation belongs to
			// the ordered relay so a later event cannot leapfrog this one after a crash.
			return event.ID
		case !errors.Is(findErr, gorm.ErrRecordNotFound):
			return uuid.Nil
		}
		ownedByOutbox, ownershipErr := deps.MoneyEventOutboxRepo.HasAggregate(ctx, payload.EntityType, payload.EntityID)
		if ownershipErr != nil {
			log.Printf("dealer lifecycle canonical aggregate lookup error event=%s id=%s aggregate=%s/%s: %v", payload.EventType, payload.EventID, payload.EntityType, payload.EntityID, ownershipErr)
			return uuid.Nil
		}
		if ownedByOutbox {
			// Do not let a legacy repair bypass a predecessor already sequenced in
			// the canonical outbox. The missing event needs reconciliation instead.
			log.Printf("dealer lifecycle canonical event missing; direct enqueue blocked for reconciliation event=%s id=%s aggregate=%s/%s", payload.EventType, payload.EventID, payload.EntityType, payload.EntityID)
			return uuid.Nil
		}
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

func openDealerOutboundLifecycleReconciliation(ctx context.Context, deps DealerDeps, chain string, merchantID *uuid.UUID, domainID *uuid.UUID, resourceType string, resourceID string, lifecycleStatus string, reason string, errText string, txHash string) {
	if deps.ReconciliationRepo == nil {
		return
	}
	evidence := map[string]any{
		"chain":   strings.TrimSpace(chain),
		"tx_hash": strings.TrimSpace(txHash),
	}
	if strings.TrimSpace(errText) != "" {
		evidence["error"] = strings.TrimSpace(errText)
	}
	_, _, err := deps.ReconciliationRepo.OpenStuckLifecycleJob(
		ctx,
		chainSlugToID(chain),
		merchantID,
		domainID,
		resourceType,
		resourceID,
		lifecycleStatus,
		reason,
		evidence,
	)
	if err != nil {
		log.Printf("outbound lifecycle reconciliation open error: %v\n", err)
	}
}

func markAdminWebhookDeliveryAttempt(ctx context.Context, deps DealerDeps, deliveryID, leaseToken uuid.UUID, delivered bool, lastErr error) {
	if deps.WebhookDeliveryRepo == nil || deliveryID == uuid.Nil || leaseToken == uuid.Nil {
		return
	}
	if err := deps.WebhookDeliveryRepo.MarkAttempt(ctx, deliveryID, leaseToken, delivered, lastErr); err != nil {
		log.Printf("admin webhook delivery attempt update delivery_id=%s error=%v", deliveryID, err)
	}
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
			EventVersion:       emptyDefault(row.EventVersion, "v1"),
			MerchantID:         row.MerchantID.String(),
			DomainID:           row.DomainID.String(),
			ResourceType:       emptyDash(row.ResourceType),
			ResourceID:         emptyDash(row.ResourceID),
			Sequence:           row.Sequence,
			IdempotencyKey:     emptyDash(row.IdempotencyKey),
			TargetURL:          row.TargetURL,
			Status:             row.Status,
			Attempts:           row.Attempts,
			LastError:          webhooksvc.SanitizeDeliveryText(row.LastError),
			FailureCategory:    row.FailureCategory,
			NextRetryAt:        nextRetryAt,
			NextAction:         webhookDeliveryNextAction(row),
			PayloadPreview:     dealerPreviewText(row.PayloadJSON, "Payload preview unavailable"),
			LatencyEvidence:    "Latency evidence unavailable",
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

func dealerReconciliationJobViews(rows []models.ReconciliationJob) []DealerReconciliationJobView {
	views := make([]DealerReconciliationJobView, 0, len(rows))
	for _, row := range rows {
		resolvedAt := ""
		if row.ResolvedAt != nil {
			resolvedAt = formatPanelTime(*row.ResolvedAt)
		}
		nextRunAt := ""
		if row.NextRunAt != nil {
			nextRunAt = formatPanelTime(*row.NextRunAt)
		}
		scope := reconciliationScopeLabel(row)
		evidence := dealerPreviewText(row.EvidenceJSON, "Evidence unavailable")
		views = append(views, DealerReconciliationJobView{
			ID:                row.ID.String(),
			ShortID:           shortText(row.ID.String(), 8, 6),
			Reason:            emptyDefault(row.Reason, "unknown"),
			Status:            emptyDefault(row.Status, "unknown"),
			StatusClass:       reconciliationStatusClass(row.Status),
			Severity:          reconciliationSeverity(row),
			Owner:             "Finance Ops",
			Chain:             chainLabel(row.ChainID),
			Scope:             scope,
			ResourceType:      emptyDash(row.ResourceType),
			ResourceID:        emptyDash(row.ResourceID),
			AffectedResources: dealerPreviewText(row.AffectedResourceIDsJSON, "Affected resources unavailable"),
			EvidencePreview:   evidence,
			Outcome:           emptyDash(row.Outcome),
			Error:             dealerPreviewText(row.Error, "No active error"),
			NextAction:        reconciliationNextAction(row),
			OpenedAt:          formatPanelTime(row.CreatedAt),
			UpdatedAt:         formatPanelTime(row.UpdatedAt),
			ResolvedAt:        emptyDash(resolvedAt),
			NextRunAt:         emptyDash(nextRunAt),
			Attempts:          row.Attempts,
			ChainEvidence:     reconciliationEvidenceAvailability(row, "chain"),
			LedgerEvidence:    reconciliationEvidenceAvailability(row, "ledger"),
			LifecycleEvidence: reconciliationEvidenceAvailability(row, "lifecycle"),
			WebhookEvidence:   reconciliationEvidenceAvailability(row, "webhook"),
			BroadcastEvidence: reconciliationEvidenceAvailability(row, "broadcast"),
			AuditTimeline:     reconciliationAuditTimeline(row),
		})
	}
	return views
}

func dealerPreviewText(value string, empty string) string {
	value = webhooksvc.SanitizeDeliveryText(strings.TrimSpace(value))
	if value == "" || value == "{}" || value == "[]" {
		return empty
	}
	if len(value) > 240 {
		return value[:240] + "..."
	}
	return value
}

func reconciliationScopeLabel(row models.ReconciliationJob) string {
	parts := []string{}
	if row.ScopeKey != "" {
		parts = append(parts, "scope="+row.ScopeKey)
	}
	if row.MerchantID != nil {
		parts = append(parts, "merchant="+shortText(row.MerchantID.String(), 8, 6))
	}
	if row.DomainID != nil {
		parts = append(parts, "domain="+shortText(row.DomainID.String(), 8, 6))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("blocks=%d-%d", row.FromBlock, row.ToBlock))
	}
	return strings.Join(parts, " · ")
}

func reconciliationStatusClass(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.ReconciliationStatusResolved:
		return "badge-green"
	case models.ReconciliationStatusFailed, models.ReconciliationStatusNeedsOperatorAction:
		return "badge-red"
	case models.ReconciliationStatusRetryScheduled, models.ReconciliationStatusProcessing:
		return "badge-amber"
	default:
		return "badge-violet"
	}
}

func reconciliationSeverity(row models.ReconciliationJob) string {
	switch row.Status {
	case models.ReconciliationStatusFailed, models.ReconciliationStatusNeedsOperatorAction:
		return "blocking"
	case models.ReconciliationStatusRetryScheduled, models.ReconciliationStatusProcessing:
		return "watch"
	default:
		return "open"
	}
}

func reconciliationNextAction(row models.ReconciliationJob) string {
	switch row.Status {
	case models.ReconciliationStatusResolved:
		return "No action"
	case models.ReconciliationStatusRetryScheduled:
		return "Wait for scheduled retry"
	case models.ReconciliationStatusNeedsOperatorAction:
		return "Operator review required"
	case models.ReconciliationStatusFailed:
		return "Escalate with evidence"
	default:
		return "Inspect evidence and resolve, defer, or escalate"
	}
}

func reconciliationEvidenceAvailability(row models.ReconciliationJob, group string) string {
	evidence := strings.ToLower(row.EvidenceJSON)
	switch group {
	case "chain":
		if row.ChainID != 0 || strings.Contains(evidence, "chain") || strings.Contains(evidence, "block") {
			return "available"
		}
	case "ledger":
		if strings.Contains(evidence, "ledger") || strings.Contains(row.Reason, "ledger") {
			return "available"
		}
	case "lifecycle":
		if row.ResourceType != "" || strings.Contains(evidence, "lifecycle") {
			return "available"
		}
	case "webhook":
		if strings.Contains(evidence, "webhook") || strings.Contains(row.ResourceType, "webhook") {
			return "available"
		}
	case "broadcast":
		if strings.Contains(evidence, "broadcast") || strings.Contains(evidence, "outbound") {
			return "available"
		}
	}
	return "evidence unavailable"
}

func reconciliationAuditTimeline(row models.ReconciliationJob) string {
	parts := []string{"opened " + formatPanelTime(row.CreatedAt)}
	if row.UpdatedAt.After(row.CreatedAt) {
		parts = append(parts, "updated "+formatPanelTime(row.UpdatedAt))
	}
	if row.ResolvedAt != nil {
		parts = append(parts, "resolved "+formatPanelTime(*row.ResolvedAt))
	}
	if row.Attempts > 0 {
		parts = append(parts, fmt.Sprintf("%d attempts", row.Attempts))
	}
	return strings.Join(parts, " · ")
}

func dealerProviderHealthViews(rows []models.ProviderHealthSnapshot) []DealerProviderHealthView {
	views := make([]DealerProviderHealthView, 0, len(rows))
	for _, row := range rows {
		views = append(views, DealerProviderHealthView{
			Chain:             emptyDefault(row.ChainName, constants.ChainName(row.ChainID)),
			ChainID:           fmt.Sprintf("%d", row.ChainID),
			ProviderLabel:     emptyDefault(row.ProviderLabel, "provider"),
			ProviderHash:      shortText(row.ProviderURLHash, 12, 8),
			Status:            emptyDefault(row.Status, models.ProviderHealthStatusUnknown),
			StatusClass:       providerHealthStatusClass(row.Status),
			Reachable:         row.Reachable,
			LatestHeight:      row.LatestHeight,
			HeadHash:          shortText(row.HeadHash, 12, 8),
			LatencyMS:         row.ResponseLatencyMS,
			Lag:               row.LagFromReference,
			StaleIndicator:    providerHealthStaleIndicator(row),
			Selected:          row.Selected,
			FailoverReason:    emptyDefault(row.FailoverReason, "primary or fallback policy not recorded"),
			ErrorCategory:     emptyDash(row.ErrorCategory),
			ErrorDetail:       dealerPreviewText(v1RedactReadinessText(row.ErrorDetail), "No provider error"),
			FailureCount:      row.ConsecutiveFailures,
			CheckedAt:         formatPanelTime(row.CheckedAt),
			FallbackPolicy:    providerHealthFallbackPolicy(row),
			ReadinessEvidence: "/api/v1/common/readiness",
		})
	}
	return views
}

func dealerNetworkOperationalStateViews(rows []models.NetworkOperationalState) []DealerNetworkOperationalStateView {
	views := make([]DealerNetworkOperationalStateView, 0, len(rows))
	for _, row := range rows {
		mode := models.NormalizeNetworkOperationalMode(row.Mode)
		modeLabel, modeClass := networkOperationalModePresentation(mode)
		views = append(views, DealerNetworkOperationalStateView{
			Chain:             chainLabel(row.ChainID),
			ChainID:           strconv.FormatInt(int64(row.ChainID), 10),
			ChainSlug:         constants.ChainName(row.ChainID),
			ChainLogoURL:      asset.ChainLogoURL(row.ChainID),
			Mode:              string(mode),
			ModeLabel:         modeLabel,
			ModeClass:         modeClass,
			Reason:            strings.TrimSpace(row.Reason),
			UpdatedBy:         emptyDash(row.UpdatedBy),
			UpdatedAt:         formatPanelTime(row.UpdatedAt),
			BlocksDeposits:    row.BlocksDeposits(),
			BlocksWithdrawals: row.BlocksWithdrawals(),
			Testnet:           constants.IsTestnet(row.ChainID),
		})
	}
	return views
}

func networkOperationalModePresentation(mode models.NetworkOperationalMode) (string, string) {
	switch models.NormalizeNetworkOperationalMode(mode) {
	case models.NetworkOperationalModeActive:
		return "Aktif", "badge-green"
	case models.NetworkOperationalModeDepositsOff:
		return "Deposit kapalı", "badge-amber"
	case models.NetworkOperationalModeWithdrawalsOff:
		return "Çekim kapalı", "badge-amber"
	case models.NetworkOperationalModeMaintenance:
		return "Bakım", "badge-red"
	default:
		return "Bilinmiyor", "badge-violet"
	}
}

func providerHealthStatusClass(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.ProviderHealthStatusHealthy:
		return "badge-green"
	case models.ProviderHealthStatusDegraded:
		return "badge-amber"
	case models.ProviderHealthStatusUnhealthy:
		return "badge-red"
	default:
		return "badge-violet"
	}
}

func providerHealthStaleIndicator(row models.ProviderHealthSnapshot) string {
	if row.LagFromReference > 0 {
		return fmt.Sprintf("lag %d blocks", row.LagFromReference)
	}
	if row.Status == models.ProviderHealthStatusUnhealthy || row.Status == models.ProviderHealthStatusUnknown {
		return "stale or unavailable head"
	}
	return "head current"
}

func providerHealthFallbackPolicy(row models.ProviderHealthSnapshot) string {
	if row.Selected {
		return "selected provider"
	}
	if strings.TrimSpace(row.FailoverReason) != "" {
		return "fallback candidate: " + row.FailoverReason
	}
	return "fallback candidate"
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
	return dealerDomainViewsWithDeliveries(domains, nil, false)
}

func dealerDomainViewsWithDeliveries(domains []models.Domain, latest map[uuid.UUID]models.WebhookDelivery, deliveryEvidenceUnavailable bool) []DealerDomainView {
	views := make([]DealerDomainView, 0, len(domains))
	for _, domain := range domains {
		keyID := strings.TrimSpace(domain.KeyID)
		if keyID == "" {
			if extracted, err := helpers.ExtractKeyID(domain.APIKey); err == nil {
				keyID = extracted
			}
		}
		if keyID == "" {
			keyID = "unknown"
		}
		scopes := strings.TrimSpace(domain.APIScopes)
		if scopes == "" {
			scopes = models.DefaultDomainAPIScopesCSV()
		}
		secretStatus := "Secret active; raw value is not displayed after creation."
		rotatedAt := "Never rotated"
		if domain.APISecretLastRotatedAt != nil {
			rotatedAt = formatPanelTime(*domain.APISecretLastRotatedAt)
			secretStatus = "Rotated; previous secret revoked immediately."
		}
		webhookStatus := "no_delivery"
		webhookEvent := "Test webhook veya ilk para olayı bekleniyor"
		webhookAt := "Henüz yok"
		var webhookAttempts uint
		if deliveryEvidenceUnavailable {
			webhookStatus = "evidence_unavailable"
			webhookEvent = "Delivery evidence okunamadı"
		} else if latest != nil {
			if row, ok := latest[domain.ID]; ok {
				webhookStatus = emptyDefault(row.Status, "unknown")
				webhookEvent = emptyDefault(row.EventType, "unknown")
				webhookAt = formatPanelTime(row.UpdatedAt)
				webhookAttempts = row.Attempts
			}
		}
		views = append(views, DealerDomainView{
			ID:                  domain.ID.String(),
			DomainURL:           domain.DomainURL,
			NotificationMode:    domain.EffectiveNotificationMode(),
			WebhookURL:          domain.WebhookURL,
			NATSURL:             domain.NATSURL,
			NATSSubject:         domain.EffectiveNATSSubject(),
			WebhookSigningMode:  "HMAC-SHA256 over timestamp + raw_body",
			WebhookCatalogURL:   "/docs/money-event-catalog.md",
			WebhookDocsURL:      "/docs/integration-guide.md#webhooks",
			WebhookLastStatus:   webhookStatus,
			WebhookLastEvent:    webhookEvent,
			WebhookLastAt:       webhookAt,
			WebhookLastAttempts: webhookAttempts,
			KeyID:               keyID,
			APIKey:              domain.APIKey,
			APIScopes:           scopes,
			APISecretStatus:     secretStatus,
			APISecretRotatedAt:  rotatedAt,
			RotateConfirm:       dealerRotateAPISecretConfirmation(domain.ID.String()),
			SigningExample:      dealerSigningExample(domain),
			IdempotencyExample:  "Idempotency-Key: payment-" + shortText(domain.ID.String(), 8, 0),
			HDAccountID:         domain.HDAccountID,
			CreatedAt:           formatPanelTime(domain.CreatedAt),
		})
		if domain.UsesNATS() {
			views[len(views)-1].WebhookSigningMode = notificationSigningMode(domain)
		}
	}
	return views
}

func notificationSigningMode(domain models.Domain) string {
	if domain.UsesNATS() {
		return "NATS JSON publish"
	}
	return "HMAC-SHA256 over timestamp + raw_body"
}

func dealerLatestWebhookDeliveries(ctx context.Context, deps DealerDeps, merchantID uuid.UUID, domains []models.Domain) (map[uuid.UUID]models.WebhookDelivery, error) {
	if deps.WebhookDeliveryRepo == nil || merchantID == uuid.Nil || len(domains) == 0 {
		return nil, nil
	}
	domainIDs := make([]uuid.UUID, 0, len(domains))
	for _, domain := range domains {
		if domain.ID != uuid.Nil && domain.MerchantID == merchantID {
			domainIDs = append(domainIDs, domain.ID)
		}
	}
	return deps.WebhookDeliveryRepo.LatestByMerchantDomains(ctx, merchantID, domainIDs)
}

func dealerRotateAPISecretConfirmation(domainID string) string {
	return "rotate:" + strings.TrimSpace(domainID)
}

func dealerRotateAPISecretConfirmed(c fiber.Ctx, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	for _, value := range []string{
		c.FormValue("confirm_rotate"),
		c.FormValue("confirmation"),
		c.Get("X-Gateway-Rotate-Confirm"),
	} {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func dealerSigningExample(domain models.Domain) string {
	apiKey := strings.TrimSpace(domain.APIKey)
	if apiKey == "" {
		apiKey = "gw_live_key"
	}
	return strings.Join([]string{
		`curl -X POST "$GATEWAY_URL/api/v1/payment/create" \`,
		`  -H "X-API-Key: ` + apiKey + `" \`,
		`  -H "X-API-Secret: $GATEWAY_API_SECRET" \`,
		`  -H "X-Gateway-Timestamp: $(date +%s)" \`,
		`  -H "X-Gateway-Signature: sha256=$SIGNATURE" \`,
		`  -H "Idempotency-Key: payment-$(uuidgen)" \`,
		`  -d '{"domain_id":"` + domain.ID.String() + `","amount":"1000","currency":"USDT"}'`,
	}, "\n")
}

func dealerProductViews(c fiber.Ctx, products []models.Product) []DealerProductView {
	views := make([]DealerProductView, 0, len(products))
	for _, product := range products {
		logoText := "?"
		if product.Name != "" {
			runes := []rune(product.Name)
			logoText = strings.ToUpper(string(runes[0]))
		}
		linkType := models.NormalizePaymentLinkType(product.LinkType)
		linkTypeLabel := "Sabit tutar"
		amountDisplay := strings.TrimSpace(product.Amount + " " + product.Currency)
		if models.IsDonationLinkType(linkType) {
			linkTypeLabel = "Donation"
			amountDisplay = "Serbest tutar"
		}
		views = append(views, DealerProductView{
			ID:                product.ID.String(),
			Name:              product.Name,
			Description:       product.Description,
			LinkType:          linkType,
			LinkTypeLabel:     linkTypeLabel,
			Amount:            product.Amount,
			Currency:          product.Currency,
			AmountDisplay:     amountDisplay,
			Language:          strings.ToUpper(product.Language),
			Merchant:          product.Merchant.Name,
			DomainID:          product.DomainID.String(),
			Domain:            product.Domain.DomainURL,
			PaymentURL:        baseURL(c) + "/payment-links/" + product.LinkToken,
			LogoURL:           product.LogoURL,
			LogoText:          logoText,
			SuccessURL:        product.SuccessURL,
			CancelURL:         product.CancelURL,
			X402Enabled:       product.X402Enabled,
			DefaultAssetValue: productDefaultAssetValue(product),
			CreatedAt:         formatPanelTime(product.CreatedAt),
		})
	}
	return views
}

func productDefaultAssetValue(product models.Product) string {
	if product.DefaultChainID == nil || strings.TrimSpace(product.DefaultSymbol) == "" {
		return ""
	}
	identifier := strings.TrimSpace(product.DefaultSymbol)
	if product.DefaultToken != nil && strings.TrimSpace(*product.DefaultToken) != "" {
		identifier = strings.TrimSpace(*product.DefaultToken)
	}
	return fmt.Sprintf("%d|%s", *product.DefaultChainID, identifier)
}

func dealerPaymentViews(c fiber.Ctx, payments []models.PaymentSession) []DealerPaymentView {
	views := make([]DealerPaymentView, 0, len(payments))
	for _, payment := range payments {
		selectedChain := "-"
		chainLogoURL := ""
		if payment.SelectedChainID != nil {
			selectedChain = chainLabel(*payment.SelectedChainID)
			chainLogoURL = asset.ChainLogoURL(*payment.SelectedChainID)
		}
		base := baseURL(c)
		checkoutURL := base + "/checkout/" + payment.SessionToken
		invoiceURL := base + "/invoice/" + payment.SessionToken
		linkType := models.NormalizePaymentLinkType(payment.LinkType)
		amountDisplay := strings.TrimSpace(payment.Amount + " " + payment.Currency)
		if models.IsDonationLinkType(linkType) {
			amountDisplay = "Serbest tutar"
			if strings.TrimSpace(payment.MatchedAmountRaw) != "" {
				amountDisplay = strings.TrimSpace(formatTokenAmount(payment.MatchedAmountRaw, payment.SelectedDecimals) + " " + payment.SelectedSymbol)
			}
		}
		merchant := payment.Merchant.Name
		if strings.TrimSpace(merchant) == "" {
			merchant = shortText(payment.MerchantID.String(), 8, 6)
		}
		domain := payment.Domain.DomainURL
		if strings.TrimSpace(domain) == "" {
			domain = shortText(payment.DomainID.String(), 8, 6)
		}
		statusLabel, statusClass := paymentStatusPresentation(payment.Status)
		webhookStatus := paymentWebhookStatus(payment)
		txHash := ""
		if payment.TxHash != nil {
			txHash = strings.TrimSpace(*payment.TxHash)
		}
		txHashShort := ""
		if txHash != "" {
			txHashShort = shortText(txHash, 10, 8)
		}
		productID := emptyDash(payment.ProductID)
		userID := emptyDash(payment.UserID)
		selectedAsset := emptyDash(payment.SelectedSymbol)
		searchText := strings.Join([]string{
			payment.ID.String(),
			payment.OrderID,
			productID,
			userID,
			merchant,
			domain,
			linkType,
			payment.Amount,
			payment.Currency,
			amountDisplay,
			payment.Status,
			statusLabel,
			webhookStatus,
			payment.SelectedSymbol,
			selectedChain,
			payment.DepositAddress,
			txHash,
			checkoutURL,
			invoiceURL,
		}, " ")
		views = append(views, DealerPaymentView{
			ID:                 payment.ID.String(),
			ShortID:            shortText(payment.ID.String(), 8, 6),
			OrderID:            payment.OrderID,
			ProductID:          productID,
			UserID:             userID,
			Merchant:           merchant,
			Domain:             domain,
			LinkType:           linkType,
			Amount:             payment.Amount,
			AmountSort:         paymentAmountSortValue(payment.Amount),
			Currency:           payment.Currency,
			AmountDisplay:      emptyDash(amountDisplay),
			Status:             payment.Status,
			StatusLabel:        statusLabel,
			StatusClass:        statusClass,
			WebhookStatus:      webhookStatus,
			WebhookAttempts:    payment.WebhookAttempts,
			CheckoutURL:        checkoutURL,
			InvoiceURL:         invoiceURL,
			SelectedAsset:      selectedAsset,
			SelectedChain:      selectedChain,
			ChainLogoURL:       chainLogoURL,
			DepositAddress:     shortText(payment.DepositAddress, 12, 8),
			DepositAddressFull: payment.DepositAddress,
			TxHash:             txHash,
			TxHashShort:        txHashShort,
			CreatedAt:          formatPanelTime(payment.CreatedAt),
			CreatedSort:        strconv.FormatInt(payment.CreatedAt.Unix(), 10),
			SearchText:         searchText,
		})
	}
	return views
}

func paymentStatusPresentation(status string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.PaymentStatusPaid:
		return "Ödendi", "is-success"
	case models.PaymentStatusAwaitingPayment:
		return "Ödeme bekliyor", "is-info"
	case models.PaymentStatusPending:
		return "Asset bekliyor", "is-warning"
	case models.PaymentStatusPartialPaid:
		return "Kısmi ödeme", "is-warning"
	case models.PaymentStatusUnderpaid:
		return "Eksik ödeme", "is-warning"
	case models.PaymentStatusOverpaid:
		return "Fazla ödeme", "is-warning"
	case models.PaymentStatusCanceled:
		return "İptal", "is-danger"
	case models.PaymentStatusExpired:
		return "Süre doldu", "is-danger"
	case models.PaymentStatusFailed:
		return "Hatalı", "is-danger"
	default:
		return emptyDefault(status, "Bilinmiyor"), ""
	}
}

func paymentWebhookStatus(payment models.PaymentSession) string {
	if payment.WebhookSentAt != nil {
		return "gönderildi"
	}
	if strings.TrimSpace(payment.WebhookLastError) != "" {
		return "hatalı"
	}
	if payment.WebhookAttempts > 0 {
		return "tekrar bekliyor"
	}
	if strings.TrimSpace(payment.WebhookEvent) != "" {
		return "bekliyor"
	}
	return "-"
}

func paymentAmountSortValue(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	if value == "" {
		return "0"
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return "0"
	}
	whole := parts[0]
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if whole == "" {
		whole = "0"
	}
	if !isDigitString(whole) || !isDigitString(fraction) {
		return "0"
	}
	if len(fraction) > 18 {
		fraction = fraction[:18]
	} else {
		fraction += strings.Repeat("0", 18-len(fraction))
	}
	out := strings.TrimLeft(whole+fraction, "0")
	if out == "" {
		return "0"
	}
	if negative {
		return "-" + out
	}
	return out
}

func isDigitString(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
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
		addresses := walletAddressViews(wallet)
		walletKind := "Kullanıcı wallet"
		if wallet.HDAddressId == 0 || domainLabel == "_reserve_" {
			walletKind = "Reserve wallet"
		}
		ownerRef := walletOwnerRef(wallet)
		views = append(views, DealerWalletView{
			ID:             wallet.ID.String(),
			ShortID:        shortText(wallet.ID.String(), 8, 6),
			MerchantID:     wallet.MerchantID.String(),
			Merchant:       merchant,
			Label:          walletLabel(wallet),
			ProductID:      emptyDash(wallet.ProductID),
			UserID:         emptyDash(wallet.UserID),
			OwnerRef:       ownerRef,
			DomainID:       wallet.DomainID.String(),
			Domain:         domainLabel,
			WalletKind:     walletKind,
			CreatedAt:      formatPanelTime(wallet.CreatedAt),
			Addresses:      addresses,
			AddressCount:   len(addresses),
			AddressPreview: dealerAddressPreview(addresses),
			MissingChains:  missing,
		})
	}
	return views
}

func dealerAddressPreview(addresses []DealerAddressView) string {
	parts := make([]string, 0, 2)
	for _, address := range addresses {
		value := strings.TrimSpace(address.Address)
		if value == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(address.Chain+" "+shortText(value, 8, 6)))
		if len(parts) == 2 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(addresses) > len(parts) {
		parts = append(parts, fmt.Sprintf("+%d", len(addresses)-len(parts)))
	}
	return strings.Join(parts, " · ")
}

func dealerBalancePreview(balances []DealerWalletBalanceRow) string {
	parts := make([]string, 0, 2)
	for _, balance := range balances {
		amount := strings.TrimSpace(balance.Available)
		if amount == "" {
			amount = strings.TrimSpace(balance.Deposited)
		}
		if amount == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(amount+" "+balance.Symbol))
		if len(parts) == 2 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(balances) > len(parts) {
		parts = append(parts, fmt.Sprintf("+%d", len(balances)-len(parts)))
	}
	return strings.Join(parts, " · ")
}

// buildWalletBalanceMap returns ledger-derived balance rows keyed by wallet id.
func buildWalletBalanceMap(ctx context.Context, ledgerRepo *repositories.LedgerRepo, wallets []models.Wallet, registry *asset.Registry) map[uuid.UUID][]DealerWalletBalanceRow {
	result := make(map[uuid.UUID][]DealerWalletBalanceRow)
	if ledgerRepo == nil || len(wallets) == 0 {
		return result
	}
	walletIDs := make([]uuid.UUID, 0, len(wallets))
	for _, wallet := range wallets {
		walletIDs = append(walletIDs, wallet.ID)
	}
	rows, err := ledgerRepo.WalletBalancesByWalletIDs(ctx, walletIDs)
	if err != nil {
		return result
	}
	buckets := make(map[uuid.UUID]map[string]*DealerWalletBalanceRow)
	lockedTotals := make(map[uuid.UUID]map[string]string)
	order := make(map[uuid.UUID][]string)
	for _, r := range rows {
		if r.WalletID == nil {
			continue
		}
		walletID := *r.WalletID
		token := ""
		if r.Token != nil {
			token = strings.ToLower(strings.TrimSpace(*r.Token))
		}
		identifier := token
		if identifier == "" {
			identifier = strings.TrimSpace(r.Symbol)
		}
		key := fmt.Sprintf("%d:%s:%s", r.ChainID, strings.ToUpper(strings.TrimSpace(r.Symbol)), token)
		if buckets[walletID] == nil {
			buckets[walletID] = make(map[string]*DealerWalletBalanceRow)
			lockedTotals[walletID] = make(map[string]string)
		}
		row := buckets[walletID][key]
		if row == nil {
			order[walletID] = append(order[walletID], key)
			row = &DealerWalletBalanceRow{
				Chain:    chainLabel(constants.ChainID(r.ChainID)),
				ChainID:  strconv.FormatInt(r.ChainID, 10),
				Symbol:   r.Symbol,
				Token:    token,
				AssetKey: dealerAssetKey(constants.ChainID(r.ChainID), identifier),
				LogoURL:  registryLogoURL(registry, r.Symbol),
				Decimals: r.Decimals,
			}
			buckets[walletID][key] = row
		}
		display := formatTokenAmount(r.BalanceRaw, r.Decimals)
		switch r.Account {
		case models.LedgerAccountMerchantAvailable:
			row.Available = display
			row.AvailableRaw = r.BalanceRaw
			row.Deposited = display
		case models.LedgerAccountMerchantPending:
			if row.Deposited == "" {
				row.Deposited = display
			}
		case models.LedgerAccountWithdrawalTransit, models.LedgerAccountRefundTransit, models.LedgerAccountSweepTransit:
			lockedTotals[walletID][key] = addTokenAmountRaw(lockedTotals[walletID][key], r.BalanceRaw)
			row.LockedRaw = lockedTotals[walletID][key]
			row.Locked = formatTokenAmount(lockedTotals[walletID][key], r.Decimals)
			if row.Deposited == "" {
				row.Deposited = display
			}
		default:
			if row.Deposited == "" {
				row.Deposited = display
			}
		}
	}
	for walletID, keys := range order {
		for _, key := range keys {
			row := buckets[walletID][key]
			if row.Deposited == "" {
				row.Deposited = "0"
			}
			if row.Available == "" {
				row.Available = "0"
			}
			if row.AvailableRaw == "" {
				row.AvailableRaw = "0"
			}
			if row.LockedRaw == "" {
				row.LockedRaw = "0"
			}
			result[walletID] = append(result[walletID], *row)
		}
	}
	return result
}

func dealerWalletViewsWithBalances(wallets []models.Wallet, balanceMap map[uuid.UUID][]DealerWalletBalanceRow) []DealerWalletView {
	views := dealerWalletViews(wallets)
	for i, v := range views {
		if id, err := uuid.Parse(v.ID); err == nil {
			if bals, ok := balanceMap[id]; ok {
				views[i].Balances = bals
				views[i].BalanceCount = len(bals)
				views[i].BalancePreview = dealerBalancePreview(bals)
			}
		}
	}
	return views
}

func filterDealerWalletViewsToAsset(views []DealerWalletView, selected asset.Asset) []DealerWalletView {
	if selected == nil {
		return views
	}
	assetKey := dealerAssetKey(selected.GetChainID(), selected.GetIdentifier())
	for i := range views {
		filtered := make([]DealerWalletBalanceRow, 0, len(views[i].Balances))
		for _, balance := range views[i].Balances {
			if balance.AssetKey == assetKey {
				filtered = append(filtered, balance)
			}
		}
		views[i].Balances = filtered
	}
	return views
}

func recoverWalletHasRecoverableAssetBalance(ctx context.Context, ledgerRepo *repositories.LedgerRepo, wallet models.Wallet, selected asset.Asset) (bool, error) {
	if ledgerRepo == nil {
		return false, errors.New("ledger repo hazır değil")
	}
	if selected == nil {
		return false, errors.New("asset seçimi geçersiz")
	}
	rows, err := ledgerRepo.WalletBalancesByWalletIDs(ctx, []uuid.UUID{wallet.ID})
	if err != nil {
		return false, err
	}
	assetKey := dealerAssetKey(selected.GetChainID(), selected.GetIdentifier())
	for _, row := range rows {
		if row.WalletID == nil || *row.WalletID != wallet.ID {
			continue
		}
		if row.Account != models.LedgerAccountMerchantAvailable && row.Account != models.LedgerAccountSweepTransit {
			continue
		}
		identifier := strings.TrimSpace(row.Symbol)
		if row.Token != nil && strings.TrimSpace(*row.Token) != "" {
			identifier = *row.Token
		}
		if dealerAssetKey(constants.ChainID(row.ChainID), identifier) == assetKey && positiveTokenAmountRaw(row.BalanceRaw) {
			return true, nil
		}
	}
	return false, nil
}

func buildWithdrawalWalletBalanceMap(ctx context.Context, ledgerRepo *repositories.LedgerRepo, wallets []models.Wallet, registry *asset.Registry, hideTestnets bool, hiddenChains string) map[uuid.UUID][]DealerWalletBalanceRow {
	result := make(map[uuid.UUID][]DealerWalletBalanceRow)
	if ledgerRepo == nil || len(wallets) == 0 {
		return result
	}
	for _, wallet := range wallets {
		rows, err := ledgerRepo.DomainBalances(ctx, wallet.MerchantID, wallet.DomainID)
		if err != nil {
			continue
		}
		balances := dealerLedgerBalanceViews(rows, registry)
		if hideTestnets || strings.TrimSpace(hiddenChains) != "" {
			balances = filterBalancesBySettings(balances, hideTestnets, hiddenChains)
		}
		result[wallet.ID] = dealerWithdrawalBalanceRows(balances, registry)
	}
	return result
}

func dealerWithdrawalBalanceRows(balances []DealerBalanceView, registry *asset.Registry) []DealerWalletBalanceRow {
	rows := make([]DealerWalletBalanceRow, 0, len(balances))
	for _, balance := range balances {
		chainID := chainSlugToID(balance.Chain)
		if !constants.IsSupportedChainID(chainID) {
			continue
		}
		identifier := strings.TrimSpace(balance.Token)
		if identifier == "" {
			identifier = strings.TrimSpace(balance.Symbol)
		}
		selected := assetFromBalance(registry, chainID, balance.Symbol, identifier)
		if selected == nil {
			continue
		}
		token := ""
		if !selected.IsNative() {
			token = selected.GetIdentifier()
		}
		display := formatTokenAmount(balance.AmountRaw, selected.GetDecimals())
		rows = append(rows, DealerWalletBalanceRow{
			Chain:        constants.ChainName(selected.GetChainID()),
			ChainID:      strconv.FormatInt(int64(selected.GetChainID()), 10),
			Symbol:       selected.GetSymbol(),
			Token:        token,
			AssetKey:     dealerAssetKey(selected.GetChainID(), selected.GetIdentifier()),
			LogoURL:      registryLogoURL(registry, selected.GetSymbol()),
			Deposited:    display,
			Available:    display,
			AvailableRaw: balance.AmountRaw,
			Decimals:     selected.GetDecimals(),
		})
	}
	return rows
}

func dealerWithdrawalAssetViews(balances []DealerBalanceView, registry *asset.Registry) []DealerWithdrawalAssetView {
	if registry == nil {
		return nil
	}
	views := make([]DealerWithdrawalAssetView, 0, len(balances))
	seen := make(map[string]struct{})
	for _, balance := range balances {
		if !positiveTokenAmountRaw(balance.AmountRaw) {
			continue
		}
		chainID := chainSlugToID(balance.Chain)
		if !constants.IsSupportedChainID(chainID) {
			continue
		}
		identifier := strings.TrimSpace(balance.Token)
		if identifier == "" {
			identifier = strings.TrimSpace(balance.Symbol)
		}
		selected := assetFromBalance(registry, chainID, balance.Symbol, identifier)
		if selected == nil {
			continue
		}
		key := dealerAssetKey(selected.GetChainID(), selected.GetIdentifier())
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		token := ""
		displayToken := "native"
		if !selected.IsNative() {
			token = selected.GetIdentifier()
			displayToken = shortText(token, 8, 6)
		}
		available := formatTokenAmount(balance.AmountRaw, selected.GetDecimals())
		chainLabelValue := chainLabel(selected.GetChainID())
		views = append(views, DealerWithdrawalAssetView{
			Value:            fmt.Sprintf("%d|%s", selected.GetChainID(), selected.GetIdentifier()),
			AssetKey:         key,
			Label:            fmt.Sprintf("%s / %s / %s available", chainLabelValue, selected.GetSymbol(), available),
			Chain:            constants.ChainName(selected.GetChainID()),
			ChainLabel:       chainLabelValue,
			Symbol:           selected.GetSymbol(),
			Token:            token,
			DisplayToken:     displayToken,
			Decimals:         selected.GetDecimals(),
			AvailableRaw:     balance.AmountRaw,
			AvailableDisplay: available + " " + selected.GetSymbol(),
			AvailableInput:   available,
		})
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Symbol != views[j].Symbol {
			return views[i].Symbol < views[j].Symbol
		}
		return views[i].ChainLabel < views[j].ChainLabel
	})
	return views
}

func assetFromBalance(registry *asset.Registry, chainID constants.ChainID, symbol string, identifier string) asset.Asset {
	if registry == nil {
		return nil
	}
	if selected, ok := registry.Get(chainID, identifier); ok {
		return selected
	}
	if selected, ok := registry.GetBySymbol(chainID, symbol); ok {
		return selected
	}
	return nil
}

func positiveTokenAmountRaw(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	n := new(big.Int)
	if _, ok := n.SetString(value, 10); !ok {
		return false
	}
	return n.Sign() > 0
}

func dealerPaginationView(page int, limit int, total int64, basePath string) DealerPaginationView {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	totalPages := totalPagesFor(total, limit)
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

func totalPagesFor(total int64, limit int) int {
	if total <= 0 {
		return 0
	}
	if limit < 1 {
		limit = 20
	}
	return int((total + int64(limit) - 1) / int64(limit))
}

func paginateViewSlice[T any](items []T, page int, limit int) []T {
	if len(items) == 0 {
		return items
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		return items
	}
	start := (page - 1) * limit
	if start >= len(items) {
		return items[:0]
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
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
		{"TRX-Testnet", "tron-testnet", wallet.TronAddress},
		{"SOL", "solana", wallet.SolanaAddress},
	}
}

func walletAddressViews(wallet models.Wallet) []DealerAddressView {
	filtered := make([]DealerAddressView, 0, 11)
	for _, def := range walletChainDefs(wallet) {
		if strings.TrimSpace(def.address) != "" {
			filtered = append(filtered, DealerAddressView{
				Chain:       def.label,
				Address:     def.address,
				ExplorerURL: addressExplorerURL(nil, chainSlugToID(def.chainName), def.address),
			})
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

func dealerTreasuryBalanceGroups(balances []DealerBalanceView, registry *asset.Registry) []DealerVaultAssetView {
	details := make([]DealerVaultBalanceView, 0, len(balances))
	for _, balance := range balances {
		amountRaw := zeroTokenAmountRaw(balance.AmountRaw)
		amountDisplay := formatTokenAmount(amountRaw, balance.Decimals)
		amountSort := tokenAmountSortValue(amountRaw, balance.Decimals)
		details = append(details, DealerVaultBalanceView{
			Chain:            balance.Chain,
			ChainLogoURL:     balance.ChainLogoURL,
			Symbol:           balance.Symbol,
			Token:            balance.Token,
			DisplayToken:     balance.DisplayToken,
			LogoURL:          balance.LogoURL,
			Decimals:         balance.Decimals,
			VaultRaw:         amountRaw,
			VaultDisplay:     amountDisplay,
			VaultSort:        amountSort,
			AvailableRaw:     amountRaw,
			AvailableDisplay: amountDisplay,
			AvailableSort:    amountSort,
		})
	}
	return dealerVaultAssetGroups(details, registry)
}

func dealerVaultBalanceViews(rows []repositories.LedgerBalanceRow, registry *asset.Registry) []DealerVaultAssetView {
	return dealerVaultAssetGroups(dealerVaultNetworkBalanceViews(rows, registry), registry)
}

func dealerVaultNetworkBalanceViews(rows []repositories.LedgerBalanceRow, registry *asset.Registry) []DealerVaultBalanceView {
	buckets := make(map[string]*DealerVaultBalanceView)
	order := make([]string, 0, len(rows))
	ensureView := func(chainID constants.ChainID, symbol string, token string, decimals uint8) *DealerVaultBalanceView {
		token = strings.ToLower(strings.TrimSpace(token))
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		key := fmt.Sprintf("%d:%s:%s", chainID, symbol, token)
		view := buckets[key]
		if view != nil {
			return view
		}
		order = append(order, key)
		view = &DealerVaultBalanceView{
			Chain:        chainLabel(chainID),
			ChainLogoURL: asset.ChainLogoURL(chainID),
			Symbol:       symbol,
			Token:        token,
			DisplayToken: emptyDash(token),
			LogoURL:      registryLogoURL(registry, symbol),
			Decimals:     decimals,
		}
		buckets[key] = view
		return view
	}

	if registry != nil {
		seen := make(map[string]struct{})
		for _, assetInfo := range registry.ListAll() {
			if assetInfo == nil {
				continue
			}
			token := ""
			if !assetInfo.IsNative() {
				token = assetInfo.GetIdentifier()
			}
			key := fmt.Sprintf("%d:%s:%s", assetInfo.GetChainID(), strings.ToUpper(strings.TrimSpace(assetInfo.GetSymbol())), strings.ToLower(strings.TrimSpace(token)))
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			ensureView(assetInfo.GetChainID(), assetInfo.GetSymbol(), token, assetInfo.GetDecimals())
		}
	}

	for _, row := range rows {
		if row.Account == models.LedgerAccountPlatformClearing {
			continue
		}
		token := ""
		if row.Token != nil {
			token = *row.Token
		}
		view := ensureView(constants.ChainID(row.ChainID), row.Symbol, token, row.Decimals)
		switch row.Account {
		case models.LedgerAccountMerchantAvailable:
			view.AvailableRaw = addTokenAmountRaw(view.AvailableRaw, row.BalanceRaw)
		case models.LedgerAccountMerchantPending:
			view.PendingRaw = addTokenAmountRaw(view.PendingRaw, row.BalanceRaw)
		case models.LedgerAccountWithdrawalTransit:
			view.WithdrawalRaw = addTokenAmountRaw(view.WithdrawalRaw, row.BalanceRaw)
		case models.LedgerAccountRefundTransit:
			view.RefundRaw = addTokenAmountRaw(view.RefundRaw, row.BalanceRaw)
		case models.LedgerAccountSweepTransit:
			view.SweepRaw = addTokenAmountRaw(view.SweepRaw, row.BalanceRaw)
		}
	}

	views := make([]DealerVaultBalanceView, 0, len(order))
	for _, key := range order {
		view := *buckets[key]
		view.AvailableRaw = zeroTokenAmountRaw(view.AvailableRaw)
		view.PendingRaw = zeroTokenAmountRaw(view.PendingRaw)
		view.WithdrawalRaw = zeroTokenAmountRaw(view.WithdrawalRaw)
		view.RefundRaw = zeroTokenAmountRaw(view.RefundRaw)
		view.SweepRaw = zeroTokenAmountRaw(view.SweepRaw)
		view.LockedRaw = addTokenAmountRaw(addTokenAmountRaw(view.WithdrawalRaw, view.RefundRaw), view.SweepRaw)
		view.VaultRaw = addTokenAmountRaw(addTokenAmountRaw(view.AvailableRaw, view.PendingRaw), view.LockedRaw)
		view.AvailableDisplay = formatTokenAmount(view.AvailableRaw, view.Decimals)
		view.AvailableSort = tokenAmountSortValue(view.AvailableRaw, view.Decimals)
		view.PendingDisplay = formatTokenAmount(view.PendingRaw, view.Decimals)
		view.PendingSort = tokenAmountSortValue(view.PendingRaw, view.Decimals)
		view.WithdrawalDisplay = formatTokenAmount(view.WithdrawalRaw, view.Decimals)
		view.WithdrawalSort = tokenAmountSortValue(view.WithdrawalRaw, view.Decimals)
		view.RefundDisplay = formatTokenAmount(view.RefundRaw, view.Decimals)
		view.RefundSort = tokenAmountSortValue(view.RefundRaw, view.Decimals)
		view.SweepDisplay = formatTokenAmount(view.SweepRaw, view.Decimals)
		view.SweepSort = tokenAmountSortValue(view.SweepRaw, view.Decimals)
		view.LockedDisplay = formatTokenAmount(view.LockedRaw, view.Decimals)
		view.LockedSort = tokenAmountSortValue(view.LockedRaw, view.Decimals)
		view.VaultDisplay = formatTokenAmount(view.VaultRaw, view.Decimals)
		view.VaultSort = tokenAmountSortValue(view.VaultRaw, view.Decimals)
		views = append(views, view)
	}
	return views
}

func dealerVaultAssetGroups(details []DealerVaultBalanceView, registry *asset.Registry) []DealerVaultAssetView {
	buckets := make(map[string]*DealerVaultAssetView)
	order := make([]string, 0, len(details))
	for _, detail := range details {
		symbol := vaultCanonicalSymbol(registry, detail.Symbol)
		group := buckets[symbol]
		if group == nil {
			order = append(order, symbol)
			group = &DealerVaultAssetView{
				ID:      vaultGroupID(symbol),
				Symbol:  symbol,
				LogoURL: registryLogoURL(registry, symbol),
			}
			if group.LogoURL == "" {
				group.LogoURL = detail.LogoURL
			}
			buckets[symbol] = group
		}
		if group.LogoURL == "" && detail.LogoURL != "" {
			group.LogoURL = detail.LogoURL
		}
		group.Details = append(group.Details, detail)
		group.AvailableRaw = addNormalizedTokenAmountRaw(group.AvailableRaw, detail.AvailableRaw, detail.Decimals)
		group.PendingRaw = addNormalizedTokenAmountRaw(group.PendingRaw, detail.PendingRaw, detail.Decimals)
		group.WithdrawalRaw = addNormalizedTokenAmountRaw(group.WithdrawalRaw, detail.WithdrawalRaw, detail.Decimals)
		group.RefundRaw = addNormalizedTokenAmountRaw(group.RefundRaw, detail.RefundRaw, detail.Decimals)
		group.SweepRaw = addNormalizedTokenAmountRaw(group.SweepRaw, detail.SweepRaw, detail.Decimals)
	}

	groups := make([]DealerVaultAssetView, 0, len(order))
	for _, symbol := range order {
		group := *buckets[symbol]
		sort.SliceStable(group.Details, func(i, j int) bool {
			if !strings.EqualFold(group.Details[i].Chain, group.Details[j].Chain) {
				return strings.ToUpper(group.Details[i].Chain) < strings.ToUpper(group.Details[j].Chain)
			}
			if !strings.EqualFold(group.Details[i].Symbol, group.Details[j].Symbol) {
				return strings.ToUpper(group.Details[i].Symbol) < strings.ToUpper(group.Details[j].Symbol)
			}
			return strings.ToLower(group.Details[i].Token) < strings.ToLower(group.Details[j].Token)
		})
		group.AvailableRaw = zeroTokenAmountRaw(group.AvailableRaw)
		group.PendingRaw = zeroTokenAmountRaw(group.PendingRaw)
		group.WithdrawalRaw = zeroTokenAmountRaw(group.WithdrawalRaw)
		group.RefundRaw = zeroTokenAmountRaw(group.RefundRaw)
		group.SweepRaw = zeroTokenAmountRaw(group.SweepRaw)
		group.LockedRaw = addTokenAmountRaw(addTokenAmountRaw(group.WithdrawalRaw, group.RefundRaw), group.SweepRaw)
		group.VaultRaw = addTokenAmountRaw(addTokenAmountRaw(group.AvailableRaw, group.PendingRaw), group.LockedRaw)
		group.AvailableDisplay = formatTokenAmount(group.AvailableRaw, tokenAmountSortScale)
		group.AvailableSort = group.AvailableRaw
		group.PendingDisplay = formatTokenAmount(group.PendingRaw, tokenAmountSortScale)
		group.PendingSort = group.PendingRaw
		group.WithdrawalDisplay = formatTokenAmount(group.WithdrawalRaw, tokenAmountSortScale)
		group.WithdrawalSort = group.WithdrawalRaw
		group.RefundDisplay = formatTokenAmount(group.RefundRaw, tokenAmountSortScale)
		group.RefundSort = group.RefundRaw
		group.SweepDisplay = formatTokenAmount(group.SweepRaw, tokenAmountSortScale)
		group.SweepSort = group.SweepRaw
		group.LockedDisplay = formatTokenAmount(group.LockedRaw, tokenAmountSortScale)
		group.LockedSort = group.LockedRaw
		group.VaultDisplay = formatTokenAmount(group.VaultRaw, tokenAmountSortScale)
		group.VaultSort = group.VaultRaw
		group.NetworkCount = vaultNetworkCount(group.Details)
		group.VariantCount = vaultVariantCount(group.Details)
		group.SearchText = vaultGroupSearchText(group)
		groups = append(groups, group)
	}

	sort.SliceStable(groups, func(i, j int) bool {
		return strings.ToUpper(groups[i].Symbol) < strings.ToUpper(groups[j].Symbol)
	})
	return groups
}

func filterBalancesBySettings(balances []DealerBalanceView, hideTestnets bool, hiddenChains string) []DealerBalanceView {
	hidden := parseHiddenChainIDs(hiddenChains)
	out := balances[:0]
	for _, b := range balances {
		chainID := chainSlugToID(b.Chain)
		if hideTestnets && constants.IsTestnet(chainID) {
			continue
		}
		if hidden[chainID] {
			continue
		}
		out = append(out, b)
	}
	return out
}

func filterVaultsBySettings(vaults []DealerChainVaultView, hideTestnets bool, hiddenChains string) []DealerChainVaultView {
	hidden := parseHiddenChainIDs(hiddenChains)
	out := vaults[:0]
	for _, v := range vaults {
		chainID := chainSlugToID(v.Chain)
		if hideTestnets && constants.IsTestnet(chainID) {
			continue
		}
		if hidden[chainID] {
			continue
		}
		out = append(out, v)
	}
	return out
}

func dealerSettingsNetworkViews(hideTestnets bool, hiddenChains string) ([]DealerSettingsNetworkView, []DealerSettingsNetworkView) {
	hidden := parseHiddenChainIDs(hiddenChains)
	chainIDs := constants.AllChainIDs()
	visibleViews := make([]DealerSettingsNetworkView, 0, len(chainIDs))
	hiddenViews := make([]DealerSettingsNetworkView, 0, len(chainIDs))
	for _, chainID := range chainIDs {
		view := DealerSettingsNetworkView{
			Key:            constants.ChainName(chainID),
			Chain:          chainLabel(chainID),
			ChainLogoURL:   asset.ChainLogoURL(chainID),
			Testnet:        constants.IsTestnet(chainID),
			ExplicitHidden: hidden[chainID],
		}
		view.PolicyHidden = hideTestnets && view.Testnet && !view.ExplicitHidden
		if view.ExplicitHidden || view.PolicyHidden {
			hiddenViews = append(hiddenViews, view)
			continue
		}
		visibleViews = append(visibleViews, view)
	}
	return visibleViews, hiddenViews
}

func parseHiddenChainIDs(value string) map[constants.ChainID]bool {
	hidden := make(map[constants.ChainID]bool)
	for _, raw := range strings.Split(value, ",") {
		chainID := chainSlugToID(raw)
		if constants.IsSupportedChainID(chainID) {
			hidden[chainID] = true
		}
	}
	return hidden
}

func canonicalHiddenChains(value string) (string, error) {
	if len(value) > 2048 {
		return "", errors.New("Gizli ağ seçimi çok uzun.")
	}
	selected := make(map[constants.ChainID]bool)
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		chainID := chainSlugToID(raw)
		if !constants.IsSupportedChainID(chainID) {
			return "", errors.New("Desteklenmeyen ağ seçimi gönderildi.")
		}
		selected[chainID] = true
	}
	canonical := make([]string, 0, len(selected))
	for _, chainID := range constants.AllChainIDs() {
		if selected[chainID] {
			canonical = append(canonical, constants.ChainName(chainID))
		}
	}
	return strings.Join(canonical, ","), nil
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
		constants.TRONTestnet: {"tron-testnet", "tron testnet", "trx-testnet", "nile", "tron-nile", "trx-nile", "tron-shasta", "shasta"},
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
		if strings.EqualFold(chainLabel(id), slug) {
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

func dealerRescanChainOptions() []DealerRescanChainOption {
	chainIDs := []constants.ChainID{
		constants.Bitcoin,
		constants.Ethereum,
		constants.Binance,
		constants.Base,
		constants.Arbitrum,
		constants.Avalanche,
		constants.Unichain,
		constants.Solana,
		constants.TRON,
		constants.Chiliz,
		constants.ChilizSpicy,
	}

	options := make([]DealerRescanChainOption, 0, len(chainIDs))
	for _, chainID := range chainIDs {
		meta := "Mainnet"
		if constants.IsTestnet(chainID) {
			meta = "Testnet"
		}
		options = append(options, DealerRescanChainOption{
			Name:    constants.ChainName(chainID),
			Label:   chainLabel(chainID),
			ChainID: strconv.FormatInt(int64(chainID), 10),
			LogoURL: asset.ChainLogoURL(chainID),
			Meta:    meta,
		})
	}
	return options
}

func dealerActivityViews(transactions []models.Transaction, registry *asset.Registry, chains *blockchain.ChainFactory) []DealerActivityView {
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
		statusLabel, statusClass := depositStatusPresentation(tx.Status)
		searchText := strings.Join([]string{
			tx.ID.String(),
			eventType,
			chainLabel(tx.ChainID),
			tx.Symbol,
			tx.Amount,
			tx.Status,
			statusLabel,
			tx.Hash,
			tx.FromAddress,
			tx.ToAddress,
			tx.ProductID,
			tx.UserID,
			webhookStatus,
		}, " ")
		views = append(views, DealerActivityView{
			ID:              tx.ID.String(),
			ShortID:         shortText(tx.ID.String(), 8, 6),
			Type:            eventType,
			Chain:           chainLabel(tx.ChainID),
			ChainLogoURL:    asset.ChainLogoURL(tx.ChainID),
			Symbol:          tx.Symbol,
			LogoURL:         registryLogoURL(registry, tx.Symbol),
			AmountRaw:       tx.Amount,
			AmountDisplay:   formatTokenAmount(tx.Amount, tx.Decimals),
			AmountSort:      tx.Amount,
			Status:          tx.Status,
			StatusLabel:     statusLabel,
			StatusClass:     statusClass,
			Hash:            tx.Hash,
			HashShort:       shortText(tx.Hash, 10, 8),
			ExplorerURL:     transactionExplorerURL(chains, tx.ChainID, tx.Hash),
			FromAddress:     shortText(tx.FromAddress, 10, 8),
			FromAddressFull: tx.FromAddress,
			ToAddress:       shortText(tx.ToAddress, 10, 8),
			ToAddressFull:   tx.ToAddress,
			ProductID:       emptyDash(tx.ProductID),
			UserID:          emptyDash(tx.UserID),
			WebhookStatus:   webhookStatus,
			WebhookAttempts: tx.WebhookAttempts,
			CreatedAt:       formatPanelTime(tx.CreatedAt),
			CreatedSort:     strconv.FormatInt(tx.CreatedAt.Unix(), 10),
			SearchText:      searchText,
		})
	}
	return views
}

func depositStatusPresentation(status string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "confirmed", "finalized", "succeeded", "success":
		return status, "is-success"
	case "pending", "processing":
		return status, "is-warning"
	case "failed", "rejected", "error":
		return status, "is-danger"
	default:
		return emptyDefault(status, "unknown"), ""
	}
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
			ActorRole:   emptyDash(log.ActorRole),
			Decision:    emptyDash(log.Decision),
			Subject:     emptyDash(subject),
			Description: emptyDash(log.Description),
			Reason:      emptyDash(log.Reason),
			BeforeAfter: dealerAuditBeforeAfter(log.BeforeStatus, log.AfterStatus),
			IPAddress:   emptyDash(log.IPAddress),
			UserAgent:   shortText(log.UserAgent, 52, 18),
			Method:      emptyDash(log.Method),
			Path:        emptyDash(log.Path),
			CreatedAt:   formatPanelTime(log.CreatedAt),
			CreatedSort: strconv.FormatInt(log.CreatedAt.Unix(), 10),
			CreatedISO:  log.CreatedAt.UTC().Format(time.RFC3339Nano),
			IsOIDC:      strings.Contains(log.Event, "oidc"),
			IsFailed:    log.Status == "failed",
		})
	}
	return views
}

func dealerAuditBeforeAfter(before string, after string) string {
	before = strings.TrimSpace(before)
	after = strings.TrimSpace(after)
	if before == "" && after == "" {
		return "-"
	}
	if before == "" {
		before = "-"
	}
	if after == "" {
		after = "-"
	}
	return before + " -> " + after
}

func logDealerActivity(c fiber.Ctx, repo *repositories.ActivityLogRepo, merchantID *uuid.UUID, actorType string, actorEmail string, event string, status string, subjectType string, subjectID string, description string) {
	if repo == nil {
		return
	}
	correlationID := strings.TrimSpace(middleware.RequestIDFromCtx(c))
	if correlationID == "" {
		correlationID = strings.TrimSpace(c.Get("X-Request-ID"))
	}
	safeDescription := redactAuditDescription(description)
	resolvedActorType := emptyDefault(actorType, "system")
	actorRole := dealerActorRole(c, resolvedActorType)
	decision := emptyDefault(status, "info")
	activityLog := &models.ActivityLog{
		MerchantID:    merchantID,
		ActorType:     resolvedActorType,
		ActorEmail:    strings.TrimSpace(actorEmail),
		ActorRole:     actorRole,
		Event:         emptyDefault(event, "activity"),
		Status:        decision,
		Decision:      decision,
		Reason:        safeDescription,
		SubjectType:   strings.TrimSpace(subjectType),
		SubjectID:     strings.TrimSpace(subjectID),
		Description:   safeDescription,
		IPAddress:     clientIP(c),
		UserAgent:     strings.TrimSpace(c.Get("User-Agent")),
		Method:        strings.TrimSpace(c.Method()),
		Path:          strings.TrimSpace(c.Path()),
		CorrelationID: correlationID,
		CreatedAt:     time.Now().UTC(),
	}
	if err := repo.Create(c.Context(), activityLog); err != nil {
		log.Printf(
			"dealer activity audit write event=%s subject_type=%s subject_id=%s correlation_id=%s error=%v",
			activityLog.Event,
			activityLog.SubjectType,
			activityLog.SubjectID,
			activityLog.CorrelationID,
			err,
		)
	}
}

func logDealerDecisionActivity(c fiber.Ctx, repo *repositories.ActivityLogRepo, merchantID *uuid.UUID, domainID *uuid.UUID, actorType string, actorEmail string, event string, status string, subjectType string, subjectID string, description string, beforeStatus string, afterStatus string) {
	if repo == nil {
		return
	}
	correlationID := strings.TrimSpace(middleware.RequestIDFromCtx(c))
	if correlationID == "" {
		correlationID = strings.TrimSpace(c.Get("X-Request-ID"))
	}
	safeDescription := redactAuditDescription(description)
	resolvedActorType := emptyDefault(actorType, "system")
	actorRole := dealerActorRole(c, resolvedActorType)
	decision := emptyDefault(status, "info")
	activityLog := &models.ActivityLog{
		MerchantID:    merchantID,
		DomainID:      domainID,
		ActorType:     resolvedActorType,
		ActorEmail:    strings.TrimSpace(actorEmail),
		ActorRole:     actorRole,
		Event:         emptyDefault(event, "activity"),
		Status:        decision,
		Decision:      decision,
		Reason:        safeDescription,
		SubjectType:   strings.TrimSpace(subjectType),
		SubjectID:     strings.TrimSpace(subjectID),
		Description:   safeDescription,
		BeforeStatus:  strings.TrimSpace(beforeStatus),
		AfterStatus:   strings.TrimSpace(afterStatus),
		IPAddress:     clientIP(c),
		UserAgent:     strings.TrimSpace(c.Get("User-Agent")),
		Method:        strings.TrimSpace(c.Method()),
		Path:          strings.TrimSpace(c.Path()),
		CorrelationID: correlationID,
		CreatedAt:     time.Now().UTC(),
	}
	if err := repo.Create(c.Context(), activityLog); err != nil {
		log.Printf(
			"dealer decision audit write event=%s subject_type=%s subject_id=%s correlation_id=%s error=%v",
			activityLog.Event,
			activityLog.SubjectType,
			activityLog.SubjectID,
			activityLog.CorrelationID,
			err,
		)
	}
}

func dealerActorRole(c fiber.Ctx, actorType string) string {
	actorType = emptyDefault(actorType, "system")
	if strings.EqualFold(actorType, "admin") {
		if role, ok := c.Locals(adminSessionRoleLocal).(string); ok {
			role = models.EffectiveAdminRole(role)
			if role != "" {
				return role
			}
		}
	}
	return actorType
}

func redactAuditDescription(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return ""
	}
	lower := strings.ToLower(description)
	for _, token := range []string{
		"api_secret",
		"api secret",
		"webhook_secret",
		"webhook secret",
		"x-api-secret",
		"x-gateway-signature",
		"signature",
		"mnemonic",
		"private key",
		"private_key",
		"raw signed",
		"signed transaction",
		"secret=",
	} {
		if strings.Contains(lower, token) {
			return "[redacted]"
		}
	}
	if len(description) > 1000 {
		return description[:1000] + "...[truncated]"
	}
	return description
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
		chainID := chainSlugToID(request.Chain)
		chainLogoURL := ""
		if constants.IsSupportedChainID(chainID) {
			chainLogoURL = asset.ChainLogoURL(chainID)
		}
		token := ""
		if request.Token != nil {
			token = strings.TrimSpace(*request.Token)
		}
		symbol := strings.TrimSpace(request.Symbol)
		if symbol == "" && token != "" {
			symbol = shortText(token, 8, 6)
		}
		if symbol == "" {
			symbol = "asset"
		}
		statusLabel, statusClass := withdrawalStatusPresentation(request.Status)
		txHashShort := ""
		if request.TxHash != "" {
			txHashShort = shortText(request.TxHash, 10, 8)
		}
		searchText := strings.Join([]string{
			request.ID.String(),
			request.MerchantID.String(),
			merchantName,
			request.WalletID.String(),
			request.Chain,
			symbol,
			token,
			request.ToAddress,
			request.AmountRaw,
			request.Status,
			statusLabel,
			request.TxHash,
			request.Error,
			request.Note,
			request.RequestedBy,
			request.ReviewedBy,
		}, " ")
		views = append(views, DealerWithdrawalView{
			ID:              request.ID.String(),
			ShortID:         shortText(request.ID.String(), 8, 6),
			MerchantID:      request.MerchantID.String(),
			ShortMerchantID: shortText(request.MerchantID.String(), 8, 6),
			MerchantName:    merchantName,
			WalletID:        request.WalletID.String(),
			ShortWalletID:   shortText(request.WalletID.String(), 8, 6),
			Chain:           request.Chain,
			ChainLogoURL:    chainLogoURL,
			ToAddress:       request.ToAddress,
			ToAddressShort:  shortText(request.ToAddress, 10, 8),
			AmountRaw:       request.AmountRaw,
			AmountDisplay:   formatTokenAmount(request.AmountRaw, request.Decimals),
			Symbol:          symbol,
			Token:           token,
			Note:            request.Note,
			Status:          request.Status,
			StatusLabel:     statusLabel,
			StatusClass:     statusClass,
			TxHash:          request.TxHash,
			TxHashShort:     txHashShort,
			Error:           request.Error,
			RequestedBy:     request.RequestedBy,
			ReviewedBy:      request.ReviewedBy,
			CreatedAt:       formatPanelTime(request.CreatedAt),
			CreatedSort:     strconv.FormatInt(request.CreatedAt.Unix(), 10),
			SearchText:      searchText,
		})
	}
	return views
}

func withdrawalStatusPresentation(status string) (string, string) {
	switch status {
	case models.WithdrawalStatusPending:
		return "Bekliyor", "is-warning"
	case models.WithdrawalStatusProcessing:
		return "İşleniyor", "is-info"
	case models.WithdrawalStatusApproved:
		return "Onaylandı", "is-info"
	case models.WithdrawalStatusFinalized:
		return "Finalized", "is-success"
	case models.WithdrawalStatusRejected:
		return "Reddedildi", "is-danger"
	case models.WithdrawalStatusFailed:
		return "Hatalı", "is-danger"
	default:
		return emptyDefault(status, "Bilinmiyor"), ""
	}
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
	case constants.TRONTestnet:
		return "TRON Nile Testnet"
	case constants.ChilizSpicy:
		return "Chiliz Spicy"
	default:
		return fmt.Sprintf("chain-%d", chainID)
	}
}

func transactionExplorerURL(chains *blockchain.ChainFactory, chainID constants.ChainID, hash string) string {
	hash = strings.TrimSpace(hash)
	if hash == "" || chains == nil {
		return ""
	}
	chain, err := chains.GetChainByID(chainID)
	if err != nil || chain == nil {
		return ""
	}
	explorerChain, ok := chain.(interface {
		Explorer() string
	})
	if !ok {
		return ""
	}
	baseURL := strings.TrimRight(strings.TrimSpace(explorerChain.Explorer()), "/")
	if baseURL == "" {
		return ""
	}
	return baseURL + transactionExplorerPath(chainID, url.PathEscape(hash))
}

func addressExplorerURL(chains *blockchain.ChainFactory, chainID constants.ChainID, address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	baseURL := explorerBaseURL(chains, chainID)
	if baseURL == "" {
		return ""
	}
	return baseURL + addressExplorerPath(chainID, url.PathEscape(address))
}

func explorerBaseURL(chains *blockchain.ChainFactory, chainID constants.ChainID) string {
	if chains != nil {
		chain, err := chains.GetChainByID(chainID)
		if err == nil && chain != nil {
			if explorerChain, ok := chain.(interface{ Explorer() string }); ok {
				if baseURL := strings.TrimRight(strings.TrimSpace(explorerChain.Explorer()), "/"); baseURL != "" {
					return baseURL
				}
			}
		}
	}
	switch chainID {
	case constants.Bitcoin:
		return "https://www.blockchain.com/explorer"
	case constants.Ethereum:
		return "https://etherscan.io"
	case constants.Binance:
		return "https://bscscan.com"
	case constants.Base:
		return "https://basescan.org"
	case constants.Arbitrum:
		return "https://arbiscan.io"
	case constants.Unichain:
		return "https://uniscan.xyz"
	case constants.Avalanche:
		return "https://snowscan.xyz"
	case constants.Chiliz:
		return "https://scan.chiliz.com"
	case constants.ChilizSpicy:
		return "https://spicy-explorer.chiliz.com"
	case constants.TRON:
		return "https://tronscan.org"
	case constants.TRONTestnet:
		return "https://nile.tronscan.org"
	case constants.Solana:
		return "https://explorer.solana.com"
	default:
		return ""
	}
}

func transactionExplorerPath(chainID constants.ChainID, escapedHash string) string {
	switch chainID {
	case constants.Bitcoin:
		return "/transactions/btc/" + escapedHash
	case constants.Solana:
		return "/tx/" + escapedHash
	case constants.TRON, constants.TRONTestnet:
		return "/#/transaction/" + escapedHash
	default:
		return "/tx/" + escapedHash
	}
}

func addressExplorerPath(chainID constants.ChainID, escapedAddress string) string {
	switch chainID {
	case constants.Bitcoin:
		return "/addresses/btc/" + escapedAddress
	case constants.TRON, constants.TRONTestnet:
		return "/#/address/" + escapedAddress
	default:
		return "/address/" + escapedAddress
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

func addTokenAmountRaw(current string, next string) string {
	next = strings.TrimSpace(next)
	if current == "" {
		return next
	}
	left, ok := new(big.Int).SetString(strings.TrimSpace(current), 10)
	if !ok {
		return next
	}
	right, ok := new(big.Int).SetString(next, 10)
	if !ok {
		return current
	}
	return left.Add(left, right).String()
}

func zeroTokenAmountRaw(value string) string {
	if strings.TrimSpace(value) == "" {
		return "0"
	}
	return strings.TrimSpace(value)
}

const tokenAmountSortScale = 36

func addNormalizedTokenAmountRaw(current string, next string, decimals uint8) string {
	return addTokenAmountRaw(current, tokenAmountSortValue(next, decimals))
}

func vaultCanonicalSymbol(registry *asset.Registry, symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if registry == nil {
		return symbol
	}
	return registry.CanonicalSymbol(symbol)
}

func vaultGroupID(symbol string) string {
	symbol = strings.ToLower(strings.TrimSpace(symbol))
	if symbol == "" {
		return "vault-asset"
	}
	var b strings.Builder
	lastDash := false
	for _, char := range symbol {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			b.WriteRune(char)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	value := strings.Trim(b.String(), "-")
	if value == "" {
		return "vault-asset"
	}
	return "vault-" + value
}

func vaultNetworkCount(details []DealerVaultBalanceView) int {
	seen := make(map[string]struct{}, len(details))
	for _, detail := range details {
		key := strings.ToUpper(strings.TrimSpace(detail.Chain))
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
	}
	return len(seen)
}

func vaultVariantCount(details []DealerVaultBalanceView) int {
	seen := make(map[string]struct{}, len(details))
	for _, detail := range details {
		key := strings.ToUpper(strings.TrimSpace(detail.Symbol))
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
	}
	return len(seen)
}

func vaultGroupSearchText(group DealerVaultAssetView) string {
	parts := []string{group.Symbol}
	for _, detail := range group.Details {
		parts = append(parts, detail.Symbol, detail.Chain, detail.Token)
	}
	return strings.Join(parts, " ")
}

func tokenAmountSortValue(raw string, decimals uint8) string {
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
			return "0"
		}
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0"
	}
	precision := int(decimals)
	if precision <= tokenAmountSortScale {
		value += strings.Repeat("0", tokenAmountSortScale-precision)
	} else {
		cut := precision - tokenAmountSortScale
		if cut >= len(value) {
			value = "0"
		} else {
			value = value[:len(value)-cut]
		}
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0"
	}
	if negative {
		return "-" + value
	}
	return value
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
