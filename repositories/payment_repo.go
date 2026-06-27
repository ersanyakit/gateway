package repositories

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"core/constants"
	"core/models"
	webhooksvc "core/services/webhook"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PaymentRepo struct {
	db *gorm.DB
}

const paymentReorgOutcomeReason = models.PaymentOutcomeReasonReorged

type PaymentMatchResult struct {
	Session        *models.PaymentSession
	Changed        bool
	Status         string
	Outcome        string
	WebhookEvent   string
	LedgerEligible bool
}

type paymentMatchDecision struct {
	Status             string
	Outcome            string
	Reason             string
	WebhookEvent       string
	MatchedAmountRaw   string
	ShortfallAmountRaw string
	ExcessAmountRaw    string
	LedgerEligible     bool
	ConfirmingPayment  bool
}

func NewPaymentRepo(db *gorm.DB) *PaymentRepo {
	return &PaymentRepo{db: db}
}

func (r *PaymentRepo) DB() *gorm.DB {
	return r.db
}

func (r *PaymentRepo) CountAll(ctx context.Context, out *int64) {
	r.db.WithContext(ctx).Model(&models.PaymentSession{}).Count(out)
}

func (r *PaymentRepo) Create(ctx context.Context, session *models.PaymentSession) error {
	if session.ID == uuid.Nil {
		session.ID = uuid.New()
	}
	if session.SessionToken == "" {
		token, err := newPaymentSessionToken()
		if err != nil {
			return err
		}
		session.SessionToken = token
	}
	if session.Status == "" {
		session.Status = models.PaymentStatusPending
	}
	now := time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now

	return r.db.WithContext(ctx).Create(session).Error
}

func (r *PaymentRepo) FindByToken(ctx context.Context, token string) (*models.PaymentSession, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Preload("Wallet").
		First(&session, "session_token = ?", token).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *PaymentRepo) FindByTokenForDomain(ctx context.Context, merchantID, domainID uuid.UUID, token string) (*models.PaymentSession, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Preload("Wallet").
		Where("merchant_id = ? AND domain_id = ? AND session_token = ?", merchantID, domainID, token).
		First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *PaymentRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.PaymentSession, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Preload("Wallet").
		First(&session, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *PaymentRepo) FindByIDForDomain(ctx context.Context, merchantID, domainID, id uuid.UUID) (*models.PaymentSession, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Preload("Wallet").
		Where("merchant_id = ? AND domain_id = ? AND id = ?", merchantID, domainID, id).
		First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *PaymentRepo) FindByTxUniqueHash(ctx context.Context, uniqueHash string) (*models.PaymentSession, error) {
	uniqueHash = strings.TrimSpace(uniqueHash)
	if uniqueHash == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var session models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Preload("Wallet").
		First(&session, "tx_unique_hash = ?", uniqueHash).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *PaymentRepo) List(ctx context.Context, limit int) ([]models.PaymentSession, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var sessions []models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Merchant").
		Preload("Domain").
		Order("created_at DESC").
		Limit(limit).
		Find(&sessions).Error
	return sessions, err
}

func (r *PaymentRepo) ListPage(ctx context.Context, page, limit int) ([]models.PaymentSession, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&models.PaymentSession{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var sessions []models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Merchant").
		Preload("Domain").
		Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&sessions).Error
	return sessions, total, err
}

func (r *PaymentRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.PaymentSession, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var sessions []models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&sessions).Error
	return sessions, err
}

