package handlers

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"core/constants"
	"core/models"
	"core/repositories"
	"core/services/networkops"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var errOutboundPolicyRejected = errors.New("outbound policy rejects transfer")

type outboundPolicyCheck struct {
	MerchantID             uuid.UUID
	DomainID               *uuid.UUID
	ResourceType           string
	ResourceID             string
	Action                 string
	Chain                  string
	Token                  *string
	Symbol                 string
	ToAddress              string
	AmountRaw              string
	CurrentAlreadyRecorded bool
}

type outboundPolicyRepos struct {
	PolicyRepo       *repositories.OutboundPolicyRepo
	WithdrawalRepo   *repositories.WithdrawalRequestRepo
	RefundRepo       *repositories.RefundRepo
	NetworkStateRepo *repositories.NetworkOperationalStateRepo
}

func enforceDealerOutboundPolicy(ctx context.Context, deps DealerDeps, check outboundPolicyCheck) error {
	return enforceOutboundPolicy(ctx, outboundPolicyRepos{
		PolicyRepo:       deps.OutboundPolicyRepo,
		WithdrawalRepo:   deps.WithdrawalRepo,
		RefundRepo:       deps.RefundRepo,
		NetworkStateRepo: deps.NetworkOperationalStateRepo,
	}, check)
}

func enforceV1OutboundPolicy(ctx context.Context, deps V1APIDeps, check outboundPolicyCheck) error {
	return enforceOutboundPolicy(ctx, outboundPolicyRepos{
		PolicyRepo:       deps.OutboundPolicyRepo,
		WithdrawalRepo:   deps.WithdrawalRepo,
		RefundRepo:       deps.RefundRepo,
		NetworkStateRepo: deps.NetworkOperationalStateRepo,
	}, check)
}

func enforceOutboundPolicy(ctx context.Context, repos outboundPolicyRepos, check outboundPolicyCheck) error {
	if chainID := chainSlugToID(check.Chain); constants.IsSupportedChainID(chainID) {
		if err := networkops.RequireWithdrawals(ctx, repos.NetworkStateRepo, chainID); err != nil {
			return err
		}
	}
	amountRaw, err := outboundPolicyAmount(check.AmountRaw)
	if err != nil {
		return err
	}
	if err := outboundEnforceStoredPolicy(ctx, repos, amountRaw, check); err != nil {
		return err
	}
	if outboundScopedEnvBool([]string{"OUTBOUND_EMERGENCY_FREEZE", "OUTBOUND_FREEZE", "OUTBOUND_TRANSFERS_FROZEN"}, check) {
		return outboundPolicyError("emergency freeze active")
	}
	if outboundScopedEnvBool([]string{"OUTBOUND_ADDRESS_WHITELIST_REQUIRED", "REQUIRE_OUTBOUND_ADDRESS_WHITELIST"}, check) {
		if err := outboundEnforceAddressWhitelist(check); err != nil {
			return err
		}
	}
	if err := outboundEnforceAmountLimit(amountRaw, check); err != nil {
		return err
	}
	if err := outboundEnforceVelocityLimit(ctx, repos, amountRaw, check); err != nil {
		return err
	}
	return nil
}

func outboundEnforceStoredPolicy(ctx context.Context, repos outboundPolicyRepos, amountRaw *big.Int, check outboundPolicyCheck) error {
	if repos.PolicyRepo == nil {
		return nil
	}
	setting, err := repos.PolicyRepo.FindEffective(ctx, check.MerchantID, check.DomainID, check.Chain, check.Token)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if setting == nil {
		return nil
	}
	if setting.EmergencyFrozen {
		return outboundPolicyError("emergency freeze active")
	}
	if setting.WhitelistRequired {
		destination := outboundNormalizeAddress(check.ToAddress)
		if destination != "" {
			ok, err := repos.PolicyRepo.IsAddressWhitelisted(ctx, check.MerchantID, check.DomainID, check.Chain, check.Token, destination)
			if err != nil {
				return err
			}
			if !ok {
				return outboundPolicyError("destination address is not whitelisted")
			}
		}
	}
	if strings.TrimSpace(setting.MaxAmountRaw) != "" {
		limit, err := outboundParsePositiveRaw(setting.MaxAmountRaw, "amount limit configuration is invalid")
		if err != nil {
			return err
		}
		if amountRaw.Cmp(limit) > 0 {
			return outboundPolicyError("amount exceeds configured limit")
		}
	}
	if strings.TrimSpace(setting.VelocityLimitRaw) != "" {
		limit, err := outboundParsePositiveRaw(setting.VelocityLimitRaw, "amount limit configuration is invalid")
		if err != nil {
			return err
		}
		window := time.Duration(setting.VelocityWindowSecs) * time.Second
		if window <= 0 {
			window = 24 * time.Hour
		}
		return outboundCheckVelocityLimit(ctx, repos, amountRaw, check, limit, window)
	}
	return nil
}

