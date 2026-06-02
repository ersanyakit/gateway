package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"core/helpers"
	"core/models"
)

type Notifier struct {
	client *http.Client
}

type Payload struct {
	EventID       string `json:"event_id"`
	EventType     string `json:"event_type"`
	TransactionID string `json:"transaction_id"`

	MerchantID string `json:"merchant_id"`
	DomainID   string `json:"domain_id"`
	ProductID  string `json:"product_id"`
	UserID     string `json:"user_id"`
	WalletID   string `json:"wallet_id"`

	ChainID     int64   `json:"chain_id"`
	Hash        string  `json:"hash"`
	LogIndex    *string `json:"log_index,omitempty"`
	BlockNumber string  `json:"block_number"`
	BlockHash   string  `json:"block_hash"`
	Token       *string `json:"token,omitempty"`
	Symbol      string  `json:"symbol"`
	Decimals    uint8   `json:"decimals"`
	From        string  `json:"from"`
	To          string  `json:"to"`
	AmountRaw   string  `json:"amount_raw"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
}

type PaymentPayload struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`

	PaymentID    string `json:"payment_id"`
	SessionToken string `json:"session_token"`
	OrderID      string `json:"order_id"`
	Status       string `json:"status"`

	MerchantID string `json:"merchant_id"`
	DomainID   string `json:"domain_id"`
	ProductID  string `json:"product_id"`
	UserID     string `json:"user_id"`
	WalletID   string `json:"wallet_id"`

	Amount            string  `json:"amount"`
	Currency          string  `json:"currency"`
	ChainID           *int64  `json:"chain_id,omitempty"`
	Symbol            string  `json:"symbol,omitempty"`
	Token             *string `json:"token,omitempty"`
	Decimals          uint8   `json:"decimals,omitempty"`
	ExpectedAmountRaw string  `json:"expected_amount_raw,omitempty"`
	DepositAddress    string  `json:"deposit_address,omitempty"`
	TxHash            *string `json:"tx_hash,omitempty"`
	TxUniqueHash      *string `json:"tx_unique_hash,omitempty"`
	CreatedAt         string  `json:"created_at"`
	PaidAt            *string `json:"paid_at,omitempty"`
}

func NewNotifier() *Notifier {
	return &Notifier{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (n *Notifier) Deliver(ctx context.Context, domain models.Domain, tx models.Transaction) error {
	if domain.WebhookURL == "" {
		return fmt.Errorf("webhook url is empty for domain %s", domain.ID.String())
	}
	if domain.WebhookSecret == "" {
		return fmt.Errorf("webhook secret is empty for domain %s", domain.ID.String())
	}

	payload := Payload{
		EventID:       tx.UniqueHash,
		EventType:     tx.EventType,
		TransactionID: tx.ID.String(),
		ProductID:     tx.ProductID,
		UserID:        tx.UserID,
		ChainID:       int64(tx.ChainID),
		Hash:          tx.Hash,
		LogIndex:      tx.LogIndex,
		BlockNumber:   tx.BlockNumber,
		BlockHash:     tx.BlockHash,
		Token:         tx.Token,
		Symbol:        tx.Symbol,
		Decimals:      tx.Decimals,
		From:          tx.FromAddress,
		To:            tx.ToAddress,
		AmountRaw:     tx.Amount,
		Status:        tx.Status,
		CreatedAt:     tx.CreatedAt.UTC().Format(time.RFC3339Nano),
	}

	if tx.MerchantID != nil {
		payload.MerchantID = tx.MerchantID.String()
	}
	if tx.DomainID != nil {
		payload.DomainID = tx.DomainID.String()
	}
	if tx.WalletID != nil {
		payload.WalletID = tx.WalletID.String()
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := helpers.GenerateSignature(domain.WebhookSecret, timestamp, body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, domain.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gateway-webhook/1.0")
	req.Header.Set("X-Gateway-Event", tx.EventType)
	req.Header.Set("X-Gateway-Event-Id", tx.UniqueHash)
	req.Header.Set("X-Gateway-Timestamp", timestamp)
	req.Header.Set("X-Gateway-Signature", "sha256="+signature)

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (n *Notifier) DeliverPayment(ctx context.Context, domain models.Domain, session models.PaymentSession) error {
	if domain.WebhookURL == "" {
		return fmt.Errorf("webhook url is empty for domain %s", domain.ID.String())
	}
	if domain.WebhookSecret == "" {
		return fmt.Errorf("webhook secret is empty for domain %s", domain.ID.String())
	}

	var chainID *int64
	if session.SelectedChainID != nil {
		value := int64(*session.SelectedChainID)
		chainID = &value
	}
	var paidAt *string
	if session.PaidAt != nil {
		value := session.PaidAt.UTC().Format(time.RFC3339Nano)
		paidAt = &value
	}

	payload := PaymentPayload{
		EventID:           session.ID.String() + ":" + session.WebhookEvent,
		EventType:         session.WebhookEvent,
		PaymentID:         session.ID.String(),
		SessionToken:      session.SessionToken,
		OrderID:           session.OrderID,
		Status:            session.Status,
		MerchantID:        session.MerchantID.String(),
		DomainID:          session.DomainID.String(),
		ProductID:         session.ProductID,
		UserID:            session.UserID,
		WalletID:          session.WalletID.String(),
		Amount:            session.Amount,
		Currency:          session.Currency,
		ChainID:           chainID,
		Symbol:            session.SelectedSymbol,
		Token:             session.SelectedToken,
		Decimals:          session.SelectedDecimals,
		ExpectedAmountRaw: session.ExpectedAmountRaw,
		DepositAddress:    session.DepositAddress,
		TxHash:            session.TxHash,
		TxUniqueHash:      session.TxUniqueHash,
		CreatedAt:         session.CreatedAt.UTC().Format(time.RFC3339Nano),
		PaidAt:            paidAt,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := helpers.GenerateSignature(domain.WebhookSecret, timestamp, body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, domain.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gateway-webhook/1.0")
	req.Header.Set("X-Gateway-Event", session.WebhookEvent)
	req.Header.Set("X-Gateway-Event-Id", payload.EventID)
	req.Header.Set("X-Gateway-Timestamp", timestamp)
	req.Header.Set("X-Gateway-Signature", "sha256="+signature)

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