func (r *PaymentRepo) ListByMerchantPage(ctx context.Context, merchantID uuid.UUID, status string, page, limit int) ([]models.PaymentSession, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	base := r.db.WithContext(ctx).Where("merchant_id = ?", merchantID)
	if status != "" {
		base = base.Where("status = ?", status)
	}
	var total int64
	if err := base.Model(&models.PaymentSession{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var sessions []models.PaymentSession
	err := base.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&sessions).Error
	return sessions, total, err
}

func (r *PaymentRepo) ListByDomainPage(ctx context.Context, merchantID, domainID uuid.UUID, status string, page, limit int) ([]models.PaymentSession, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	base := r.db.WithContext(ctx).Where("merchant_id = ? AND domain_id = ?", merchantID, domainID)
	if status != "" {
		base = base.Where("status = ?", status)
	}
	var total int64
	if err := base.Model(&models.PaymentSession{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var sessions []models.PaymentSession
	err := base.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&sessions).Error
	return sessions, total, err
}

func (r *PaymentRepo) FindByOrderID(ctx context.Context, merchantID uuid.UUID, orderID string) (*models.PaymentSession, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).
		Where("merchant_id = ? AND order_id = ?", merchantID, orderID).
		Order("created_at DESC").
		First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *PaymentRepo) FindByOrderIDForDomain(ctx context.Context, merchantID, domainID uuid.UUID, orderID string) (*models.PaymentSession, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).
		Where("merchant_id = ? AND domain_id = ? AND order_id = ?", merchantID, domainID, orderID).
		Order("created_at DESC").
		First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *PaymentRepo) StatsByMerchant(ctx context.Context, merchantID uuid.UUID) (map[string]int64, error) {
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Select("status, COUNT(*) as count").
		Where("merchant_id = ?", merchantID).
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]int64)
	for _, r := range rows {
		result[r.Status] = r.Count
	}
	return result, nil
}

func (r *PaymentRepo) StatsByDomain(ctx context.Context, merchantID, domainID uuid.UUID) (map[string]int64, error) {
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Select("status, COUNT(*) as count").
		Where("merchant_id = ? AND domain_id = ?", merchantID, domainID).
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]int64)
	for _, r := range rows {
		result[r.Status] = r.Count
	}
	return result, nil
}

func (r *PaymentRepo) SelectAsset(ctx context.Context, token string, chainID constants.ChainID, symbol string, assetToken *string, decimals uint8, amountRaw string, depositAddress string, quote *models.PriceQuote) (*models.PaymentSession, error) {
	updates := map[string]interface{}{
		"selected_chain_id":   &chainID,
		"selected_token":      assetToken,
		"selected_symbol":     symbol,
		"selected_decimals":   decimals,
		"expected_amount_raw": amountRaw,
		"deposit_address":     depositAddress,
		"status":              models.PaymentStatusAwaitingPayment,
		"updated_at":          time.Now(),
	}

	var session models.PaymentSession
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.PaymentSession{}).
			Where("session_token = ?", token).
			Where("status IN ?", []string{models.PaymentStatusPending, models.PaymentStatusAwaitingPayment}).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Preload("Domain").
			Preload("Wallet").
			First(&session, "session_token = ?", token).Error; err != nil {
			return err
		}
		if quote != nil {
			if quote.ID == uuid.Nil {
				quote.ID = uuid.New()
			}
			quote.PaymentID = session.ID
			if quote.QuotedAt.IsZero() {
				quote.QuotedAt = time.Now()
			}
			if quote.ExpiresAt.IsZero() {
				quote.ExpiresAt = time.Now().Add(15 * time.Minute)
				if session.ExpiresAt != nil {
					quote.ExpiresAt = *session.ExpiresAt
				}
			}
			return tx.Create(quote).Error
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &session, nil
}

func (r *PaymentRepo) ResetSelection(ctx context.Context, token string) (*models.PaymentSession, error) {
	updates := map[string]interface{}{
		"selected_chain_id":   nil,
		"selected_token":      nil,
		"selected_symbol":     "",
		"selected_decimals":   uint8(0),
		"expected_amount_raw": "",
		"deposit_address":     "",
		"status":              models.PaymentStatusPending,
		"updated_at":          time.Now(),
	}

	if err := r.db.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Where("session_token = ?", token).
		Where("status IN ?", []string{models.PaymentStatusPending, models.PaymentStatusAwaitingPayment}).
		Updates(updates).Error; err != nil {
		return nil, err
	}

	return r.FindByToken(ctx, token)
}

func (r *PaymentRepo) Cancel(ctx context.Context, token string) (*models.PaymentSession, bool, error) {
	var session models.PaymentSession
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Domain").
			First(&session, "session_token = ?", token).Error; err != nil {
			return err
		}
		if paymentStatusBlocksCancel(session.Status) {
			return nil
		}

		session.Status = models.PaymentStatusCanceled
		session.WebhookEvent = constants.WebhookEventPaymentFailed
		session.UpdatedAt = time.Now()
		return tx.Save(&session).Error
	})
	if err != nil {
		return nil, false, err
	}
	return &session, session.WebhookEvent == constants.WebhookEventPaymentFailed && session.WebhookSentAt == nil, nil
}

