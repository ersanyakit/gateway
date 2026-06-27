package signer

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAuthorizeAllowsDevelopmentSoftwareSignerAndAudits(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("SIGNER_MODE", "")
	t.Setenv("ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION", "")

	var audit bytes.Buffer
	restore := SetAuditOutput(&audit)
	defer restore()

	decision, err := Authorize(context.Background(), Request{
		Chain:          "ethereum",
		ChainID:        1,
		KeyReference:   "chain:1:path:m/44'/60'/7'/0/9",
		DerivationPath: "m/44'/60'/7'/0/9",
		Intent:         "withdraw.native",
		AmountRaw:      "1000000000000000000",
		Destination:    "0x1111111111111111111111111111111111111111",
		ActorID:        "admin-123",
		CorrelationID:  "corr-123",
		PolicyMetadata: map[string]string{
			"ledger_hold_id": "hold-123",
			"private_key":    "should-not-log",
			"mnemonic":       "seed words should not log",
			"raw_signature":  "signature-should-not-log",
		},
	})
	if err != nil {
		t.Fatalf("Authorize development software signer err=%v, want nil", err)
	}
	if decision.PolicyDecision != PolicyDecisionAllow {
		t.Fatalf("policy decision = %q, want %q", decision.PolicyDecision, PolicyDecisionAllow)
	}

	logged := audit.String()
	requireLogContains(t, logged,
		"signer_mode=software",
		"key_reference=chain:1:path:m/44'/60'/7'/0/9",
		"chain=ethereum",
		"intent=withdraw.native",
		"amount_raw=1000000000000000000",
		"destination=0x1111111111111111111111111111111111111111",
		"actor_id=admin-123",
		"correlation_id=corr-123",
		"metadata_keys=ledger_hold_id",
		"policy_decision=allow",
		"outcome=allowed",
	)
	for _, forbidden := range []string{"should-not-log", "seed words should not log", "signature-should-not-log"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("audit log contains secret %q: %s", forbidden, logged)
		}
	}
}

func TestAuthorizeRejectsUnsupportedSignerModeAndAudits(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("SIGNER_MODE", "browser-wallet")

	var audit bytes.Buffer
	restore := SetAuditOutput(&audit)
	defer restore()

	_, err := Authorize(context.Background(), Request{
		Chain:          "ethereum",
		ChainID:        1,
		KeyReference:   "chain:1:path:m/44'/60'/7'/0/9",
		DerivationPath: "m/44'/60'/7'/0/9",
		Intent:         "withdraw.native",
		AmountRaw:      "100",
		Destination:    "0x1111111111111111111111111111111111111111",
		ActorID:        "operator-unsupported",
		CorrelationID:  "corr-unsupported",
	})
	if !errors.Is(err, ErrUnsupportedSignerMode) {
		t.Fatalf("Authorize unsupported mode err=%v, want ErrUnsupportedSignerMode", err)
	}

	requireLogContains(t, audit.String(),
		"signer_mode=browser-wallet",
		"key_reference=chain:1:path:m/44'/60'/7'/0/9",
		"actor_id=operator-unsupported",
		"correlation_id=corr-unsupported",
		"policy_decision=deny",
		"outcome=rejected",
		"reason=unsupported_signer_mode",
	)
}

func TestAuthorizeBlocksProductionSoftwareSignerEvenWithLegacyOverride(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "software")
	t.Setenv("ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION", "true")

	var audit bytes.Buffer
	restore := SetAuditOutput(&audit)
	defer restore()

	_, err := Authorize(context.Background(), Request{
		Chain:         "bitcoin",
		ChainID:       0,
		KeyReference:  "chain:0:path:m/86'/0'/1'/0/2",
		Intent:        "withdraw.native",
		AmountRaw:     "1000",
		Destination:   "bc1qdestination",
		JobID:         "sweep-job-123",
		CorrelationID: "corr-production",
	})
	if !errors.Is(err, ErrProductionSoftwareSignerDisabled) {
		t.Fatalf("Authorize production software err=%v, want ErrProductionSoftwareSignerDisabled", err)
	}

	logged := audit.String()
	requireLogContains(t, logged,
		"signer_mode=software",
		"key_reference=chain:0:path:m/86'/0'/1'/0/2",
		"job_id=sweep-job-123",
		"correlation_id=corr-production",
		"policy_decision=deny",
		"outcome=rejected",
		"reason=production_software_signer_disabled",
	)
}

