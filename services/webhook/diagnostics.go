package webhook

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"unicode"
)

const maxDeliveryDiagnosticLength = 512

func FailureCategory(err error) string {
	if err == nil {
		return ""
	}
	if IsPermanent(err) {
		return "permanent"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "timeout"), strings.Contains(text, "deadline exceeded"):
		return "timeout"
	case strings.Contains(text, "webhook returned http"):
		return "http_response"
	case strings.Contains(text, "connection refused"), strings.Contains(text, "no such host"), strings.Contains(text, "network"):
		return "network"
	default:
		return "transient"
	}
}

func SanitizeDeliveryError(err error) string {
	if err == nil {
		return ""
	}
	return SanitizeDeliveryText(err.Error())
}

func RedactDeliveryError(err error) error {
	if err == nil {
		return nil
	}
	prefix := "transient"
	if IsPermanent(err) {
		prefix = "permanent"
	}
	return redactedDeliveryError{
		message:   fmt.Sprintf("%s: %s", prefix, SanitizeDeliveryText(err.Error())),
		cause:     err,
		permanent: IsPermanent(err),
	}
}

type redactedDeliveryError struct {
	message   string
	cause     error
	permanent bool
}

func (e redactedDeliveryError) Error() string {
	return e.message
}

func (e redactedDeliveryError) Unwrap() error {
	return e.cause
}

func (e redactedDeliveryError) Permanent() bool {
	return e.permanent
}

func SanitizeDeliveryText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lowered := strings.ToLower(value)
	for _, marker := range []string{
		"api_secret",
		"authorization",
		"mnemonic",
		"private_key",
		"raw_signature",
		"secret",
		"signature",
		"sha256",
		"webhook_secret",
	} {
		if strings.Contains(lowered, marker) {
			return "redacted sensitive delivery error"
		}
	}

	var b strings.Builder
	b.Grow(len(value))
	lastSpace := false
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
		if b.Len() >= maxDeliveryDiagnosticLength {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > maxDeliveryDiagnosticLength {
		out = out[:maxDeliveryDiagnosticLength]
	}
	return out
}
