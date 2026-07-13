package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestOutboundPolicyEmergencyFreezeRejects(t *testing.T) {
	clearOutboundPolicyTestEnv(t)
	t.Setenv("OUTBOUND_EMERGENCY_FREEZE", "true")

	err := enforceOutboundPolicy(context.Background(), outboundPolicyRepos{}, outboundPolicyTestCheck())
	if !errors.Is(err, errOutboundPolicyRejected) || !strings.Contains(err.Error(), "emergency freeze active") {
		t.Fatalf("freeze err = %v, want emergency freeze rejection", err)
	}
}

func TestOutboundPolicyAddressWhitelistIsEnforcedWhenRequired(t *testing.T) {
	clearOutboundPolicyTestEnv(t)
	t.Setenv("OUTBOUND_ADDRESS_WHITELIST_REQUIRED", "true")
	t.Setenv("OUTBOUND_ADDRESS_WHITELIST_ETHEREUM", "0xallowed;0xsecond")

	rejected := outboundPolicyTestCheck()
	rejected.ToAddress = "0xother"
	err := enforceOutboundPolicy(context.Background(), outboundPolicyRepos{}, rejected)
	if !errors.Is(err, errOutboundPolicyRejected) || !strings.Contains(err.Error(), "not whitelisted") {
		t.Fatalf("whitelist reject err = %v, want whitelist rejection", err)
	}

	accepted := outboundPolicyTestCheck()
	accepted.ToAddress = "0xAllowed"
	if err := enforceOutboundPolicy(context.Background(), outboundPolicyRepos{}, accepted); err != nil {
		t.Fatalf("allowed address rejected: %v", err)
	}
}

func TestOutboundPolicyAmountLimitRejectsOverLimit(t *testing.T) {
	clearOutboundPolicyTestEnv(t)
	t.Setenv("OUTBOUND_MAX_AMOUNT_RAW", "99")

	check := outboundPolicyTestCheck()
	check.AmountRaw = "100"
	err := enforceOutboundPolicy(context.Background(), outboundPolicyRepos{}, check)
	if !errors.Is(err, errOutboundPolicyRejected) || !strings.Contains(err.Error(), "amount exceeds") {
		t.Fatalf("amount limit err = %v, want over-limit rejection", err)
	}
}

func TestOutboundPolicyVelocityLimitFailsClosedWithoutRepos(t *testing.T) {
	clearOutboundPolicyTestEnv(t)
	t.Setenv("OUTBOUND_VELOCITY_LIMIT_RAW", "1000")

	err := enforceOutboundPolicy(context.Background(), outboundPolicyRepos{}, outboundPolicyTestCheck())
	if !errors.Is(err, errOutboundPolicyRejected) || !strings.Contains(err.Error(), "velocity check unavailable") {
		t.Fatalf("velocity err = %v, want unavailable rejection", err)
	}
}

func outboundPolicyTestCheck() outboundPolicyCheck {
	domainID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	return outboundPolicyCheck{
		MerchantID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DomainID:     &domainID,
		ResourceType: "payout",
		ResourceID:   "policy-test",
		Action:       "payout.create",
		Chain:        "ethereum",
		Symbol:       "ETH",
		ToAddress:    "0xto",
		AmountRaw:    "10",
	}
}

func clearOutboundPolicyTestEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OUTBOUND_EMERGENCY_FREEZE",
		"OUTBOUND_FREEZE",
		"OUTBOUND_TRANSFERS_FROZEN",
		"OUTBOUND_ADDRESS_WHITELIST_REQUIRED",
		"REQUIRE_OUTBOUND_ADDRESS_WHITELIST",
		"OUTBOUND_ADDRESS_WHITELIST",
		"OUTBOUND_ADDRESS_WHITELIST_ETHEREUM",
		"OUTBOUND_MAX_AMOUNT_RAW",
		"OUTBOUND_PER_TRANSFER_LIMIT_RAW",
		"OUTBOUND_VELOCITY_LIMIT_RAW",
		"OUTBOUND_DAILY_LIMIT_RAW",
		"OUTBOUND_VELOCITY_WINDOW",
	} {
		t.Setenv(key, "")
	}
}