func paymentStatusBlocksCancel(status string) bool {
	switch status {
	case models.PaymentStatusPaid,
		models.PaymentStatusCanceled,
		models.PaymentStatusExpired,
		models.PaymentStatusFailed,
		models.PaymentStatusUnderpaid,
		models.PaymentStatusOverpaid,
		models.PaymentStatusPartialPaid:
		return true
	default:
		return false
	}
}

func (r *PaymentRepo) MarkPaidByTransaction(ctx context.Context, txModel models.Transaction) (*models.PaymentSession, bool, error) {
	matchResult, err := r.MatchFinalizedTransaction(ctx, txModel)
	if err != nil || matchResult == nil || matchResult.Session == nil {
		return nil, false, err
	}
	if matchResult.Changed && matchResult.Status == models.PaymentStatusPaid {
		return matchResult.Session, true, nil
	}
	return nil, false, nil
}

func (r *PaymentRepo) MatchFinalizedTransaction(ctx context.Context, txModel models.Transaction) (*PaymentMatchResult, error) {
	if txModel.WalletID == nil || txModel.Amount == "" {
		return nil, nil
	}
	if txModel.Status != models.TransactionStatusConfirmed || txModel.FinalizedAt == nil {
		return nil, nil
	}

	txAmount, ok := new(big.Int).SetString(txModel.Amount, 10)
	if !ok || txAmount.Sign() <= 0 {
		return nil, nil
	}
	if r == nil || r.db == nil {
		return nil, nil
	}
	if strings.TrimSpace(txModel.UniqueHash) != "" {
		existing, err := r.FindByTxUniqueHash(ctx, txModel.UniqueHash)
		if err == nil {
			return paymentMatchResultFromSession(existing, false), nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	var sessions []models.PaymentSession
	if err := r.db.WithContext(ctx).
		Preload("Domain").
		Where("wallet_id = ?", *txModel.WalletID).
		Where("status IN ?", []string{models.PaymentStatusAwaitingPayment, models.PaymentStatusExpired}).
		Order("created_at ASC").
		Find(&sessions).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	sessionID, decision, ok := selectPaymentMatchCandidate(sessions, txModel, now)
	if ok {
		return r.applyPaymentMatchDecision(ctx, sessionID, txModel, decision)
	}

	return nil, nil
}

func selectPaymentMatchCandidate(sessions []models.PaymentSession, txModel models.Transaction, now time.Time) (uuid.UUID, paymentMatchDecision, bool) {
	selectedPriority := 0
	var selectedID uuid.UUID
	var selectedDecision paymentMatchDecision
	selected := false
	for _, candidate := range sessions {
		decision, ok := paymentMatchDecisionForSession(candidate, txModel, now)
		if !ok {
			continue
		}
		priority := paymentMatchDecisionPriority(decision)
		if !selected || priority < selectedPriority {
			selected = true
			selectedPriority = priority
			selectedID = candidate.ID
			selectedDecision = decision
		}
	}

	if selected {
		return selectedID, selectedDecision, true
	}
	return uuid.Nil, paymentMatchDecision{}, false
}

func paymentMatchDecisionPriority(decision paymentMatchDecision) int {
	switch {
	case decision.Status == models.PaymentStatusPaid && decision.Outcome == models.PaymentOutcomeExact:
		return 0
	case decision.Status == models.PaymentStatusUnderpaid || decision.Status == models.PaymentStatusPartialPaid || decision.Status == models.PaymentStatusOverpaid:
		return 10
	case decision.Outcome == models.PaymentOutcomeExpiredAfterDeposit:
		return 20
	case decision.Outcome == models.PaymentOutcomeWrongChain || decision.Outcome == models.PaymentOutcomeWrongAsset:
		return 100
	default:
		return 50
	}
}

func (r *PaymentRepo) applyPaymentMatchDecision(ctx context.Context, sessionID uuid.UUID, txModel models.Transaction, decision paymentMatchDecision) (*PaymentMatchResult, error) {
	var matchedSession models.PaymentSession
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "payment-tx:"+txModel.UniqueHash).Error; err != nil {
			return err
		}
		var used int64
		if err := tx.Model(&models.PaymentSession{}).
			Where("tx_unique_hash = ?", txModel.UniqueHash).
			Count(&used).Error; err != nil {
			return err
		}
		if used > 0 {
			return nil
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Domain").
			First(&matchedSession, "id = ?", sessionID).Error; err != nil {
			return err
		}
		if matchedSession.TxUniqueHash != nil {
			return nil
		}
		if matchedSession.Status != models.PaymentStatusAwaitingPayment && matchedSession.Status != models.PaymentStatusExpired {
			return nil
		}

		now := time.Now()
		matchedSession.Status = decision.Status
		matchedSession.PaymentOutcome = decision.Outcome
		matchedSession.PaymentOutcomeReason = decision.Reason
		matchedSession.MatchedAmountRaw = decision.MatchedAmountRaw
		matchedSession.ShortfallAmountRaw = decision.ShortfallAmountRaw
		matchedSession.ExcessAmountRaw = decision.ExcessAmountRaw
		if decision.Status == models.PaymentStatusPaid {
			matchedSession.PaidAt = &now
		}
		matchedSession.ConfirmedAt = txModel.FinalizedAt
		matchedSession.ConfirmationsRequired = txModel.ConfirmationsRequired
		matchedSession.TxUniqueHash = &txModel.UniqueHash
		matchedSession.TxHash = &txModel.Hash
		matchedSession.WebhookEvent = decision.WebhookEvent
		matchedSession.UpdatedAt = now
		changed = true
		return tx.Save(&matchedSession).Error
	})
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return &PaymentMatchResult{
		Session:        &matchedSession,
		Changed:        true,
		Status:         decision.Status,
		Outcome:        decision.Outcome,
		WebhookEvent:   decision.WebhookEvent,
		LedgerEligible: decision.LedgerEligible,
	}, nil
}

