package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"core/constants"
	"core/helpers"
	"core/models"
)

type Notifier struct {
	client *http.Client
}

type permanentError struct {
	err error
}

func (e permanentError) Error() string {
	return e.err.Error()
}

func (e permanentError) Unwrap() error {
	return e.err
}

func (e permanentError) Permanent() bool {
	return true
}

func permanent(err error) error {
	return permanentError{err: err}
}

func IsPermanent(err error) bool {
	var permanentErr interface {
		Permanent() bool
	}
	return errors.As(err, &permanentErr) && permanentErr.Permanent()
}

type Payload struct {
	EventID       string `json:"event_id"`
	EventType     string `json:"event_type"`
	EventVersion  string `json:"event_version"`
	TransactionID string `json:"transaction_id"`

	MerchantID string `json:"merchant_id"`
	DomainID   string `json:"domain_id"`
	ProductID  string `json:"product_id"`
	UserID     string `json:"user_id"`
	WalletID   string `json:"wallet_id"`

	ChainID            int64   `json:"chain_id"`
	Hash               string  `json:"hash"`
	LogIndex           *string `json:"log_index,omitempty"`
	BlockNumber        string  `json:"block_number"`
	BlockHash          string  `json:"block_hash"`
	Token              *string `json:"token,omitempty"`
	Symbol             string  `json:"symbol"`
	Decimals           uint8   `json:"decimals"`
	From               string  `json:"from"`
	To                 string  `json:"to"`
	AmountRaw          string  `json:"amount_raw"`
	Status             string  `json:"status"`
	OriginalEventID    string  `json:"original_event_id,omitempty"`
	OriginalResourceID string  `json:"original_resource_id,omitempty"`
	CorrectionReason   string  `json:"correction_reason,omitempty"`
	CreatedAt          string  `json:"created_at"`
}

type PaymentPayload struct {
	EventID      string `json:"event_id"`
	EventType    string `json:"event_type"`
	EventVersion string `json:"event_version"`

	PaymentID    string `json:"payment_id"`
	SessionToken string `json:"session_token"`
	OrderID      string `json:"order_id"`
	Status       string `json:"status"`

	MerchantID string `json:"merchant_id"`
	DomainID   string `json:"domain_id"`
	ProductID  string `json:"product_id"`
	UserID     string `json:"user_id"`
	WalletID   string `json:"wallet_id"`

	Amount             string  `json:"amount"`
	Currency           string  `json:"currency"`
	ChainID            *int64  `json:"chain_id,omitempty"`
	Symbol             string  `json:"symbol,omitempty"`
	Token              *string `json:"token,omitempty"`
	Decimals           uint8   `json:"decimals,omitempty"`
	ExpectedAmountRaw  string  `json:"expected_amount_raw,omitempty"`
	DepositAddress     string  `json:"deposit_address,omitempty"`
	PaymentOutcome     string  `json:"payment_outcome,omitempty"`
	OutcomeReason      string  `json:"payment_outcome_reason,omitempty"`
	MatchedAmountRaw   string  `json:"matched_amount_raw,omitempty"`
	ShortfallAmountRaw string  `json:"shortfall_amount_raw,omitempty"`
	ExcessAmountRaw    string  `json:"excess_amount_raw,omitempty"`
	TxHash             *string `json:"tx_hash,omitempty"`
	TxUniqueHash       *string `json:"tx_unique_hash,omitempty"`
	OriginalEventID    string  `json:"original_event_id,omitempty"`
	OriginalResourceID string  `json:"original_resource_id,omitempty"`
	CorrectionReason   string  `json:"correction_reason,omitempty"`
	CreatedAt          string  `json:"created_at"`
	PaidAt             *string `json:"paid_at,omitempty"`
}

