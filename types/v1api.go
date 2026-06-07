package types

// ─── Request bodies ───────────────────────────────────────────────────────────

// V1InvoiceRequest is the request body for POST /api/v1/payment/create and /payment/white-label.
type V1InvoiceRequest struct {
	OrderID     string `json:"order_id"              example:"ORD-2024-001"                  swaggertype:"string"`
	Amount      string `json:"amount"                example:"25.00"                        swaggertype:"string"`
	Currency    string `json:"currency"              example:"USD"                          swaggertype:"string"`
	Description string `json:"description,omitempty" example:"Product purchase"              swaggertype:"string"`
	UserID      string `json:"user_id,omitempty"     example:"customer_42"                  swaggertype:"string"`
	SuccessURL  string `json:"success_url,omitempty" example:"https://example.com/success"   swaggertype:"string"`
	CancelURL   string `json:"cancel_url,omitempty"  example:"https://example.com/cancel"    swaggertype:"string"`
}

// V1StaticAddressRequest is the request body for POST /api/v1/payment/static-address.
type V1StaticAddressRequest struct {
	UserID  string `json:"user_id"            example:"customer_42"  swaggertype:"string"`
	ChainID int64  `json:"chain_id"           example:"1"            swaggertype:"integer"`
	Symbol  string `json:"symbol"             example:"USDT"         swaggertype:"string"`
	Label   string `json:"label,omitempty"    example:"Main wallet"  swaggertype:"string"`
}

// V1PayoutRequest is the request body for POST /api/v1/payout/create.
type V1PayoutRequest struct {
	Chain        string `json:"chain"                  example:"ethereum"                                      swaggertype:"string"`
	Symbol       string `json:"symbol,omitempty"       example:"USDT"                                          swaggertype:"string"`
	TokenAddress string `json:"token_address,omitempty" example:"0xdAC17F958D2ee523a2206206994597C13D831ec7" swaggertype:"string"`
	ToAddress    string `json:"to_address"             example:"0xAbCdEf1234567890AbCdEf1234567890AbCdEf12" swaggertype:"string"`
	Amount       string `json:"amount"                 example:"0.05"                                          swaggertype:"string"`
	Note         string `json:"note,omitempty"         example:"Monthly settlement"                           swaggertype:"string"`
}

// V1RefundRequest is the request body for POST /api/v1/refund/create.
type V1RefundRequest struct {
	PaymentID string `json:"payment_id,omitempty"  example:"550e8400-e29b-41d4-a716-446655440000"  swaggertype:"string"`
	OrderID   string `json:"order_id,omitempty"    example:"ORD-2024-001"                           swaggertype:"string"`
	AmountRaw string `json:"amount_raw,omitempty"  example:"25000000"                              swaggertype:"string"`
	Reason    string `json:"reason,omitempty"      example:"Customer requested refund"             swaggertype:"string"`
}

// ─── Common responses ─────────────────────────────────────────────────────────

// V1StatusResponse is returned by GET /api/v1/common/status.
type V1StatusResponse struct {
	Result string       `json:"result" example:"ok"`
	Data   V1StatusData `json:"data"`
}

type V1StatusData struct {
	Status  string `json:"status"   example:"operational"`
	Version string `json:"version"  example:"1.0.0"`
}

// V1BalanceResponse is returned by GET /api/v1/common/balance.
type V1BalanceResponse struct {
	Result string        `json:"result" example:"ok"`
	Data   []V1AssetItem `json:"data"`
}

type V1AssetItem struct {
	Chain        string `json:"chain"           example:"ethereum"`
	Symbol       string `json:"symbol"          example:"USDT"`
	TokenAddress string `json:"token_address"   example:"0xdAC17F958D2ee523a2206206994597C13D831ec7"`
	Balance      string `json:"balance"         example:"1250.00"`
	BalanceRaw   string `json:"balance_raw"     example:"1250000000"`
	Decimals     int    `json:"decimals"        example:"6"`
}

// V1PricesResponse is returned by GET /api/v1/common/prices.
type V1PricesResponse struct {
	Result   string        `json:"result"    example:"ok"`
	Currency string        `json:"currency"  example:"USD"`
	Data     []V1PriceItem `json:"data"`
}

type V1PriceItem struct {
	Symbol string `json:"symbol"  example:"BTC"`
	Price  string `json:"price"   example:"67430.21"`
}