func outboundPolicyCheckFromWithdrawal(action string, request *models.WithdrawalRequest, currentAlreadyRecorded bool) outboundPolicyCheck {
	if request == nil {
		return outboundPolicyCheck{}
	}
	return outboundPolicyCheck{
		MerchantID:             request.MerchantID,
		DomainID:               request.DomainID,
		ResourceType:           "withdrawal",
		ResourceID:             request.ID.String(),
		Action:                 action,
		Chain:                  request.Chain,
		Token:                  request.Token,
		Symbol:                 request.Symbol,
		ToAddress:              request.ToAddress,
		AmountRaw:              request.AmountRaw,
		CurrentAlreadyRecorded: currentAlreadyRecorded,
	}
}

func outboundPolicyCheckFromRefund(action string, refund *models.Refund, toAddress string, currentAlreadyRecorded bool) outboundPolicyCheck {
	if refund == nil {
		return outboundPolicyCheck{}
	}
	domainID := refund.DomainID
	return outboundPolicyCheck{
		MerchantID:             refund.MerchantID,
		DomainID:               &domainID,
		ResourceType:           "refund",
		ResourceID:             refund.ID.String(),
		Action:                 action,
		Chain:                  refund.Chain,
		Token:                  refund.Token,
		Symbol:                 refund.Symbol,
		ToAddress:              strings.TrimSpace(toAddress),
		AmountRaw:              refund.AmountRaw,
		CurrentAlreadyRecorded: currentAlreadyRecorded,
	}
}

func outboundEnforceAddressWhitelist(check outboundPolicyCheck) error {
	destination := outboundNormalizeAddress(check.ToAddress)
	if destination == "" {
		return nil
	}
	allowed := outboundScopedEnvValues([]string{"OUTBOUND_ADDRESS_WHITELIST"}, check)
	for _, raw := range allowed {
		for _, candidate := range outboundSplitList(raw) {
			if outboundNormalizeAddress(candidate) == destination {
				return nil
			}
		}
	}
	return outboundPolicyError("destination address is not whitelisted")
}

func outboundEnforceAmountLimit(amountRaw *big.Int, check outboundPolicyCheck) error {
	limit, configured, err := outboundScopedEnvBig([]string{"OUTBOUND_MAX_AMOUNT_RAW", "OUTBOUND_PER_TRANSFER_LIMIT_RAW"}, check)
	if err != nil {
		return err
	}
	if configured && amountRaw.Cmp(limit) > 0 {
		return outboundPolicyError("amount exceeds configured limit")
	}
	return nil
}

func outboundEnforceVelocityLimit(ctx context.Context, repos outboundPolicyRepos, amountRaw *big.Int, check outboundPolicyCheck) error {
	limit, configured, err := outboundScopedEnvBig([]string{"OUTBOUND_VELOCITY_LIMIT_RAW", "OUTBOUND_DAILY_LIMIT_RAW"}, check)
	if err != nil {
		return err
	}
	if !configured {
		return nil
	}
	window, err := outboundVelocityWindow(check)
	if err != nil {
		return err
	}
	return outboundCheckVelocityLimit(ctx, repos, amountRaw, check, limit, window)
}

func outboundCheckVelocityLimit(ctx context.Context, repos outboundPolicyRepos, amountRaw *big.Int, check outboundPolicyCheck, limit *big.Int, window time.Duration) error {
	if repos.WithdrawalRepo == nil || repos.RefundRepo == nil {
		return outboundPolicyError("velocity check unavailable")
	}
	since := time.Now().UTC().Add(-window)
	withdrawalTotal, err := repos.WithdrawalRepo.SumActiveAmountRawByMerchantSince(ctx, check.MerchantID, check.Chain, check.Token, check.Symbol, since)
	if err != nil {
		return err
	}
	refundTotal, err := repos.RefundRepo.SumActiveAmountRawByMerchantSince(ctx, check.MerchantID, check.Chain, check.Token, check.Symbol, since)
	if err != nil {
		return err
	}
	total := new(big.Int).Add(withdrawalTotal, refundTotal)
	if !check.CurrentAlreadyRecorded {
		total.Add(total, amountRaw)
	}
	if total.Cmp(limit) > 0 {
		return outboundPolicyError("velocity limit exceeded")
	}
	return nil
}

