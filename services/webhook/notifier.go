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
	"strings"
	"time"

	"core/constants"
	"core/helpers"
	"core/models"

	"github.com/nats-io/nats.go"
)

type natsConnection interface {
	Publish(subject string, data []byte) error
	FlushTimeout(timeout time.Duration) error
	Close()
}

type Notifier struct {
	client      *http.Client
	natsConnect func(string) (natsConnection, error)
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
	EventID        string `json:"event_id"`
	EventType      string `json:"event_type"`
	EventVersion   string `json:"event_version"`
	TransactionID  string `json:"transaction_id"`
	DeliveryID     string `json:"delivery_id,omitempty"`
	ResourceType   string `json:"resource_type,omitempty"`
	ResourceID     string `json:"resource_id,omitempty"`
	Sequence       int64  `json:"sequence,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`

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
	EventID        string `json:"event_id"`
	EventType      string `json:"event_type"`
	EventVersion   string `json:"event_version"`
	DeliveryID     string `json:"delivery_id,omitempty"`
	ResourceType   string `json:"resource_type,omitempty"`
	ResourceID     string `json:"resource_id,omitempty"`
	Sequence       int64  `json:"sequence,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	PaymentID    string `json:"payment_id"`
	SessionToken string `json:"session_token"`
	OrderID      string `json:"order_id"`
	Status       string `json:"status"`

	MerchantID string `json:"merchant_id"`
	DomainID   string `json:"domain_id"`
	ProductID  string `json:"product_id"`
	UserID     string `json:"user_id"`
	WalletID   string `json:"wallet_id"`

	Amount             string                         `json:"amount"`
	Currency           string                         `json:"currency"`
	LinkType           string                         `json:"link_type,omitempty"`
	ChainID            *int64                         `json:"chain_id,omitempty"`
	Symbol             string                         `json:"symbol,omitempty"`
	Token              *string                        `json:"token,omitempty"`
	Decimals           uint8                          `json:"decimals,omitempty"`
	ExpectedAmountRaw  string                         `json:"expected_amount_raw,omitempty"`
	DepositAddress     string                         `json:"deposit_address,omitempty"`
	PaymentOutcome     string                         `json:"payment_outcome,omitempty"`
	OutcomeReason      string                         `json:"payment_outcome_reason,omitempty"`
	MatchedAmountRaw   string                         `json:"matched_amount_raw,omitempty"`
	ShortfallAmountRaw string                         `json:"shortfall_amount_raw,omitempty"`
	ExcessAmountRaw    string                         `json:"excess_amount_raw,omitempty"`
	Product            *models.PaymentProductSnapshot `json:"product,omitempty"`
	TxHash             *string                        `json:"tx_hash,omitempty"`
	TxUniqueHash       *string                        `json:"tx_unique_hash,omitempty"`
	OriginalEventID    string                         `json:"original_event_id,omitempty"`
	OriginalResourceID string                         `json:"original_resource_id,omitempty"`
	CorrectionReason   string                         `json:"correction_reason,omitempty"`
	CreatedAt          string                         `json:"created_at"`
	PaidAt             *string                        `json:"paid_at,omitempty"`
}

type DeliveryMetadata struct {
	DeliveryID         string
	OriginalDeliveryID string
	ResourceType       string
	ResourceID         string
	Sequence           int64
	IdempotencyKey     string
	ReplayCount        uint
}

func NewNotifier() *Notifier {
	return &Notifier{
		client: &http.Client{Timeout: 15 * time.Second},
		natsConnect: func(server string) (natsConnection, error) {
			return nats.Connect(server, nats.Timeout(10*time.Second))
		},
	}
}

func (n *Notifier) Deliver(ctx context.Context, domain models.Domain, tx models.Transaction) error {
	return n.DeliverWithMetadata(ctx, domain, tx, DeliveryMetadata{})
}

func (n *Notifier) DeliverWithMetadata(ctx context.Context, domain models.Domain, tx models.Transaction, metadata DeliveryMetadata) error {
	payload := Payload{
		EventID:            TransactionEventID(tx),
		EventType:          tx.EventType,
		EventVersion:       "v1",
		TransactionID:      tx.ID.String(),
		DeliveryID:         metadata.DeliveryID,
		ResourceType:       metadata.ResourceType,
		ResourceID:         metadata.ResourceID,
		Sequence:           metadata.Sequence,
		IdempotencyKey:     firstNonEmpty(metadata.IdempotencyKey, TransactionEventID(tx)),
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
	return n.deliverPayload(ctx, domain, tx.EventType, payload.EventID, "v1", body, metadata)
}

func (n *Notifier) DeliverPayment(ctx context.Context, domain models.Domain, session models.PaymentSession) error {
	return n.DeliverPaymentWithMetadata(ctx, domain, session, DeliveryMetadata{})
}

func (n *Notifier) DeliverPaymentWithMetadata(ctx context.Context, domain models.Domain, session models.PaymentSession, metadata DeliveryMetadata) error {
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
	productSnapshot, err := paymentProductSnapshot(session.ProductSnapshot)
	if err != nil {
		return err
	}

	payload := PaymentPayload{
		EventID:            PaymentEventID(session),
		EventType:          session.WebhookEvent,
		EventVersion:       "v1",
		DeliveryID:         metadata.DeliveryID,
		ResourceType:       metadata.ResourceType,
		ResourceID:         metadata.ResourceID,
		Sequence:           metadata.Sequence,
		IdempotencyKey:     firstNonEmpty(metadata.IdempotencyKey, PaymentEventID(session)),
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
		LinkType:           models.NormalizePaymentLinkType(session.LinkType),
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
		Product:            productSnapshot,
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
	return n.deliverPayload(ctx, domain, session.WebhookEvent, payload.EventID, "v1", body, metadata)
}

func paymentProductSnapshot(raw models.JSONData) (*models.PaymentProductSnapshot, error) {
	if strings.TrimSpace(string(raw)) == "" {
		return nil, nil
	}
	var snapshot models.PaymentProductSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil, fmt.Errorf("invalid payment product snapshot: %w", err)
	}
	return &snapshot, nil
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
	case models.PaymentOutcomeExact, models.PaymentOutcomeDonation:
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
	return n.DeliverRawWithMetadata(ctx, domain, eventType, eventID, eventVersion, body, DeliveryMetadata{})
}

func (n *Notifier) DeliverRawWithMetadata(ctx context.Context, domain models.Domain, eventType, eventID, eventVersion string, body []byte, metadata DeliveryMetadata) error {
	if eventVersion == "" {
		eventVersion = constants.WebhookEventVersionV1
	}
	enrichedBody, err := EnrichPayloadJSON(body, metadata)
	if err != nil {
		return err
	}
	body = enrichedBody
	return n.deliverPayload(ctx, domain, eventType, eventID, eventVersion, body, metadata)
}

func (n *Notifier) deliverPayload(ctx context.Context, domain models.Domain, eventType, eventID, eventVersion string, body []byte, metadata DeliveryMetadata) error {
	if domain.UsesNATS() {
		return n.deliverNATS(ctx, domain, body)
	}
	return n.deliverWebhook(ctx, domain, eventType, eventID, eventVersion, body, metadata)
}

func (n *Notifier) deliverWebhook(ctx context.Context, domain models.Domain, eventType, eventID, eventVersion string, body []byte, metadata DeliveryMetadata) error {
	if domain.WebhookURL == "" {
		return permanent(fmt.Errorf("webhook url is empty for domain %s", domain.ID.String()))
	}
	if domain.WebhookSecret == "" {
		return permanent(fmt.Errorf("webhook secret is empty for domain %s", domain.ID.String()))
	}
	if err := helpers.ValidateWebhookURL(domain.WebhookURL); err != nil {
		return permanent(fmt.Errorf("webhook url validation failed for domain %s: %w", domain.ID.String(), err))
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
	setDeliveryMetadataHeaders(req, metadata)
	req.Header.Set("X-Gateway-Timestamp", timestamp)
	req.Header.Set("X-Gateway-Signature", "sha256="+signature)

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, SanitizeDeliveryText(string(respBody)))
	}

	return nil
}

func (n *Notifier) deliverNATS(ctx context.Context, domain models.Domain, body []byte) error {
	if err := helpers.ValidateNATSURL(domain.NATSURL); err != nil {
		return permanent(fmt.Errorf("nats url validation failed for domain %s: %w", domain.ID.String(), err))
	}
	subject := domain.EffectiveNATSSubject()
	if err := helpers.ValidateNATSSubject(subject); err != nil {
		return permanent(fmt.Errorf("nats subject validation failed for domain %s: %w", domain.ID.String(), err))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	connector := n.natsConnect
	if connector == nil {
		connector = func(server string) (natsConnection, error) {
			return nats.Connect(server, nats.Timeout(10*time.Second))
		}
	}
	connection, err := connector(domain.NATSURL)
	if err != nil {
		return fmt.Errorf("nats connection failed: %w", err)
	}
	defer connection.Close()
	if err := connection.Publish(subject, body); err != nil {
		return fmt.Errorf("nats publish failed: %w", err)
	}
	flushTimeout := 10 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ctx.Err()
		}
		if remaining < flushTimeout {
			flushTimeout = remaining
		}
	}
	if err := connection.FlushTimeout(flushTimeout); err != nil {
		return fmt.Errorf("nats flush failed: %w", err)
	}
	return nil
}

func EnrichPayloadJSON(body []byte, metadata DeliveryMetadata) ([]byte, error) {
	if metadata.DeliveryID == "" &&
		metadata.ResourceType == "" &&
		metadata.ResourceID == "" &&
		metadata.Sequence == 0 &&
		metadata.IdempotencyKey == "" &&
		metadata.OriginalDeliveryID == "" &&
		metadata.ReplayCount == 0 {
		return body, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if metadata.DeliveryID != "" {
		payload["delivery_id"] = metadata.DeliveryID
	}
	if metadata.OriginalDeliveryID != "" {
		payload["original_delivery_id"] = metadata.OriginalDeliveryID
	}
	if metadata.ResourceType != "" {
		payload["resource_type"] = metadata.ResourceType
	}
	if metadata.ResourceID != "" {
		payload["resource_id"] = metadata.ResourceID
	}
	if metadata.Sequence > 0 {
		payload["sequence"] = metadata.Sequence
	}
	if metadata.IdempotencyKey != "" {
		payload["idempotency_key"] = metadata.IdempotencyKey
	}
	if metadata.ReplayCount > 0 {
		payload["replay_count"] = metadata.ReplayCount
	}
	return json.Marshal(payload)
}

func setDeliveryMetadataHeaders(req *http.Request, metadata DeliveryMetadata) {
	if metadata.DeliveryID != "" {
		req.Header.Set("X-Gateway-Delivery-Id", metadata.DeliveryID)
	}
	if metadata.ResourceType != "" {
		req.Header.Set("X-Gateway-Resource-Type", metadata.ResourceType)
	}
	if metadata.ResourceID != "" {
		req.Header.Set("X-Gateway-Resource-Id", metadata.ResourceID)
	}
	if metadata.Sequence > 0 {
		req.Header.Set("X-Gateway-Sequence", strconv.FormatInt(metadata.Sequence, 10))
	}
	if metadata.IdempotencyKey != "" {
		req.Header.Set("X-Gateway-Idempotency-Key", metadata.IdempotencyKey)
	}
}