// V1CurrencyItem represents a single supported crypto asset.
type V1CurrencyItem struct {
	Name         string `json:"name"          example:"Tether USD"`
	Symbol       string `json:"symbol"        example:"USDT"`
	Chain        string `json:"chain"         example:"ethereum"`
	ChainID      int64  `json:"chain_id"      example:"1"`
	Decimals     int    `json:"decimals"      example:"6"`
	TokenAddress string `json:"token_address" example:"0xdAC17F958D2ee523a2206206994597C13D831ec7"`
	LogoURL      string `json:"logo_url"      example:"https://assets.coingecko.com/coins/images/325/large/Tether.png"`
}

// V1CurrenciesResponse is returned by GET /api/v1/common/currencies and /payment/currencies.
type V1CurrenciesResponse struct {
	Result string           `json:"result" example:"ok"`
	Data   []V1CurrencyItem `json:"data"`
}

// V1AssetsResponse is returned by GET /api/v1/common/assets and /api/v1/payment/assets.
type V1AssetsResponse struct {
	Result string       `json:"result" example:"ok"`
	Data   V1AssetsData `json:"data"`
}

type V1AssetsData struct {
	Assets []V1AssetCatalogItem `json:"assets"`
}

type V1AssetCatalogItem struct {
	Symbol      string                  `json:"symbol"      example:"USDT"`
	Name        string                  `json:"name"        example:"Tether USD"`
	Type        string                  `json:"type"        example:"erc20"`
	Decimals    int                     `json:"decimals"    example:"6"`
	LogoURL     string                  `json:"logo_url"    example:"/static/coins/usdt.svg"`
	Deployments []V1AssetDeploymentItem `json:"deployments"`
}

type V1AssetDeploymentItem struct {
	Symbol       string `json:"symbol"         example:"USDT"`
	Name         string `json:"name"           example:"Tether USD"`
	Type         string `json:"type"           example:"erc20"`
	Chain        string `json:"chain"          example:"Ethereum"`
	Network      string `json:"network"        example:"ethereum"`
	ChainID      int64  `json:"chain_id"       example:"1"`
	Decimals     int    `json:"decimals"       example:"6"`
	Native       bool   `json:"native"         example:"false"`
	Enabled      bool   `json:"enabled"        example:"true"`
	Identifier   string `json:"identifier"     example:"0xdAC17F958D2ee523a2206206994597C13D831ec7"`
	TokenAddress string `json:"token_address"  example:"0xdAC17F958D2ee523a2206206994597C13D831ec7"`
	MintAddress  string `json:"mint_address"   example:""`
	LogoURL      string `json:"logo_url"       example:"/static/coins/usdt.svg"`
	ChainLogoURL string `json:"chain_logo_url" example:"/static/chains/ethereumchain.svg"`
}

// V1FiatCurrencyItem represents a supported fiat denomination.
type V1FiatCurrencyItem struct {
	Code   string `json:"code"    example:"USD"`
	Name   string `json:"name"    example:"US Dollar"`
	Symbol string `json:"symbol"  example:"$"`
}

// V1FiatCurrenciesResponse is returned by GET /api/v1/common/fiat-currencies.
type V1FiatCurrenciesResponse struct {
	Result string               `json:"result" example:"ok"`
	Data   []V1FiatCurrencyItem `json:"data"`
}

// V1NetworkItem represents a supported blockchain network.
type V1NetworkItem struct {
	ChainID  int64  `json:"chain_id"   example:"1"`
	Name     string `json:"name"       example:"Ethereum"`
	Slug     string `json:"slug"       example:"ethereum"`
	LogoURL  string `json:"logo_url"   example:"https://assets.coingecko.com/coins/images/279/large/ethereum.png"`
	Explorer string `json:"explorer"   example:"https://etherscan.io"`
}

// V1NetworksResponse is returned by GET /api/v1/common/networks.
type V1NetworksResponse struct {
	Result string          `json:"result" example:"ok"`
	Data   []V1NetworkItem `json:"data"`
}

// ─── Payment responses ────────────────────────────────────────────────────────

// V1PaymentCreateResponse is returned by POST /api/v1/payment/create and /white-label.
type V1PaymentCreateResponse struct {
	Result string               `json:"result" example:"ok"`
	Data   V1PaymentCreatedData `json:"data"`
}