func NewNotifier() *Notifier {
	return &Notifier{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (n *Notifier) Deliver(ctx context.Context, domain models.Domain, tx models.Transaction) error {
	if domain.WebhookURL == "" {
		return permanent(fmt.Errorf("webhook url is empty for domain %s", domain.ID.String()))
	}
	if domain.WebhookSecret == "" {
		return permanent(fmt.Errorf("webhook secret is empty for domain %s", domain.ID.String()))
	}
	if err := helpers.ValidateWebhookURL(domain.WebhookURL); err != nil {
		return permanent(fmt.Errorf("webhook url validation failed for domain %s: %w", domain.ID.String(), err))
	}

	payload := Payload{
		EventID:            TransactionEventID(tx),
		EventType:          tx.EventType,
		EventVersion:       "v1",
		TransactionID:      tx.ID.String(),
		ProductID:          tx.ProductID,
		UserID:             tx.UserID,
		ChainID:            int64(tx.ChainID),
		Hash:               tx.Hash,
		LogIndex:           tx.LogIndex,
		BlockNumber:        tx.BlockNumber,
		BlockHash:          tx.BlockHash,
		Token:              tx.Token,
		Symbol:             tx.Symbol,
		Decimals:           tx.Decimals,
		From:               tx.FromAddress,
		To:                 tx.ToAddress,
		AmountRaw:          tx.Amount,
		Status:             tx.Status,
		OriginalEventID:    tx.OriginalEventID,
		OriginalResourceID: tx.OriginalResourceID,
		CorrectionReason:   tx.CorrectionReason,
		CreatedAt:          tx.CreatedAt.UTC().Format(time.RFC3339Nano),
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

	secret, err := helpers.DecryptSecret(domain.WebhookSecret)
	if err != nil {
		return permanent(fmt.Errorf("webhook secret decrypt failed for domain %s: %w", domain.ID, err))
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := helpers.GenerateSignature(secret, timestamp, body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, domain.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gateway-webhook/1.0")
	req.Header.Set("X-Gateway-Event", tx.EventType)
	req.Header.Set("X-Gateway-Event-Version", "v1")
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
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, SanitizeDeliveryText(string(respBody)))
	}

	return nil
}

func (n *Notifier) DeliverPayment(ctx context.Context, domain models.Domain, session models.PaymentSession) error {
	if domain.WebhookURL == "" {
		return permanent(fmt.Errorf("webhook url is empty for domain %s", domain.ID.String()))
	}
	if domain.WebhookSecret == "" {
		return permanent(fmt.Errorf("webhook secret is empty for domain %s", domain.ID.String()))
	}
	if err := helpers.ValidateWebhookURL(domain.WebhookURL); err != nil {
		return permanent(fmt.Errorf("webhook url validation failed for domain %s: %w", domain.ID.String(), err))
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
	originalEventID, originalResourceID, correctionReason := paymentCorrectionRelation(session)

	payload := PaymentPayload{
		EventID:            PaymentEventID(session),
		EventType:          session.WebhookEvent,
		EventVersion:       "v1",
		PaymentID:          session.ID.String(),
		SessionToken:       session.SessionToken,
		OrderID:            session.OrderID,
		Status:             session.Status,
		MerchantID:         session.MerchantID.String(),
		DomainID:           session.DomainID.String(),
		ProductID:          session.ProductID,
		UserID:             session.UserID,
		WalletID:           session.WalletID.String(),
		Amount:             session.Amount,
		Currency:           session.Currency,
		ChainID:            chainID,
		Symbol:             session.SelectedSymbol,
		Token:              session.SelectedToken,
		Decimals:           session.SelectedDecimals,
		ExpectedAmountRaw:  session.ExpectedAmountRaw,
		DepositAddress:     session.DepositAddress,
		PaymentOutcome:     session.PaymentOutcome,
		OutcomeReason:      session.PaymentOutcomeReason,
		MatchedAmountRaw:   session.MatchedAmountRaw,
		ShortfallAmountRaw: session.ShortfallAmountRaw,
		ExcessAmountRaw:    session.ExcessAmountRaw,
		TxHash:             session.TxHash,
		TxUniqueHash:       session.TxUniqueHash,
		OriginalEventID:    originalEventID,
		OriginalResourceID: originalResourceID,
		CorrectionReason:   correctionReason,
		CreatedAt:          session.CreatedAt.UTC().Format(time.RFC3339Nano),
		PaidAt:             paidAt,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	secret, err := helpers.DecryptSecret(domain.WebhookSecret)
	if err != nil {
		return permanent(fmt.Errorf("webhook secret decrypt failed for domain %s: %w", domain.ID, err))
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := helpers.GenerateSignature(secret, timestamp, body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, domain.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gateway-webhook/1.0")
	req.Header.Set("X-Gateway-Event", session.WebhookEvent)
	req.Header.Set("X-Gateway-Event-Version", "v1")
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
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, SanitizeDeliveryText(string(respBody)))
	}

	return nil
}

func paymentCorrectionRelation(session models.PaymentSession) (string, string, string) {
	if session.PaymentOutcomeReason != models.PaymentOutcomeReasonReorged {
		return "", "", ""
	}
	originalEventType := paymentOriginalEventType(session)
	if originalEventType == "" {
		return "", session.ID.String(), session.PaymentOutcomeReason
	}
	return session.ID.String() + ":" + originalEventType, session.ID.String(), session.PaymentOutcomeReason
}

func paymentOriginalEventType(session models.PaymentSession) string {
	switch session.PaymentOutcome {
	case models.PaymentOutcomeExact:
		return constants.WebhookEventPaymentSucceeded
	case models.PaymentOutcomeUnderpaid:
		return constants.WebhookEventPaymentUnderpaid
	case models.PaymentOutcomeOverpaid:
		return constants.WebhookEventPaymentOverpaid
	case models.PaymentOutcomePartialUnsupported:
		return constants.WebhookEventPaymentPartialPaid
	case models.PaymentOutcomeExpiredAfterDeposit:
		return constants.WebhookEventPaymentExpired
	case models.PaymentOutcomeWrongAsset, models.PaymentOutcomeWrongChain:
		return constants.WebhookEventPaymentFailed
	default:
		return ""
	}
}

func (n *Notifier) DeliverRaw(ctx context.Context, domain models.Domain, eventType, eventID, eventVersion string, body []byte) error {
	if domain.WebhookURL == "" {
		return permanent(fmt.Errorf("webhook url is empty for domain %s", domain.ID.String()))
	}
	if domain.WebhookSecret == "" {
		return permanent(fmt.Errorf("webhook secret is empty for domain %s", domain.ID.String()))
	}
	if err := helpers.ValidateWebhookURL(domain.WebhookURL); err != nil {
		return permanent(fmt.Errorf("webhook url validation failed for domain %s: %w", domain.ID.String(), err))
	}
	if eventVersion == "" {
		eventVersion = constants.WebhookEventVersionV1
	}

	secret, err := helpers.DecryptSecret(domain.WebhookSecret)
	if err != nil {
		return permanent(fmt.Errorf("webhook secret decrypt failed for domain %s: %w", domain.ID, err))
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := helpers.GenerateSignature(secret, timestamp, body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, domain.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gateway-webhook/1.0")
	req.Header.Set("X-Gateway-Event", eventType)
	req.Header.Set("X-Gateway-Event-Version", eventVersion)
	req.Header.Set("X-Gateway-Event-Id", eventID)
	req.Header.Set("X-Gateway-Timestamp", timestamp)
	req.Header.Set("X-Gateway-Signature", "sha256="+signature)

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, SanitizeDeliveryText(string(respBody)))
	}

	return nil
}
