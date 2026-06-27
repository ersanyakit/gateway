package signer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync"

	"core/constants"
)

const (
	ModeSoftware = "software"

	PolicyDecisionAllow = "allow"
	PolicyDecisionDeny  = "deny"

	OutcomeAllowed  = "allowed"
	OutcomeRejected = "rejected"
)

var (
	ErrProductionSoftwareSignerDisabled  = errors.New("production software signer is disabled")
	ErrExternalSignerIntegrationRequired = errors.New("external signer integration is required")
	ErrUnsupportedSignerMode             = errors.New("unsupported signer mode")
)

type Request struct {
	Chain          string
	ChainID        int
	KeyReference   string
	DerivationPath string
	Intent         string
	AmountRaw      string
	Destination    string
	ActorID        string
	JobID          string
	CorrelationID  string
	PolicyMetadata map[string]string
}

type AuditContext struct {
	ActorID       string
	JobID         string
	CorrelationID string
}

type auditContextKey struct{}

type Decision struct {
	SignerMode     string
	KeyReference   string
	PolicyDecision string
	Outcome        string
	Reason         string
}

var auditSink = struct {
	sync.Mutex
	output io.Writer
}{
	output: log.Writer(),
}

func WithAuditContext(ctx context.Context, audit AuditContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, auditContextKey{}, audit)
}

func AuditContextFrom(ctx context.Context) AuditContext {
	if ctx == nil {
		return AuditContext{}
	}
	if audit, ok := ctx.Value(auditContextKey{}).(AuditContext); ok {
		return audit
	}
	return AuditContext{}
}

func SetAuditOutput(w io.Writer) func() {
	auditSink.Lock()
	previous := auditSink.output
	if w == nil {
		auditSink.output = io.Discard
	} else {
		auditSink.output = w
	}
	auditSink.Unlock()

	return func() {
		auditSink.Lock()
		auditSink.output = previous
		auditSink.Unlock()
	}
}

func Authorize(ctx context.Context, req Request) (Decision, error) {
	auditContext := AuditContextFrom(ctx)
	if strings.TrimSpace(req.ActorID) == "" {
		req.ActorID = auditContext.ActorID
	}
	if strings.TrimSpace(req.JobID) == "" {
		req.JobID = auditContext.JobID
	}
	if strings.TrimSpace(req.CorrelationID) == "" {
		req.CorrelationID = auditContext.CorrelationID
	}

	mode := CurrentMode()
	decision := Decision{
		SignerMode:     mode,
		KeyReference:   nonSecretKeyReference(req),
		PolicyDecision: PolicyDecisionAllow,
		Outcome:        OutcomeAllowed,
	}

	var err error
	switch {
	case IsProduction() && mode == ModeSoftware:
		decision.PolicyDecision = PolicyDecisionDeny
		decision.Outcome = OutcomeRejected
		decision.Reason = "production_software_signer_disabled"
		err = ErrProductionSoftwareSignerDisabled
	case IsExternalMode(mode):
		decision.PolicyDecision = PolicyDecisionDeny
		decision.Outcome = OutcomeRejected
		decision.Reason = "external_signer_integration_required"
		err = ErrExternalSignerIntegrationRequired
	case mode != ModeSoftware:
		decision.PolicyDecision = PolicyDecisionDeny
		decision.Outcome = OutcomeRejected
		decision.Reason = "unsupported_signer_mode"
		err = fmt.Errorf("%w: %s", ErrUnsupportedSignerMode, mode)
	}

	writeAudit(req, decision)
	return decision, err
}

func ProductionReadiness() (bool, string, error) {
	if !IsProduction() {
		return true, "non-production signer policy", nil
	}

	mode := CurrentMode()
	switch {
	case mode == ModeSoftware:
		return false, "software signer is blocked in production", ErrProductionSoftwareSignerDisabled
	case IsExternalMode(mode):
		return false, fmt.Sprintf("SIGNER_MODE=%s is declared but no external signer adapter is active", mode), ErrExternalSignerIntegrationRequired
	default:
		return false, fmt.Sprintf("SIGNER_MODE=%s", mode), fmt.Errorf("%w: %s", ErrUnsupportedSignerMode, mode)
	}
}

func CurrentMode() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("SIGNER_MODE")))
	if mode == "" {
		return ModeSoftware
	}
	return mode
}

func IsProduction() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production")
}

func IsExternalMode(mode string) bool {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)
	switch normalized {
	case "kms", "hsm", "mpc", "vault", "external", "custody", "external_custody", "fireblocks", "bitgo":
		return true
	default:
		return false
	}
}

func KeyReference(chainID constants.ChainID, derivationPath string) string {
	if configured := strings.TrimSpace(os.Getenv("SIGNER_KEY_REF")); configured != "" {
		return configured
	}
	derivationPath = strings.TrimSpace(derivationPath)
	if derivationPath != "" {
		return fmt.Sprintf("chain:%d:path:%s", chainID, derivationPath)
	}
	return fmt.Sprintf("chain:%d:unspecified", chainID)
}

func nonSecretKeyReference(req Request) string {
	if strings.TrimSpace(req.KeyReference) != "" {
		return strings.TrimSpace(req.KeyReference)
	}
	if strings.TrimSpace(req.DerivationPath) != "" {
		return "path:" + strings.TrimSpace(req.DerivationPath)
	}
	return "unspecified"
}

func writeAudit(req Request, decision Decision) {
	fields := map[string]string{
		"event":           "signer_policy_decision",
		"signer_mode":     decision.SignerMode,
		"key_reference":   decision.KeyReference,
		"chain":           req.Chain,
		"chain_id":        fmt.Sprintf("%d", req.ChainID),
		"derivation_path": req.DerivationPath,
		"intent":          req.Intent,
		"amount_raw":      req.AmountRaw,
		"destination":     req.Destination,
		"actor_id":        req.ActorID,
		"job_id":          req.JobID,
		"correlation_id":  req.CorrelationID,
		"policy_decision": decision.PolicyDecision,
		"outcome":         decision.Outcome,
		"reason":          decision.Reason,
		"metadata_keys":   safeMetadataKeys(req.PolicyMetadata),
	}

	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, key := range keys {
		value := sanitizeAuditValue(key, fields[key])
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
	}
	b.WriteByte('\n')

	auditSink.Lock()
	_, _ = auditSink.output.Write([]byte(b.String()))
	auditSink.Unlock()
}

func safeMetadataKeys(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		if isSensitiveKey(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func sanitizeAuditValue(key string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if isSensitiveKey(key) {
		return "[redacted]"
	}
	value = strings.ReplaceAll(value, "\n", "_")
	value = strings.ReplaceAll(value, "\r", "_")
	value = strings.ReplaceAll(value, "\t", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"secret", "private", "mnemonic", "seed", "signature", "raw_payload", "authorization", "body"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}