func paymentMatchResultFromSession(session *models.PaymentSession, changed bool) *PaymentMatchResult {
	if session == nil {
		return nil
	}
	return &PaymentMatchResult{
		Session:        session,
		Changed:        changed,
		Status:         session.Status,
		Outcome:        session.PaymentOutcome,
		WebhookEvent:   session.WebhookEvent,
		LedgerEligible: session.TxUniqueHash != nil,
	}
}

func paymentMatchDecisionForSession(session models.PaymentSession, txModel models.Transaction, now time.Time) (paymentMatchDecision, bool) {
	if session.Status != models.PaymentStatusAwaitingPayment && session.Status != models.PaymentStatusExpired {
		return paymentMatchDecision{}, false
	}
	if session.SelectedChainID == nil {
		return paymentMatchDecision{}, false
	}
	if *session.SelectedChainID != txModel.ChainID {
		return failedPaymentMatchDecision(txModel, models.PaymentOutcomeWrongChain, "deposit chain does not match selected checkout chain"), true
	}
	if !paymentSessionAssetMatchesTransaction(session, txModel) {
		return failedPaymentMatchDecision(txModel, models.PaymentOutcomeWrongAsset, "deposit asset does not match selected checkout asset"), true
	}
	if session.Status == models.PaymentStatusExpired || (!now.IsZero() && session.ExpiresAt != nil && now.After(*session.ExpiresAt)) {
		return paymentMatchDecision{
			Status:           models.PaymentStatusExpired,
			Outcome:          models.PaymentOutcomeExpiredAfterDeposit,
			Reason:           "deposit finalized after checkout expiry",
			WebhookEvent:     constants.WebhookEventPaymentExpired,
			MatchedAmountRaw: txModel.Amount,
			LedgerEligible:   true,
		}, true
	}

	expected, ok := new(big.Int).SetString(session.ExpectedAmountRaw, 10)
	if !ok || expected.Sign() <= 0 {
		return paymentMatchDecision{}, false
	}
	txAmount, ok := new(big.Int).SetString(txModel.Amount, 10)
	if !ok || txAmount.Sign() <= 0 {
		return paymentMatchDecision{}, false
	}

	switch txAmount.Cmp(expected) {
	case 0:
		return paymentMatchDecision{
			Status:            models.PaymentStatusPaid,
			Outcome:           models.PaymentOutcomeExact,
			Reason:            "deposit amount exactly matches expected amount",
			WebhookEvent:      constants.WebhookEventPaymentSucceeded,
			MatchedAmountRaw:  txModel.Amount,
			LedgerEligible:    true,
			ConfirmingPayment: true,
		}, true
	case -1:
		shortfall := new(big.Int).Sub(expected, txAmount).String()
		threshold := new(big.Int).Mul(expected, big.NewInt(995))
		if new(big.Int).Mul(txAmount, big.NewInt(1000)).Cmp(threshold) >= 0 {
			return paymentMatchDecision{
				Status:             models.PaymentStatusUnderpaid,
				Outcome:            models.PaymentOutcomeUnderpaid,
				Reason:             "deposit amount is below expected amount",
				WebhookEvent:       constants.WebhookEventPaymentUnderpaid,
				MatchedAmountRaw:   txModel.Amount,
				ShortfallAmountRaw: shortfall,
				LedgerEligible:     true,
			}, true
		}
		return paymentMatchDecision{
			Status:             models.PaymentStatusPartialPaid,
			Outcome:            models.PaymentOutcomePartialUnsupported,
			Reason:             "partial deposits are not automatically aggregated for checkout settlement",
			WebhookEvent:       constants.WebhookEventPaymentPartialPaid,
			MatchedAmountRaw:   txModel.Amount,
			ShortfallAmountRaw: shortfall,
			LedgerEligible:     true,
		}, true
	default:
		return paymentMatchDecision{
			Status:           models.PaymentStatusOverpaid,
			Outcome:          models.PaymentOutcomeOverpaid,
			Reason:           "deposit amount exceeds expected amount",
			WebhookEvent:     constants.WebhookEventPaymentOverpaid,
			MatchedAmountRaw: txModel.Amount,
			ExcessAmountRaw:  new(big.Int).Sub(txAmount, expected).String(),
			LedgerEligible:   true,
		}, true
	}
}

