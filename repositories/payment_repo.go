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
	Aggregate          bool
	AllocationStatus   string
	MemoStatus         string
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
	session.LinkType = models.NormalizePaymentLinkType(session.LinkType)
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

func (r *PaymentRepo) ListTestableCheckoutSessions(ctx context.Context, limit int) ([]models.PaymentSession, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var sessions []models.PaymentSession
	err := r.db.WithContext(ctx).
		Preload("Merchant").
		Preload("Domain").
		Where("selected_chain_id IS NOT NULL").
		Where("TRIM(COALESCE(deposit_address, '')) <> ''").
		Where("(status = ? OR (status = ? AND link_type = ?))", models.PaymentStatusAwaitingPayment, models.PaymentStatusExpired, models.PaymentLinkTypeDonation).
		Order("created_at DESC").
		Limit(limit).
		Find(&sessions).Error
	return sessions, err
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
	err := base.
		Preload("Merchant").
		Preload("Domain").
		Order("created_at DESC").
		Limit(limit).
		Offset((page - 1) * limit).
		Find(&sessions).Error
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
	err := base.
		Preload("Merchant").
		Preload("Domain").
		Order("created_at DESC").
		Limit(limit).
		Offset((page - 1) * limit).
		Find(&sessions).Error
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
			Where(
				"status IN ? OR (status = ? AND link_type = ?)",
				[]string{models.PaymentStatusPending, models.PaymentStatusAwaitingPayment},
				models.PaymentStatusExpired,
				models.PaymentLinkTypeDonation,
			).
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
		Where(
			"status IN ? OR (status = ? AND link_type = ?)",
			[]string{models.PaymentStatusPending, models.PaymentStatusAwaitingPayment},
			models.PaymentStatusExpired,
			models.PaymentLinkTypeDonation,
		).
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
		if paymentSessionBlocksCancel(session) {
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

// Expire marks one checkout session as expired while holding a row lock. The
// conditional state check prevents a stale checkout request from overwriting a
// payment that completed concurrently.
func (r *PaymentRepo) Expire(ctx context.Context, token string) (*models.PaymentSession, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	if ctx == nil {
		ctx = context.Background()
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, false, gorm.ErrRecordNotFound
	}

	now := time.Now()
	var session models.PaymentSession
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Domain").
			Preload("Wallet").
			First(&session, "session_token = ?", token).Error; err != nil {
			return err
		}
		if !paymentSessionCanExpire(session, now) {
			return nil
		}

		updates := map[string]any{
			"status":        models.PaymentStatusExpired,
			"webhook_event": constants.WebhookEventPaymentExpired,
			"updated_at":    now,
		}
		result := tx.Model(&models.PaymentSession{}).
			Where("id = ?", session.ID).
			Where(
				"status IN ? OR (status = ? AND payment_outcome = ?)",
				[]string{models.PaymentStatusPending, models.PaymentStatusAwaitingPayment},
				models.PaymentStatusPartialPaid,
				models.PaymentOutcomePartialAggregating,
			).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return tx.Preload("Domain").Preload("Wallet").First(&session, "id = ?", session.ID).Error
		}

		if session.SelectedChainID != nil && strings.TrimSpace(session.DepositAddress) != "" {
			if err := NewWalletAddressRepo(tx).ReleaseExpiredCheckoutAddress(ctx, *session.SelectedChainID, session.DepositAddress, now); err != nil {
				return err
			}
		}
		session.Status = models.PaymentStatusExpired
		session.WebhookEvent = constants.WebhookEventPaymentExpired
		session.UpdatedAt = now
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &session, changed, nil
}

func paymentSessionCanExpire(session models.PaymentSession, now time.Time) bool {
	if models.IsDonationLinkType(session.LinkType) || session.ExpiresAt == nil || !now.After(*session.ExpiresAt) {
		return false
	}
	if session.Status == models.PaymentStatusPartialPaid {
		return session.PaymentOutcome == models.PaymentOutcomePartialAggregating
	}
	return session.Status == models.PaymentStatusPending || session.Status == models.PaymentStatusAwaitingPayment
}

func (r *PaymentRepo) MarkFailedForTest(ctx context.Context, sessionID uuid.UUID, reason string) (*models.PaymentSession, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	if sessionID == uuid.Nil {
		return nil, false, gorm.ErrRecordNotFound
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "admin test payment failure"
	}
	var session models.PaymentSession
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Domain").
			First(&session, "id = ?", sessionID).Error; err != nil {
			return err
		}
		if !paymentSessionAllowsAdminTestFailure(session) {
			return nil
		}
		now := time.Now()
		session.Status = models.PaymentStatusFailed
		session.PaymentOutcome = models.PaymentOutcomeAdminTestFailed
		session.PaymentOutcomeReason = boundedCorrectionReason(reason)
		session.WebhookEvent = constants.WebhookEventPaymentFailed
		session.UpdatedAt = now
		changed = true
		return tx.Save(&session).Error
	})
	if err != nil {
		return nil, false, err
	}
	return &session, changed && session.WebhookSentAt == nil, nil
}

