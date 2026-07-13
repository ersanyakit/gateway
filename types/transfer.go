package types

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/google/uuid"
)

type TransferParams struct {
	Context context.Context `json:"-"`

	WalletID  *string `json:"wallet_id,omitempty"`
	Chain     *string `json:"chain,omitempty"`
	Token     *string `json:"token,omitempty"`
	ToAddress *string `json:"to_address,omitempty"`
	AmountRaw *string `json:"amount_raw,omitempty"`

	ActorID       string `json:"-"`
	JobID         string `json:"-"`
	CorrelationID string `json:"-"`
}

func (p *TransferParams) ValidateWithdraw() error {
	if err := p.validateBase(); err != nil {
		return err
	}
	if p.ToAddress == nil || strings.TrimSpace(*p.ToAddress) == "" {
		return errors.New("ToAddress is required")
	}
	if p.AmountRaw == nil || strings.TrimSpace(*p.AmountRaw) == "" {
		return errors.New("AmountRaw is required")
	}
	amountRaw := strings.TrimSpace(*p.AmountRaw)
	if strings.HasPrefix(amountRaw, "-") || strings.Contains(amountRaw, ".") {
		return errors.New("AmountRaw must be a positive integer")
	}
	amount, ok := new(big.Int).SetString(amountRaw, 10)
	if !ok || amount.Sign() <= 0 {
		return errors.New("AmountRaw must be greater than zero")
	}

	toAddress := strings.TrimSpace(*p.ToAddress)
	if p.Token != nil {
		token := strings.TrimSpace(*p.Token)
		if token == "" {
			p.Token = nil
		} else {
			p.Token = &token
		}
	}
	p.ToAddress = &toAddress
	p.AmountRaw = &amountRaw
	return nil
}

func (p *TransferParams) ValidateSweep() error {
	return p.validateBase()
}

func (p *TransferParams) validateBase() error {
	if p.WalletID == nil || strings.TrimSpace(*p.WalletID) == "" {
		return errors.New("WalletID is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(*p.WalletID)); err != nil {
		return errors.New("invalid WalletID format")
	}
	if p.Chain == nil || strings.TrimSpace(*p.Chain) == "" {
		return errors.New("Chain is required")
	}

	walletID := strings.TrimSpace(*p.WalletID)
	chain := strings.ToLower(strings.TrimSpace(*p.Chain))
	p.WalletID = &walletID
	p.Chain = &chain
	return nil
}