func failedPaymentMatchDecision(txModel models.Transaction, outcome string, reason string) paymentMatchDecision {
	return paymentMatchDecision{
		Status:           models.PaymentStatusFailed,
		Outcome:          outcome,
		Reason:           reason,
		WebhookEvent:     constants.WebhookEventPaymentFailed,
		MatchedAmountRaw: txModel.Amount,
		LedgerEligible:   true,
	}
}

func paymentSessionAssetMatchesTransaction(session models.PaymentSession, txModel models.Transaction) bool {
	return strings.EqualFold(strings.TrimSpace(session.SelectedSymbol), strings.TrimSpace(txModel.Symbol)) &&
		paymentTokenMatches(session.SelectedToken, txModel.Token)
}

func paymentSessionChainMatchesTransaction(session models.PaymentSession, txModel models.Transaction) bool {
	return session.SelectedChainID != nil && *session.SelectedChainID == txModel.ChainID
}

func paymentTokenMatches(expected, actual *string) bool {
	expectedValue := ""
	if expected != nil {
		expectedValue = strings.TrimSpace(*expected)
	}
	actualValue := ""
	if actual != nil {
		actualValue = strings.TrimSpace(*actual)
	}
	if expectedValue == "" || actualValue == "" {
		return expectedValue == "" && actualValue == ""
	}
	return strings.EqualFold(expectedValue, actualValue)
}

func (r *PaymentRepo) MarkWebhookAttempt(ctx context.Context, sessionID uuid.UUID, delivered bool, lastErr error) error {
	var current models.PaymentSession
	if err := r.db.WithContext(ctx).Select("webhook_attempts").First(&current, "id = ?", sessionID).Error; err != nil {
		return err
	}
	attempts := current.WebhookAttempts + 1
	updates := map[string]interface{}{
		"webhook_attempts":     gorm.Expr("webhook_attempts + 1"),
		"webhook_locked_until": nil,
		"updated_at":           time.Now(),
	}
	if delivered {
		now := time.Now()
		updates["webhook_sent_at"] = &now
		updates["webhook_last_error"] = ""
	} else if lastErr != nil {
		updates["webhook_last_error"] = webhooksvc.SanitizeDeliveryError(lastErr)
		if attempts < webhookMaxAttempts() {
			lockUntil := time.Now().Add(webhookRetryBackoff(attempts))
			updates["webhook_locked_until"] = &lockUntil
		}
	}

	return r.db.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Where("id = ?", sessionID).
		Updates(updates).Error
}