func paymentSessionAllowsAdminTestFailure(session models.PaymentSession) bool {
	if session.Status == models.PaymentStatusAwaitingPayment {
		return true
	}
	return session.Status == models.PaymentStatusExpired && models.IsDonationLinkType(session.LinkType)
}

func paymentSessionBlocksCancel(session models.PaymentSession) bool {
	if models.IsDonationLinkType(session.LinkType) && session.Status == models.PaymentStatusExpired {
		return false
	}
	return paymentStatusBlocksCancel(session.Status)
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
	return r.MatchFinalizedDeposit(ctx, txModel, nil)
}

func (r *PaymentRepo) MatchFinalizedDeposit(ctx context.Context, txModel models.Transaction, deposit *models.Deposit) (*PaymentMatchResult, error) {
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
		if existing, err := r.findPaymentSessionByAllocationTx(ctx, txModel.UniqueHash); err == nil {
			result := paymentMatchResultFromSession(existing, false)
			if result != nil {
				result.LedgerEligible = true
			}
			return result, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		existing, err := r.FindByTxUniqueHash(ctx, txModel.UniqueHash)
		if err == nil {
			return paymentMatchResultFromSession(existing, false), nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	var sessions []models.PaymentSession
	observedAddress := paymentObservedDepositAddress(txModel, deposit)
	if strings.TrimSpace(observedAddress) == "" {
		return nil, nil
	}
	normalizedObservedAddress := NormalizeWalletLookupAddress(txModel.ChainID, observedAddress)
	query := r.db.WithContext(ctx).
		Preload("Domain").
		Where("wallet_id = ?", *txModel.WalletID).
		Where("status IN ?", []string{models.PaymentStatusAwaitingPayment, models.PaymentStatusExpired, models.PaymentStatusPartialPaid, models.PaymentStatusUnderpaid})
	if normalizedObservedAddress != "" {
		query = query.Where("(deposit_address = ? OR LOWER(deposit_address) = LOWER(?) OR LOWER(deposit_address) = LOWER(?))", observedAddress, observedAddress, normalizedObservedAddress)
	} else {
		query = query.Where("(deposit_address = ? OR LOWER(deposit_address) = LOWER(?))", observedAddress, observedAddress)
	}
	if err := query.
		Order("created_at ASC").
		Find(&sessions).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	sessionID, decision, ok, err := r.selectPaymentMatchCandidateForDeposit(ctx, sessions, txModel, deposit, now)
	if err != nil {
		return nil, err
	}
	if ok {
		return r.applyPaymentMatchDecision(ctx, sessionID, txModel, deposit, decision)
	}

	return nil, nil
}

func (r *PaymentRepo) findPaymentSessionByAllocationTx(ctx context.Context, uniqueHash string) (*models.PaymentSession, error) {
	uniqueHash = strings.TrimSpace(uniqueHash)
	if uniqueHash == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var allocation models.PaymentDepositAllocation
	if err := r.db.WithContext(ctx).
		First(&allocation, "transaction_unique_hash = ?", uniqueHash).Error; err != nil {
		return nil, err
	}
	return r.FindByID(ctx, allocation.PaymentSessionID)
}

func (r *PaymentRepo) selectPaymentMatchCandidateForDeposit(ctx context.Context, sessions []models.PaymentSession, txModel models.Transaction, deposit *models.Deposit, now time.Time) (uuid.UUID, paymentMatchDecision, bool, error) {
	selectedPriority := 0
	var selectedID uuid.UUID
	var selectedDecision paymentMatchDecision
	selected := false
	for _, candidate := range sessions {
		decision, ok, err := r.paymentMatchDecisionForSessionAndDeposit(ctx, candidate, txModel, deposit, now)
		if err != nil {
			return uuid.Nil, paymentMatchDecision{}, false, err
		}
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
		return selectedID, selectedDecision, true, nil
	}
	return uuid.Nil, paymentMatchDecision{}, false, nil
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
	case decision.Status == models.PaymentStatusPaid && decision.Outcome == models.PaymentOutcomeDonation:
		return 0
	case decision.Status == models.PaymentStatusPaid && decision.Outcome == models.PaymentOutcomeAggregateComplete:
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

func (r *PaymentRepo) applyPaymentMatchDecision(ctx context.Context, sessionID uuid.UUID, txModel models.Transaction, deposit *models.Deposit, decision paymentMatchDecision) (*PaymentMatchResult, error) {
	var matchedSession models.PaymentSession
	changed := false
	existingMatch := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "payment-tx:"+txModel.UniqueHash).Error; err != nil {
			return err
		}
		var allocated int64
		if err := tx.Model(&models.PaymentDepositAllocation{}).
			Where("transaction_unique_hash = ?", txModel.UniqueHash).
			Count(&allocated).Error; err != nil {
			return err
		}
		if allocated > 0 {
			var allocation models.PaymentDepositAllocation
			if err := tx.First(&allocation, "transaction_unique_hash = ?", txModel.UniqueHash).Error; err != nil {
				return err
			}
			if err := tx.Preload("Domain").First(&matchedSession, "id = ?", allocation.PaymentSessionID).Error; err != nil {
				return err
			}
			existingMatch = true
			return nil
		}
		var used int64
		if err := tx.Model(&models.PaymentSession{}).
			Where("tx_unique_hash = ?", txModel.UniqueHash).
			Count(&used).Error; err != nil {
			return err
		}
		if used > 0 {
			if err := tx.Preload("Domain").First(&matchedSession, "tx_unique_hash = ?", txModel.UniqueHash).Error; err != nil {
				return err
			}
			existingMatch = true
			return nil
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Domain").
			First(&matchedSession, "id = ?", sessionID).Error; err != nil {
			return err
		}
		if matchedSession.TxUniqueHash != nil && !decision.Aggregate {
			return nil
		}
		if !paymentSessionAcceptsMatchDecision(matchedSession, decision) {
			return nil
		}
		if decision.Aggregate && decision.AllocationStatus != models.PaymentDepositAllocationStatusQuarantined {
			refreshed, err := r.aggregatePaymentMatchDecisionForSession(ctx, tx, matchedSession, txModel, deposit, time.Now())
			if err != nil {
				return err
			}
			if refreshed.Status == "" {
				return nil
			}
			decision = refreshed
		}

		now := time.Now()
		allocation := paymentDepositAllocationFromDecision(matchedSession.ID, txModel, deposit, decision, now)
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "transaction_unique_hash"}},
			DoNothing: true,
		}).Create(&allocation).Error; err != nil {
			return err
		}
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
		if paymentMatchDecisionShouldSetTerminalTx(decision) {
			matchedSession.TxUniqueHash = &txModel.UniqueHash
			matchedSession.TxHash = &txModel.Hash
		}
		matchedSession.WebhookEvent = decision.WebhookEvent
		matchedSession.UpdatedAt = now
		changed = true
		if err := tx.Save(&matchedSession).Error; err != nil {
			return err
		}
		if err := recordPaymentSettlementEvent(ctx, tx, matchedSession, txModel, deposit, decision, now); err != nil {
			return err
		}
		return openPaymentSettlementReconciliation(ctx, tx, matchedSession, txModel, deposit, decision)
	})
	if err != nil {
		return nil, err
	}
	if !changed {
		if !existingMatch {
			return nil, nil
		}
		if matchedSession.ID != uuid.Nil {
			result := paymentMatchResultFromSession(&matchedSession, false)
			if result != nil {
				result.LedgerEligible = true
			}
			return result, nil
		}
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

func recordPaymentSettlementEvent(ctx context.Context, tx *gorm.DB, session models.PaymentSession, txModel models.Transaction, deposit *models.Deposit, decision paymentMatchDecision, occurredAt time.Time) error {
	eventName := paymentSettlementMoneyEventName(decision)
	if eventName == "" || session.ID == uuid.Nil || session.MerchantID == uuid.Nil || session.DomainID == uuid.Nil {
		return nil
	}
	txIdentity := strings.TrimSpace(txModel.UniqueHash)
	if txIdentity == "" {
		txIdentity = strings.TrimSpace(txModel.Hash)
	}
	if txIdentity == "" {
		txIdentity = "session"
	}
	eventID := session.ID.String() + ":" + eventName + ":" + txIdentity
	payload := map[string]any{
		"event_id":               eventID,
		"event_type":             eventName,
		"event_version":          constants.WebhookEventVersionV1,
		"occurred_at":            occurredAt.UTC().Format(time.RFC3339Nano),
		"merchant_id":            session.MerchantID.String(),
		"domain_id":              session.DomainID.String(),
		"resource_type":          "payment",
		"resource_id":            session.ID.String(),
		"resource_status":        session.Status,
		"idempotency_key":        eventID,
		"correlation_id":         "transaction:" + txIdentity,
		"payment_id":             session.ID.String(),
		"order_id":               session.OrderID,
		"amount":                 session.Amount,
		"currency":               session.Currency,
		"status":                 session.Status,
		"payment_outcome":        session.PaymentOutcome,
		"payment_outcome_reason": session.PaymentOutcomeReason,
		"failure_reason":         session.PaymentOutcomeReason,
		"expected_amount_raw":    session.ExpectedAmountRaw,
		"matched_amount_raw":     session.MatchedAmountRaw,
		"shortfall_amount_raw":   session.ShortfallAmountRaw,
		"excess_amount_raw":      session.ExcessAmountRaw,
		"tx_hash":                txModel.Hash,
		"tx_unique_hash":         txModel.UniqueHash,
		"chain_id":               int64(txModel.ChainID),
		"amount_raw":             txModel.Amount,
		"symbol":                 txModel.Symbol,
		"token":                  txModel.Token,
		"memo_status":            decision.MemoStatus,
		"allocation_status":      decision.AllocationStatus,
	}
	if session.ExpiresAt != nil {
		payload["expires_at"] = session.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if deposit != nil {
		payload["deposit_id"] = deposit.ID.String()
		payload["chain_fact_event_id"] = deposit.ChainFactEventID
		payload["observed_address"] = deposit.ObservedAddress
	}
	event, err := BuildMoneyEventOutbox(MoneyEventOutboxBuildParams{
		EventName:      eventName,
		EventID:        eventID,
		AggregateType:  "payment",
		AggregateID:    session.ID.String(),
		MerchantID:     session.MerchantID,
		DomainID:       session.DomainID,
		IdempotencyKey: eventID,
		Payload:        payload,
	})
	if err != nil {
		return err
	}
	_, _, err = NewMoneyEventOutboxRepo(tx).RecordWithDB(ctx, tx, &event)
	return err
}

func paymentSettlementMoneyEventName(decision paymentMatchDecision) string {
	switch decision.WebhookEvent {
	case constants.WebhookEventPaymentSucceeded:
		return "payment.succeeded.v1"
	case constants.WebhookEventPaymentFailed:
		return "payment.failed.v1"
	case constants.WebhookEventPaymentExpired:
		return "payment.expired.v1"
	case constants.WebhookEventPaymentUnderpaid:
		return "payment.underpaid.v1"
	case constants.WebhookEventPaymentOverpaid:
		return "payment.overpaid.v1"
	case constants.WebhookEventPaymentPartialPaid:
		return "payment.partial_paid.v1"
	default:
		return strings.TrimSpace(decision.WebhookEvent)
	}
}

func openPaymentSettlementReconciliation(ctx context.Context, tx *gorm.DB, session models.PaymentSession, txModel models.Transaction, deposit *models.Deposit, decision paymentMatchDecision) error {
	reason := paymentSettlementReconciliationReason(decision)
	if reason == "" {
		return nil
	}
	merchantID := session.MerchantID
	domainID := session.DomainID
	affected := []string{session.ID.String()}
	if strings.TrimSpace(txModel.UniqueHash) != "" {
		affected = append(affected, txModel.UniqueHash)
	}
	if deposit != nil && deposit.ID != uuid.Nil {
		affected = append(affected, deposit.ID.String(), deposit.ChainFactEventID)
	}
	_, _, err := NewReconciliationRepo(tx).CreateScopedOpenIfMissing(ctx, ReconciliationScope{
		ChainID:             txModel.ChainID,
		Reason:              reason,
		MerchantID:          &merchantID,
		DomainID:            &domainID,
		ScopeKey:            reason + ":" + session.ID.String() + ":" + strings.TrimSpace(txModel.UniqueHash),
		ResourceType:        "payment",
		ResourceID:          session.ID.String(),
		AffectedResourceIDs: affected,
		Evidence: map[string]any{
			"payment_id":             session.ID.String(),
			"status":                 session.Status,
			"payment_outcome":        session.PaymentOutcome,
			"payment_outcome_reason": session.PaymentOutcomeReason,
			"tx_unique_hash":         txModel.UniqueHash,
			"tx_hash":                txModel.Hash,
			"allocation_status":      decision.AllocationStatus,
			"memo_status":            decision.MemoStatus,
			"matched_amount_raw":     session.MatchedAmountRaw,
			"shortfall_amount_raw":   session.ShortfallAmountRaw,
			"excess_amount_raw":      session.ExcessAmountRaw,
		},
	})
	return err
}

func paymentSettlementReconciliationReason(decision paymentMatchDecision) string {
	switch decision.Outcome {
	case models.PaymentOutcomeWrongMemo:
		return "wrong_memo_quarantine"
	case models.PaymentOutcomeMissingMemo:
		return "missing_memo_quarantine"
	case models.PaymentOutcomeOverpaid:
		if decision.Aggregate {
			return "aggregate_overpaid_drift"
		}
	}
	return ""
}

func (r *PaymentRepo) paymentMatchDecisionForSessionAndDeposit(ctx context.Context, session models.PaymentSession, txModel models.Transaction, deposit *models.Deposit, now time.Time) (paymentMatchDecision, bool, error) {
	if !paymentSessionChainMatchesTransaction(session, txModel) {
		return failedPaymentMatchDecision(txModel, models.PaymentOutcomeWrongChain, "deposit chain does not match selected checkout chain"), true, nil
	}
	if !paymentSessionAssetMatchesTransaction(session, txModel) {
		return failedPaymentMatchDecision(txModel, models.PaymentOutcomeWrongAsset, "deposit asset does not match selected checkout asset"), true, nil
	}
	if !paymentSessionDepositAddressMatchesTransaction(session, txModel, deposit) {
		return paymentMatchDecision{}, false, nil
	}
	if memoDecision, ok := paymentMemoDecisionForSession(session, txModel, deposit); ok {
		return memoDecision, true, nil
	}
	if paymentSettlementPolicy(session) == models.PaymentSettlementPolicyAggregate {
		decision, err := r.aggregatePaymentMatchDecisionForSession(ctx, r.db, session, txModel, deposit, now)
		if err != nil {
			return paymentMatchDecision{}, false, err
		}
		if decision.Status == "" {
			return paymentMatchDecision{}, false, nil
		}
		return decision, true, nil
	}
	decision, ok := paymentMatchDecisionForSession(session, txModel, now)
	return decision, ok, nil
}

func (r *PaymentRepo) aggregatePaymentMatchDecisionForSession(ctx context.Context, tx *gorm.DB, session models.PaymentSession, txModel models.Transaction, deposit *models.Deposit, now time.Time) (paymentMatchDecision, error) {
	if session.Status != models.PaymentStatusAwaitingPayment &&
		session.Status != models.PaymentStatusExpired &&
		session.Status != models.PaymentStatusPartialPaid &&
		session.Status != models.PaymentStatusUnderpaid {
		return paymentMatchDecision{}, nil
	}
	if session.SelectedChainID == nil {
		return paymentMatchDecision{}, nil
	}
	if *session.SelectedChainID != txModel.ChainID {
		return aggregateFailedPaymentMatchDecision(txModel, models.PaymentOutcomeWrongChain, "deposit chain does not match selected checkout chain"), nil
	}
	if !paymentSessionAssetMatchesTransaction(session, txModel) {
		return aggregateFailedPaymentMatchDecision(txModel, models.PaymentOutcomeWrongAsset, "deposit asset does not match selected checkout asset"), nil
	}
	if !paymentSessionDepositAddressMatchesTransaction(session, txModel, deposit) {
		return paymentMatchDecision{}, nil
	}
	if !models.IsDonationLinkType(session.LinkType) && (session.Status == models.PaymentStatusExpired || (!now.IsZero() && session.ExpiresAt != nil && now.After(*session.ExpiresAt))) {
		return paymentMatchDecision{
			Status:           models.PaymentStatusExpired,
			Outcome:          models.PaymentOutcomeExpiredAfterDeposit,
			Reason:           "deposit finalized after checkout expiry",
			WebhookEvent:     constants.WebhookEventPaymentExpired,
			MatchedAmountRaw: txModel.Amount,
			LedgerEligible:   true,
			Aggregate:        true,
			AllocationStatus: models.PaymentDepositAllocationStatusApplied,
			MemoStatus:       paymentMemoStatusForAllocation(session, deposit),
		}, nil
	}
	expected, ok := new(big.Int).SetString(session.ExpectedAmountRaw, 10)
	if !ok || expected.Sign() <= 0 {
		return paymentMatchDecision{}, nil
	}
	txAmount, ok := new(big.Int).SetString(txModel.Amount, 10)
	if !ok || txAmount.Sign() <= 0 {
		return paymentMatchDecision{}, nil
	}
	current, err := paymentAllocatedAmountRaw(ctx, tx, session.ID)
	if err != nil {
		return paymentMatchDecision{}, err
	}
	total := new(big.Int).Add(current, txAmount)
	decision := paymentMatchDecision{
		MatchedAmountRaw: total.String(),
		LedgerEligible:   true,
		Aggregate:        true,
		AllocationStatus: models.PaymentDepositAllocationStatusApplied,
		MemoStatus:       paymentMemoStatusForAllocation(session, deposit),
	}
	switch total.Cmp(expected) {
	case 0:
		decision.Status = models.PaymentStatusPaid
		decision.Outcome = models.PaymentOutcomeAggregateComplete
		decision.Reason = "aggregate finalized deposits exactly match expected amount"
		decision.WebhookEvent = constants.WebhookEventPaymentSucceeded
		decision.ConfirmingPayment = true
	case -1:
		decision.Status = models.PaymentStatusPartialPaid
		decision.Outcome = models.PaymentOutcomePartialAggregating
		decision.Reason = "aggregate finalized deposits are below expected amount"
		decision.WebhookEvent = constants.WebhookEventPaymentPartialPaid
		decision.ShortfallAmountRaw = new(big.Int).Sub(expected, total).String()
	default:
		decision.Status = models.PaymentStatusOverpaid
		decision.Outcome = models.PaymentOutcomeOverpaid
		decision.Reason = "aggregate finalized deposits exceed expected amount"
		decision.WebhookEvent = constants.WebhookEventPaymentOverpaid
		decision.ExcessAmountRaw = new(big.Int).Sub(total, expected).String()
	}
	return decision, nil
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
	if !models.IsDonationLinkType(session.LinkType) && (session.Status == models.PaymentStatusExpired || (!now.IsZero() && session.ExpiresAt != nil && now.After(*session.ExpiresAt))) {
		return paymentMatchDecision{
			Status:           models.PaymentStatusExpired,
			Outcome:          models.PaymentOutcomeExpiredAfterDeposit,
			Reason:           "deposit finalized after checkout expiry",
			WebhookEvent:     constants.WebhookEventPaymentExpired,
			MatchedAmountRaw: txModel.Amount,
			LedgerEligible:   true,
		}, true
	}

	if models.IsDonationLinkType(session.LinkType) {
		return paymentMatchDecision{
			Status:            models.PaymentStatusPaid,
			Outcome:           models.PaymentOutcomeDonation,
			Reason:            "donation amount accepted without fixed expected amount",
			WebhookEvent:      constants.WebhookEventPaymentSucceeded,
			MatchedAmountRaw:  txModel.Amount,
			LedgerEligible:    true,
			ConfirmingPayment: true,
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

func paymentMemoDecisionForSession(session models.PaymentSession, txModel models.Transaction, deposit *models.Deposit) (paymentMatchDecision, bool) {
	requiredMemo := normalizePaymentMemo(firstNonEmptyString(session.RequiredMemoNormalized, session.RequiredMemo))
	if requiredMemo == "" {
		return paymentMatchDecision{}, false
	}
	actualMemo := ""
	if deposit != nil {
		actualMemo = normalizePaymentMemo(firstNonEmptyString(deposit.MemoNormalized, deposit.Memo))
	}
	if actualMemo == "" {
		decision := failedMemoPaymentMatchDecision(txModel, models.PaymentOutcomeMissingMemo, "deposit memo/tag is required but missing", models.DepositMemoStatusMissing)
		decision.Aggregate = paymentSettlementPolicy(session) == models.PaymentSettlementPolicyAggregate
		return decision, true
	}
	if actualMemo != requiredMemo {
		decision := failedMemoPaymentMatchDecision(txModel, models.PaymentOutcomeWrongMemo, "deposit memo/tag does not match required payment memo/tag", models.DepositMemoStatusWrong)
		decision.Aggregate = paymentSettlementPolicy(session) == models.PaymentSettlementPolicyAggregate
		return decision, true
	}
	return paymentMatchDecision{}, false
}

func failedMemoPaymentMatchDecision(txModel models.Transaction, outcome string, reason string, memoStatus string) paymentMatchDecision {
	decision := failedPaymentMatchDecision(txModel, outcome, reason)
	decision.AllocationStatus = models.PaymentDepositAllocationStatusQuarantined
	decision.MemoStatus = memoStatus
	return decision
}

func failedPaymentMatchDecision(txModel models.Transaction, outcome string, reason string) paymentMatchDecision {
	return paymentMatchDecision{
		Status:           models.PaymentStatusFailed,
		Outcome:          outcome,
		Reason:           reason,
		WebhookEvent:     constants.WebhookEventPaymentFailed,
		MatchedAmountRaw: txModel.Amount,
		LedgerEligible:   true,
		AllocationStatus: models.PaymentDepositAllocationStatusApplied,
		MemoStatus:       models.DepositMemoStatusNotRequired,
	}
}

func aggregateFailedPaymentMatchDecision(txModel models.Transaction, outcome string, reason string) paymentMatchDecision {
	decision := failedPaymentMatchDecision(txModel, outcome, reason)
	decision.Aggregate = true
	return decision
}

func paymentAllocatedAmountRaw(ctx context.Context, tx *gorm.DB, sessionID uuid.UUID) (*big.Int, error) {
	var rows []models.PaymentDepositAllocation
	if err := tx.WithContext(ctx).
		Where("payment_session_id = ?", sessionID).
		Where("status = ?", models.PaymentDepositAllocationStatusApplied).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	total := big.NewInt(0)
	for _, row := range rows {
		amount, ok := new(big.Int).SetString(strings.TrimSpace(row.AmountRaw), 10)
		if ok && amount.Sign() > 0 {
			total.Add(total, amount)
		}
	}
	return total, nil
}

func paymentDepositAllocationFromDecision(sessionID uuid.UUID, txModel models.Transaction, deposit *models.Deposit, decision paymentMatchDecision, now time.Time) models.PaymentDepositAllocation {
	status := decision.AllocationStatus
	if status == "" {
		status = models.PaymentDepositAllocationStatusApplied
	}
	memoStatus := decision.MemoStatus
	if memoStatus == "" {
		memoStatus = paymentMemoStatusForAllocation(models.PaymentSession{}, deposit)
	}
	allocation := models.PaymentDepositAllocation{
		ID:                    uuid.New(),
		PaymentSessionID:      sessionID,
		TransactionUniqueHash: strings.TrimSpace(txModel.UniqueHash),
		TxHash:                strings.TrimSpace(txModel.Hash),
		ChainID:               txModel.ChainID,
		Token:                 trimOptionalString(txModel.Token),
		Symbol:                strings.TrimSpace(txModel.Symbol),
		Decimals:              txModel.Decimals,
		AmountRaw:             strings.TrimSpace(txModel.Amount),
		MemoStatus:            memoStatus,
		Status:                status,
		Outcome:               decision.Outcome,
		Reason:                boundedCorrectionReason(decision.Reason),
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if deposit != nil {
		depositID := deposit.ID
		if depositID != uuid.Nil {
			allocation.DepositID = &depositID
		}
		allocation.ChainFactEventID = strings.TrimSpace(deposit.ChainFactEventID)
		allocation.ObservedAddress = strings.TrimSpace(deposit.ObservedAddress)
		allocation.ObservedAddressNormalized = NormalizeWalletLookupAddress(deposit.ChainID, deposit.ObservedAddress)
		allocation.Memo = strings.TrimSpace(deposit.Memo)
		allocation.MemoNormalized = normalizePaymentMemo(firstNonEmptyString(deposit.MemoNormalized, deposit.Memo))
	}
	return allocation
}

func paymentSessionAcceptsMatchDecision(session models.PaymentSession, decision paymentMatchDecision) bool {
	if decision.Aggregate {
		switch session.Status {
		case models.PaymentStatusAwaitingPayment, models.PaymentStatusExpired, models.PaymentStatusPartialPaid, models.PaymentStatusUnderpaid:
			return true
		default:
			return false
		}
	}
	return session.Status == models.PaymentStatusAwaitingPayment || session.Status == models.PaymentStatusExpired
}

func paymentMatchDecisionShouldSetTerminalTx(decision paymentMatchDecision) bool {
	if !decision.Aggregate {
		return true
	}
	switch decision.Status {
	case models.PaymentStatusPaid, models.PaymentStatusFailed, models.PaymentStatusExpired, models.PaymentStatusOverpaid:
		return true
	default:
		return false
	}
}

func paymentSettlementPolicy(session models.PaymentSession) string {
	switch strings.ToLower(strings.TrimSpace(session.SettlementPolicy)) {
	case models.PaymentSettlementPolicyAggregate:
		return models.PaymentSettlementPolicyAggregate
	default:
		return models.PaymentSettlementPolicySingle
	}
}

func paymentMemoStatusForAllocation(session models.PaymentSession, deposit *models.Deposit) string {
	if normalizePaymentMemo(firstNonEmptyString(session.RequiredMemoNormalized, session.RequiredMemo)) == "" {
		return models.DepositMemoStatusNotRequired
	}
	if deposit == nil || normalizePaymentMemo(firstNonEmptyString(deposit.MemoNormalized, deposit.Memo)) == "" {
		return models.DepositMemoStatusMissing
	}
	if normalizePaymentMemo(firstNonEmptyString(deposit.MemoNormalized, deposit.Memo)) != normalizePaymentMemo(firstNonEmptyString(session.RequiredMemoNormalized, session.RequiredMemo)) {
		return models.DepositMemoStatusWrong
	}
	return models.DepositMemoStatusPresent
}

func paymentSessionAssetMatchesTransaction(session models.PaymentSession, txModel models.Transaction) bool {
	if session.SelectedDecimals != txModel.Decimals {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(session.SelectedSymbol), strings.TrimSpace(txModel.Symbol)) &&
		paymentTokenMatches(session.SelectedToken, txModel.Token)
}

func paymentSessionChainMatchesTransaction(session models.PaymentSession, txModel models.Transaction) bool {
	return session.SelectedChainID != nil && *session.SelectedChainID == txModel.ChainID
}

func paymentSessionDepositAddressMatchesTransaction(session models.PaymentSession, txModel models.Transaction, deposit *models.Deposit) bool {
	expected := NormalizeWalletLookupAddress(txModel.ChainID, session.DepositAddress)
	if expected == "" {
		return false
	}
	observed := paymentObservedDepositAddress(txModel, deposit)
	if observed == "" {
		return false
	}
	return expected == NormalizeWalletLookupAddress(txModel.ChainID, observed)
}

func paymentObservedDepositAddress(txModel models.Transaction, deposit *models.Deposit) string {
	if deposit != nil && strings.TrimSpace(deposit.ObservedAddress) != "" {
		return strings.TrimSpace(deposit.ObservedAddress)
	}
	return strings.TrimSpace(txModel.ToAddress)
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
	var allocationSessionIDs []uuid.UUID
	if err := tx.WithContext(ctx).
		Model(&models.PaymentDepositAllocation{}).
		Where("transaction_unique_hash = ?", uniqueHash).
		Where("status <> ?", models.PaymentDepositAllocationStatusReorged).
		Pluck("payment_session_id", &allocationSessionIDs).Error; err != nil {
		return err
	}
	if err := tx.WithContext(ctx).
		Model(&models.PaymentDepositAllocation{}).
		Where("transaction_unique_hash = ?", uniqueHash).
		Where("status <> ?", models.PaymentDepositAllocationStatusReorged).
		Updates(map[string]any{
			"status":     models.PaymentDepositAllocationStatusReorged,
			"reorged_at": &now,
			"reason":     paymentReorgOutcomeReason,
			"updated_at": now,
		}).Error; err != nil {
		return err
	}
	query := tx.WithContext(ctx).
		Model(&models.PaymentSession{}).
		Where("NOT (status = ? AND payment_outcome_reason = ?)", models.PaymentStatusFailed, paymentReorgOutcomeReason)
	if len(allocationSessionIDs) > 0 {
		query = query.Where("tx_unique_hash = ? OR id IN ?", uniqueHash, allocationSessionIDs)
	} else {
		query = query.Where("tx_unique_hash = ?", uniqueHash)
	}
	if err := query.
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
		}).Error; err != nil {
		return err
	}
	return openPaymentReorgReconciliation(ctx, tx, uniqueHash, allocationSessionIDs, now)
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

func openPaymentReorgReconciliation(ctx context.Context, tx *gorm.DB, uniqueHash string, sessionIDs []uuid.UUID, occurredAt time.Time) error {
	uniqueHash = strings.TrimSpace(uniqueHash)
	if uniqueHash == "" {
		return nil
	}
	affected := []string{uniqueHash}
	for _, id := range sessionIDs {
		if id != uuid.Nil {
			affected = append(affected, id.String())
		}
	}
	var merchantID *uuid.UUID
	var domainID *uuid.UUID
	var session models.PaymentSession
	query := tx.WithContext(ctx).Select("id", "merchant_id", "domain_id")
	if len(sessionIDs) > 0 {
		query = query.First(&session, "id IN ?", sessionIDs)
	} else {
		query = query.First(&session, "tx_unique_hash = ?", uniqueHash)
	}
	if query.Error == nil {
		merchantID = &session.MerchantID
		domainID = &session.DomainID
		if session.ID != uuid.Nil {
			affected = append(affected, session.ID.String())
		}
	} else if !errors.Is(query.Error, gorm.ErrRecordNotFound) {
		return query.Error
	}
	var allocation models.PaymentDepositAllocation
	chainID := constants.ChainID(0)
	if err := tx.WithContext(ctx).
		Select("chain_id", "payment_session_id", "tx_hash", "outcome", "status").
		First(&allocation, "transaction_unique_hash = ?", uniqueHash).Error; err == nil {
		chainID = allocation.ChainID
		if allocation.PaymentSessionID != uuid.Nil {
			affected = append(affected, allocation.PaymentSessionID.String())
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	_, _, err := NewReconciliationRepo(tx).CreateScopedOpenIfMissing(ctx, ReconciliationScope{
		ChainID:             chainID,
		Reason:              "payment_allocation_reorg",
		MerchantID:          merchantID,
		DomainID:            domainID,
		ScopeKey:            "payment_allocation_reorg:" + uniqueHash,
		ResourceType:        "payment_deposit_allocation",
		ResourceID:          uniqueHash,
		AffectedResourceIDs: affected,
		Evidence: map[string]any{
			"tx_unique_hash": uniqueHash,
			"occurred_at":    occurredAt.UTC().Format(time.RFC3339Nano),
			"session_ids":    uuidStrings(sessionIDs),
			"allocation": map[string]any{
				"payment_session_id": allocation.PaymentSessionID.String(),
				"tx_hash":            allocation.TxHash,
				"status":             allocation.Status,
				"outcome":            allocation.Outcome,
			},
		},
	})
	return err
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != uuid.Nil {
			out = append(out, id.String())
		}
	}
	return out
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
			Scopes(whereConfiguredDomainNotificationTarget).
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
	now := time.Now()
	var expired []models.PaymentSession
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Model(&models.PaymentSession{}).
			Select("id", "selected_chain_id", "deposit_address").
			Where(
				"(status IN ? OR (status = ? AND payment_outcome = ?))",
				[]string{models.PaymentStatusPending, models.PaymentStatusAwaitingPayment},
				models.PaymentStatusPartialPaid,
				models.PaymentOutcomePartialAggregating,
			).
			Where("link_type <> ?", models.PaymentLinkTypeDonation).
			Where("expires_at IS NOT NULL").
			Where("expires_at < ?", now)
		if err := query.Find(&expired).Error; err != nil {
			return err
		}
		if len(expired) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(expired))
		for _, session := range expired {
			ids = append(ids, session.ID)
		}
		if err := tx.Model(&models.PaymentSession{}).
			Where("id IN ?", ids).
			Updates(map[string]interface{}{
				"status":        models.PaymentStatusExpired,
				"webhook_event": "payment_expired",
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		addressRepo := NewWalletAddressRepo(tx)
		for _, session := range expired {
			if session.SelectedChainID == nil || strings.TrimSpace(session.DepositAddress) == "" {
				continue
			}
			if err := addressRepo.ReleaseExpiredCheckoutAddress(ctx, *session.SelectedChainID, session.DepositAddress, now); err != nil {
				return err
			}
		}
		return nil
	})
	return int64(len(expired)), err
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