type V1PaymentCreatedData struct {
	PaymentID    string `json:"payment_id"     example:"550e8400-e29b-41d4-a716-446655440000"`
	TrackID      string `json:"track_id"       example:"550e8400-e29b-41d4-a716-446655440000"`
	SessionToken string `json:"session_token"  example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"`
	CheckoutURL  string `json:"checkout_url"   example:"https://pay.example.com/checkout/eyJhbGci"`
	Status       string `json:"status"         example:"pending"`
	ExpiresAt    string `json:"expires_at"     example:"2024-06-01T12:30:00Z"`
	OrderID      string `json:"order_id"       example:"ORD-2024-001"`
	Amount       string `json:"amount"         example:"25.00"`
	Currency     string `json:"currency"       example:"USD"`
}

// V1PaymentInfoResponse is returned by GET /api/v1/payment/info.
type V1PaymentInfoResponse struct {
	Result string          `json:"result" example:"ok"`
	Data   V1PaymentDetail `json:"data"`
}

type V1PaymentDetail struct {
	PaymentID     string `json:"payment_id"      example:"550e8400-e29b-41d4-a716-446655440000"`
	TrackID       string `json:"track_id"        example:"550e8400-e29b-41d4-a716-446655440000"`
	OrderID       string `json:"order_id"        example:"ORD-2024-001"`
	Status        string `json:"status"          example:"paid"`
	Amount        string `json:"amount"          example:"25.00"`
	Currency      string `json:"currency"        example:"USD"`
	UserID        string `json:"user_id"         example:"customer_42"`
	SelectedAsset string `json:"selected_asset"  example:"USDT"`
	SelectedChain string `json:"selected_chain"  example:"ethereum"`
	DepositWallet string `json:"deposit_wallet"  example:"0xAbCdEf1234567890AbCdEf1234567890AbCdEf12"`
	TxHash        string `json:"tx_hash"         example:"0xabc123..."`
	PaidAt        string `json:"paid_at"         example:"2024-06-01T12:15:00Z"`
	ExpiresAt     string `json:"expires_at"      example:"2024-06-01T12:30:00Z"`
	CheckoutURL   string `json:"checkout_url"    example:"https://pay.example.com/checkout/eyJhbGci"`
	CreatedAt     string `json:"created_at"      example:"2024-06-01T12:00:00Z"`
}

// V1PaymentHistoryResponse is returned by GET /api/v1/payment/history.
type V1PaymentHistoryResponse struct {
	Result string            `json:"result" example:"ok"`
	Total  int64             `json:"total"  example:"48"`
	Page   int               `json:"page"   example:"1"`
	Limit  int               `json:"limit"  example:"20"`
	Data   []V1PaymentDetail `json:"data"`
}

// V1PaymentStatisticsResponse is returned by GET /api/v1/payment/statistics.
type V1PaymentStatisticsResponse struct {
	Result string                  `json:"result" example:"ok"`
	Data   V1PaymentStatisticsData `json:"data"`
}

type V1PaymentStatisticsData struct {
	Total           int64  `json:"total"             example:"120"`
	Paid            int64  `json:"paid"              example:"95"`
	Pending         int64  `json:"pending"           example:"12"`
	AwaitingPayment int64  `json:"awaiting_payment"  example:"8"`
	Expired         int64  `json:"expired"           example:"5"`
	TotalVolumeUSD  string `json:"total_volume_usd"  example:"48320.00"`
}

// V1StatusTableItem describes a single payment status.
type V1StatusTableItem struct {
	Status      string `json:"status"       example:"paid"`
	Description string `json:"description"  example:"Payment confirmed on-chain"`
	IsFinal     bool   `json:"is_final"     example:"true"`
}

// V1PaymentStatusTableResponse is returned by GET /api/v1/payment/status-table.
type V1PaymentStatusTableResponse struct {
	Result string              `json:"result" example:"ok"`
	Data   []V1StatusTableItem `json:"data"`
}

// V1StaticAddressResponse is returned by POST /api/v1/payment/static-address.
type V1StaticAddressResponse struct {
	Result string                `json:"result" example:"ok"`
	Data   V1StaticAddressDetail `json:"data"`
}

type V1StaticAddressDetail struct {
	WalletID string `json:"wallet_id"  example:"550e8400-e29b-41d4-a716-446655440000"`
	UserID   string `json:"user_id"    example:"customer_42"`
	Chain    string `json:"chain"      example:"ethereum"`
	Symbol   string `json:"symbol"     example:"USDT"`
	Address  string `json:"address"    example:"0xAbCdEf1234567890AbCdEf1234567890AbCdEf12"`
	Label    string `json:"label"      example:"Main wallet"`
}