func (r *PaymentRepo) MarkReorgedByTransactionWithDB(ctx context.Context, tx *gorm.DB, uniqueHash string) error {
	if strings.TrimSpace(uniqueHash) == "" {
		return nil
	}
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Where("tx_unique_hash = ?", uniqueHash).
		Where("NOT (status = ? AND payment_outcome_reason = ?)", models.PaymentStatusFailed, paymentReorgOutcomeReason).
		Updates(map[string]any{
			"status":                 models.PaymentStatusFailed,
			"payment_outcome":        paymentReorgOutcomeExpr(),
			"payment_outcome_reason": paymentReorgOutcomeReason,
			"paid_at":                nil,
			"confirmed_at":           nil,
			"webhook_event":          constants.WebhookEventPaymentFailed,
			"webhook_sent_at":        nil,
			"webhook_attempts":       0,
			"webhook_last_error":     "",
			"webhook_locked_until":   nil,
			"updated_at":             now,
		}).Error
}

func paymentReorgOutcomeExpr() clause.Expr {
	return gorm.Expr(
		`CASE
			WHEN payment_outcome <> '' THEN payment_outcome
			WHEN status = ? THEN ?
			WHEN status = ? THEN ?
			WHEN status = ? THEN ?
			WHEN status = ? THEN ?
			WHEN status = ? THEN ?
			ELSE payment_outcome
		END`,
		models.PaymentStatusPaid, models.PaymentOutcomeExact,
		models.PaymentStatusUnderpaid, models.PaymentOutcomeUnderpaid,
		models.PaymentStatusOverpaid, models.PaymentOutcomeOverpaid,
		models.PaymentStatusPartialPaid, models.PaymentOutcomePartialUnsupported,
		models.PaymentStatusExpired, models.PaymentOutcomeExpiredAfterDeposit,
	)
}

func (r *PaymentRepo) ListPendingWebhooks(ctx context.Context, limit int) ([]models.PaymentSession, error) {
	if limit <= 0 {
		limit = 100
	}

	var sessions []models.PaymentSession
	now := time.Now()
	lockUntil := now.Add(2 * time.Minute)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Preload("Domain").
			Joins("JOIN domains ON domains.id = payment_sessions.domain_id").
			Where("domains.webhook_url <> ''").
			Where("domains.webhook_secret <> ''").
			Where("webhook_event <> ''").
			Where("webhook_sent_at IS NULL").
			Where("webhook_attempts < ?", webhookMaxAttempts()).
			Where("webhook_locked_until IS NULL OR webhook_locked_until < ?", now).
			Order("payment_sessions.updated_at ASC").
			Limit(limit).
			Find(&sessions).Error; err != nil {
			return err
		}
		if len(sessions) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(sessions))
		for _, row := range sessions {
			ids = append(ids, row.ID)
		}
		return tx.Model(&models.PaymentSession{}).
			Where("id IN ?", ids).
			Update("webhook_locked_until", &lockUntil).Error
	})
	return sessions, err
}

func (r *PaymentRepo) MarkExpiredSessions(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Where("status IN ?", []string{models.PaymentStatusPending, models.PaymentStatusAwaitingPayment}).
		Where("expires_at IS NOT NULL").
		Where("expires_at < ?", time.Now()).
		Updates(map[string]interface{}{
			"status":        models.PaymentStatusExpired,
			"webhook_event": "payment_expired",
			"updated_at":    time.Now(),
		})
	return result.RowsAffected, result.Error
}

func newPaymentSessionToken() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func ErrPaymentSessionNotReady(session *models.PaymentSession) error {
	if session == nil {
		return errors.New("payment session not found")
	}
	return fmt.Errorf("payment session status is %s", session.Status)
}