func TestAuthorizeExternalModeRequiresIntegration(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "kms")

	var audit bytes.Buffer
	restore := SetAuditOutput(&audit)
	defer restore()

	_, err := Authorize(context.Background(), Request{
		Chain:         "solana",
		ChainID:       501,
		KeyReference:  "chain:501:path:m/44'/501'/1'/2'",
		Intent:        "withdraw.token",
		AmountRaw:     "42",
		Destination:   "So11111111111111111111111111111111111111112",
		CorrelationID: "corr-kms",
	})
	if !errors.Is(err, ErrExternalSignerIntegrationRequired) {
		t.Fatalf("Authorize external mode err=%v, want ErrExternalSignerIntegrationRequired", err)
	}

	requireLogContains(t, audit.String(),
		"signer_mode=kms",
		"policy_decision=deny",
		"outcome=rejected",
		"reason=external_signer_integration_required",
	)
}

func TestAuthorizeFillsAuditFieldsFromContext(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("SIGNER_MODE", "software")

	var audit bytes.Buffer
	restore := SetAuditOutput(&audit)
	defer restore()

	ctx := WithAuditContext(context.Background(), AuditContext{
		ActorID:       "operator-7",
		JobID:         "sweep-job-456",
		CorrelationID: "corr-context",
	})

	_, err := Authorize(ctx, Request{
		Chain:        "tron",
		ChainID:      728126428,
		KeyReference: "chain:728126428:path:m/44'/195'/7'/0/1",
		Intent:       "sweep.native",
		AmountRaw:    "max",
		Destination:  "TRecipient",
	})
	if err != nil {
		t.Fatalf("Authorize err=%v, want nil", err)
	}

	requireLogContains(t, audit.String(),
		"actor_id=operator-7",
		"job_id=sweep-job-456",
		"correlation_id=corr-context",
	)
}

func TestProductionReadinessUsesSignerPolicy(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("SIGNER_MODE", "")
	ok, _, err := ProductionReadiness()
	if !ok || err != nil {
		t.Fatalf("development readiness ok=%v err=%v, want ok", ok, err)
	}

	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "software")
	ok, details, err := ProductionReadiness()
	if ok || !errors.Is(err, ErrProductionSoftwareSignerDisabled) {
		t.Fatalf("production software readiness ok=%v details=%q err=%v, want production software failure", ok, details, err)
	}

	t.Setenv("SIGNER_MODE", "vault")
	ok, details, err = ProductionReadiness()
	if ok || !errors.Is(err, ErrExternalSignerIntegrationRequired) {
		t.Fatalf("production vault readiness ok=%v details=%q err=%v, want external integration failure", ok, details, err)
	}

	t.Setenv("SIGNER_MODE", "external-custody")
	ok, details, err = ProductionReadiness()
	if ok || !errors.Is(err, ErrExternalSignerIntegrationRequired) {
		t.Fatalf("production external custody readiness ok=%v details=%q err=%v, want external integration failure", ok, details, err)
	}

	t.Setenv("SIGNER_MODE", "browser-wallet")
	ok, details, err = ProductionReadiness()
	if ok || !errors.Is(err, ErrUnsupportedSignerMode) || !strings.Contains(details, "browser-wallet") {
		t.Fatalf("production unsupported signer readiness ok=%v details=%q err=%v, want unsupported mode failure", ok, details, err)
	}
}

func requireLogContains(t *testing.T, logged string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(logged, needle) {
			t.Fatalf("audit log missing %q:\n%s", needle, logged)
		}
	}
}