func outboundVelocityWindow(check outboundPolicyCheck) (time.Duration, error) {
	raw := outboundScopedEnvValue([]string{"OUTBOUND_VELOCITY_WINDOW"}, check)
	if strings.TrimSpace(raw) == "" {
		return 24 * time.Hour, nil
	}
	window, err := time.ParseDuration(strings.TrimSpace(raw))
	if err == nil && window > 0 {
		return window, nil
	}
	hours, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
	if ok && hours.Sign() > 0 && hours.IsInt64() {
		return time.Duration(hours.Int64()) * time.Hour, nil
	}
	return 0, outboundPolicyError("velocity window configuration is invalid")
}

func outboundScopedEnvBig(bases []string, check outboundPolicyCheck) (*big.Int, bool, error) {
	raw := strings.TrimSpace(outboundScopedEnvValue(bases, check))
	if raw == "" {
		return nil, false, nil
	}
	value, err := outboundParsePositiveRaw(raw, "amount limit configuration is invalid")
	if err != nil {
		return nil, true, err
	}
	return value, true, nil
}

func outboundParsePositiveRaw(raw string, reason string) (*big.Int, error) {
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok || value.Sign() <= 0 {
		return nil, outboundPolicyError(reason)
	}
	return value, nil
}

func outboundPolicyAmount(raw string) (*big.Int, error) {
	value, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
	if !ok || value.Sign() <= 0 {
		return nil, outboundPolicyError("amount is invalid")
	}
	return value, nil
}

func outboundPolicyError(reason string) error {
	return fmt.Errorf("%w: %s", errOutboundPolicyRejected, reason)
}

func outboundScopedEnvBool(bases []string, check outboundPolicyCheck) bool {
	raw := strings.TrimSpace(outboundScopedEnvValue(bases, check))
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func outboundScopedEnvValue(bases []string, check outboundPolicyCheck) string {
	for _, suffix := range outboundEnvSuffixes(check) {
		for _, base := range bases {
			key := strings.TrimSpace(base) + suffix
			if value := strings.TrimSpace(os.Getenv(key)); value != "" {
				return value
			}
		}
	}
	return ""
}

func outboundScopedEnvValues(bases []string, check outboundPolicyCheck) []string {
	values := make([]string, 0)
	seen := map[string]struct{}{}
	for _, suffix := range outboundEnvSuffixes(check) {
		for _, base := range bases {
			key := strings.TrimSpace(base) + suffix
			value := strings.TrimSpace(os.Getenv(key))
			if value == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			values = append(values, value)
		}
	}
	return values
}

func outboundEnvSuffixes(check outboundPolicyCheck) []string {
	chain := outboundEnvToken(check.Chain)
	merchant := outboundEnvToken(check.MerchantID.String())
	domain := ""
	if check.DomainID != nil && *check.DomainID != uuid.Nil {
		domain = outboundEnvToken(check.DomainID.String())
	}
	suffixes := make([]string, 0, 7)
	if domain != "" && chain != "" {
		suffixes = append(suffixes, "_DOMAIN_"+domain+"_"+chain)
	}
	if merchant != "" && chain != "" {
		suffixes = append(suffixes, "_MERCHANT_"+merchant+"_"+chain)
	}
	if chain != "" {
		suffixes = append(suffixes, "_"+chain)
	}
	if domain != "" {
		suffixes = append(suffixes, "_DOMAIN_"+domain)
	}
	if merchant != "" {
		suffixes = append(suffixes, "_MERCHANT_"+merchant)
	}
	suffixes = append(suffixes, "")
	return suffixes
}

func outboundEnvToken(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func outboundSplitList(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}

func outboundNormalizeAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

func logV1OutboundPolicyFailure(c fiber.Ctx, repo *repositories.ActivityLogRepo, domain models.Domain, event string, subjectType string, subjectID string, err error) {
	if err == nil {
		return
	}
	logDealerActivity(c, repo, &domain.MerchantID, "api", domain.DomainURL, event, "failed", subjectType, subjectID, err.Error())
}
