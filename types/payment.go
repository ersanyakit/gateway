package types

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

type PaymentCreateParams struct {
	Context context.Context `json:"-"`

	DomainID    *string `json:"domain_id,omitempty"`
	MerchantID  *string `json:"merchant_id,omitempty"`
	OrderID     *string `json:"order_id,omitempty"`
	ProductID   *string `json:"product_id,omitempty"`
	UserID      *string `json:"user_id,omitempty"`
	Amount      *string `json:"amount,omitempty"`
	Currency    *string `json:"currency,omitempty"`
	Description *string `json:"description,omitempty"`
	ChainID     *int64  `json:"chain_id,omitempty"`
	Symbol      *string `json:"symbol,omitempty"`
	Token       *string `json:"token,omitempty"`
	SuccessURL  *string `json:"success_url,omitempty"`
	CancelURL   *string `json:"cancel_url,omitempty"`
}

type PaymentSelectAssetParams struct {
	Context context.Context `json:"-"`

	ChainID *int64  `json:"chain_id,omitempty" form:"chain_id"`
	Symbol  *string `json:"symbol,omitempty" form:"symbol"`
	Token   *string `json:"token,omitempty" form:"token"`
}

type PaymentCreateResponse struct {
	Success           bool   `json:"success"`
	PaymentID         string `json:"payment_id"`
	SessionToken      string `json:"session_token"`
	CheckoutURL       string `json:"checkout_url"`
	Status            string `json:"status"`
	ExpiresAt         string `json:"expires_at"`
	DepositWallet     string `json:"deposit_wallet"`
	ChainID           *int64 `json:"chain_id,omitempty"`
	Symbol            string `json:"symbol,omitempty"`
	Token             string `json:"token,omitempty"`
	Decimals          uint8  `json:"decimals,omitempty"`
	ExpectedAmountRaw string `json:"expected_amount_raw,omitempty"`
	DepositAddress    string `json:"deposit_address,omitempty"`
}

type PaymentStatusResponse struct {
	Success     bool   `json:"success"`
	Status      string `json:"status"`
	Paid        bool   `json:"paid"`
	SuccessPath string `json:"success_path"`
	CancelPath  string `json:"cancel_path"`
}

type ErrorResponse struct {
	Success bool        `json:"success"`
	Error   string      `json:"error,omitempty"`
	Errors  interface{} `json:"errors,omitempty"`
}

func (p *PaymentCreateParams) Validate() error {
	if p.OrderID == nil || strings.TrimSpace(*p.OrderID) == "" {
		return errors.New("OrderID is required")
	}
	if p.Amount == nil || strings.TrimSpace(*p.Amount) == "" {
		return errors.New("Amount is required")
	}
	if err := ValidatePositiveDecimal(strings.TrimSpace(*p.Amount)); err != nil {
		return errors.New("Amount must be a positive decimal")
	}
	if p.Currency == nil || strings.TrimSpace(*p.Currency) == "" {
		return errors.New("Currency is required")
	}
	if p.DomainID != nil && strings.TrimSpace(*p.DomainID) != "" {
		if _, err := uuid.Parse(strings.TrimSpace(*p.DomainID)); err != nil {
			return errors.New("invalid DomainID format")
		}
	}
	if p.MerchantID != nil && strings.TrimSpace(*p.MerchantID) != "" {
		if _, err := uuid.Parse(strings.TrimSpace(*p.MerchantID)); err != nil {
			return errors.New("invalid MerchantID format")
		}
	}
	if p.SuccessURL != nil && strings.TrimSpace(*p.SuccessURL) != "" {
		if _, err := url.ParseRequestURI(strings.TrimSpace(*p.SuccessURL)); err != nil {
			return errors.New("invalid SuccessURL")
		}
	}
	if p.CancelURL != nil && strings.TrimSpace(*p.CancelURL) != "" {
		if _, err := url.ParseRequestURI(strings.TrimSpace(*p.CancelURL)); err != nil {
			return errors.New("invalid CancelURL")
		}
	}

	trim := func(value *string) *string {
		if value == nil {
			return nil
		}
		v := strings.TrimSpace(*value)
		if v == "" {
			return nil
		}
		return &v
	}

	p.DomainID = trim(p.DomainID)
	p.MerchantID = trim(p.MerchantID)
	p.OrderID = trim(p.OrderID)
	p.ProductID = trim(p.ProductID)
	p.UserID = trim(p.UserID)
	p.Amount = trim(p.Amount)
	if p.Currency != nil {
		currency := strings.ToUpper(strings.TrimSpace(*p.Currency))
		p.Currency = &currency
	}
	p.Description = trim(p.Description)
	assetRequested := p.ChainID != nil || (p.Symbol != nil && strings.TrimSpace(*p.Symbol) != "") || (p.Token != nil && strings.TrimSpace(*p.Token) != "")
	if assetRequested {
		if p.ChainID == nil {
			return errors.New("ChainID is required when selecting an asset")
		}
		if *p.ChainID < 0 {
			return errors.New("invalid ChainID")
		}
		if p.Symbol == nil || strings.TrimSpace(*p.Symbol) == "" {
			return errors.New("Symbol is required when selecting an asset")
		}
		symbol := strings.ToUpper(strings.TrimSpace(*p.Symbol))
		p.Symbol = &symbol
		if p.Token != nil {
			token := strings.TrimSpace(*p.Token)
			if token == "" {
				p.Token = nil
			} else {
				p.Token = &token
			}
		}
	} else {
		p.Symbol = nil
		p.Token = nil
	}
	p.SuccessURL = trim(p.SuccessURL)
	p.CancelURL = trim(p.CancelURL)
	return nil
}

func ValidatePositiveDecimal(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") {
		return errors.New("invalid amount")
	}
	if strings.Count(value, ".") > 1 {
		return errors.New("invalid amount")
	}
	digits := strings.ReplaceAll(value, ".", "")
	if digits == "" {
		return errors.New("invalid amount")
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return errors.New("invalid amount")
		}
	}
	if strings.Trim(digits, "0") == "" {
		return errors.New("amount must be greater than zero")
	}
	return nil
}

func (p *PaymentSelectAssetParams) Validate() error {
	if p.ChainID == nil {
		return errors.New("ChainID is required")
	}
	if p.Symbol == nil || strings.TrimSpace(*p.Symbol) == "" {
		return errors.New("Symbol is required")
	}

	symbol := strings.ToUpper(strings.TrimSpace(*p.Symbol))
	p.Symbol = &symbol
	if p.Token != nil {
		token := strings.TrimSpace(*p.Token)
		if token == "" {
			p.Token = nil
		} else {
			p.Token = &token
		}
	}
	return nil
}

func DecimalToRaw(value string, decimals uint8) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") {
		return "", errors.New("invalid amount")
	}

	parts := strings.SplitN(value, ".", 2)
	whole := parts[0]
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if whole == "" {
		whole = "0"
	}
	if strings.ContainsAny(whole+fraction, "+- ") {
		return "", errors.New("invalid amount")
	}
	for _, r := range whole + fraction {
		if r < '0' || r > '9' {
			return "", errors.New("invalid amount")
		}
	}
	if len(fraction) > int(decimals) {
		return "", errors.New("amount has more decimals than selected asset")
	}
	for len(fraction) < int(decimals) {
		fraction += "0"
	}

	raw := strings.TrimLeft(whole+fraction, "0")
	if raw == "" {
		raw = "0"
	}
	if raw == "0" {
		return "", errors.New("amount must be greater than zero")
	}
	return raw, nil
}