// V1StaticAddressListResponse is returned by GET /api/v1/payment/static-addresses.
type V1StaticAddressListResponse struct {
	Result string                  `json:"result" example:"ok"`
	Data   []V1StaticAddressDetail `json:"data"`
}

// ─── Payout responses ─────────────────────────────────────────────────────────

// V1PayoutCreateResponse is returned by POST /api/v1/payout/create.
type V1PayoutCreateResponse struct {
	Result string         `json:"result" example:"ok"`
	Data   V1PayoutDetail `json:"data"`
}

type V1PayoutDetail struct {
	PayoutID     string `json:"payout_id"     example:"550e8400-e29b-41d4-a716-446655440000"`
	DomainID     string `json:"domain_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
	Chain        string `json:"chain"         example:"ethereum"`
	Symbol       string `json:"symbol"        example:"USDT"`
	TokenAddress string `json:"token_address" example:"0xdAC17F958D2ee523a2206206994597C13D831ec7"`
	ToAddress    string `json:"to_address"    example:"0xAbCdEf1234567890AbCdEf1234567890AbCdEf12"`
	AmountRaw    string `json:"amount_raw"    example:"50000000"`
	Status       string `json:"status"        example:"pending"`
	Note         string `json:"note"          example:"Monthly settlement"`
	TxHash       string `json:"tx_hash"       example:""`
	CreatedAt    string `json:"created_at"    example:"2024-06-01T12:00:00Z"`
}

// V1PayoutInfoResponse is returned by GET /api/v1/payout/info.
type V1PayoutInfoResponse struct {
	Result string         `json:"result" example:"ok"`
	Data   V1PayoutDetail `json:"data"`
}

// V1PayoutHistoryResponse is returned by GET /api/v1/payout/history.
type V1PayoutHistoryResponse struct {
	Result string           `json:"result" example:"ok"`
	Total  int64            `json:"total"  example:"12"`
	Page   int              `json:"page"   example:"1"`
	Limit  int              `json:"limit"  example:"20"`
	Data   []V1PayoutDetail `json:"data"`
}

// V1PayoutStatusTableItem describes a single payout status.
type V1PayoutStatusTableItem struct {
	Status      string `json:"status"       example:"pending"`
	Description string `json:"description"  example:"Awaiting admin approval"`
	IsFinal     bool   `json:"is_final"     example:"false"`
}

// V1PayoutStatusTableResponse is returned by GET /api/v1/payout/status-table.
type V1PayoutStatusTableResponse struct {
	Result string                    `json:"result" example:"ok"`
	Data   []V1PayoutStatusTableItem `json:"data"`
}

// ─── Refund responses ─────────────────────────────────────────────────────────

// V1RefundDetail holds refund request details.
type V1RefundDetail struct {
	RefundID  string `json:"refund_id"   example:"550e8400-e29b-41d4-a716-446655440000"`
	PaymentID string `json:"payment_id"  example:"550e8400-e29b-41d4-a716-446655440001"`
	OrderID   string `json:"order_id"    example:"ORD-2024-001"`
	AmountRaw string `json:"amount_raw"  example:"25000000"`
	Status    string `json:"status"      example:"pending"`
	Reason    string `json:"reason"      example:"Customer requested refund"`
	TxHash    string `json:"tx_hash"     example:""`
	CreatedAt string `json:"created_at"  example:"2024-06-01T12:00:00Z"`
}

// V1RefundCreateResponse is returned by POST /api/v1/refund/create.
type V1RefundCreateResponse struct {
	Result string         `json:"result" example:"ok"`
	Data   V1RefundDetail `json:"data"`
}

// V1RefundInfoResponse is returned by GET /api/v1/refund/info.
type V1RefundInfoResponse struct {
	Result string         `json:"result" example:"ok"`
	Data   V1RefundDetail `json:"data"`
}

// V1RefundHistoryResponse is returned by GET /api/v1/refund/history.
type V1RefundHistoryResponse struct {
	Result string           `json:"result" example:"ok"`
	Total  int64            `json:"total"  example:"3"`
	Page   int              `json:"page"   example:"1"`
	Limit  int              `json:"limit"  example:"20"`
	Data   []V1RefundDetail `json:"data"`
}

// V1ErrorResponse is the standard error envelope for all v1 endpoints.
type V1ErrorResponse struct {
	Result  string `json:"result"  example:"error"`
	Message string `json:"message" example:"X-API-Key header is required"`
}
