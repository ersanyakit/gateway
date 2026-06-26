package handlers

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"core/helpers"
	"core/models"
	"core/types"

	"github.com/gofiber/fiber/v3"
)

type v1DomainLookup interface {
	FindByAPIKey(types.DomainParams) (*models.Domain, error)
	FindByAPISecret(types.DomainParams) (*models.Domain, error)
}

var v1SignedReplayGuard = newV1SignatureReplayGuard(time.Duration(helpers.TimeSkewSec*2) * time.Second)

type v1SignatureReplayGuard struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]time.Time
}

func newV1SignatureReplayGuard(ttl time.Duration) *v1SignatureReplayGuard {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &v1SignatureReplayGuard{
		ttl:     ttl,
		entries: make(map[string]time.Time),
	}
}

func (g *v1SignatureReplayGuard) Accept(domainID string, method string, path string, timestamp string, signature string) bool {
	if g == nil {
		return true
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()

	for key, seenAt := range g.entries {
		if now.Sub(seenAt) > g.ttl {
			delete(g.entries, key)
		}
	}

	key := strings.Join([]string{
		domainID,
		strings.ToUpper(strings.TrimSpace(method)),
		strings.TrimSpace(path),
		strings.TrimSpace(timestamp),
		strings.TrimSpace(signature),
	}, "\x00")
	if _, exists := g.entries[key]; exists {
		return false
	}
	g.entries[key] = now
	return true
}

func v1LogAuthFailure(c fiber.Ctx, domain *models.Domain, category string, err error) {
	domainID := ""
	if domain != nil {
		domainID = domain.ID.String()
	}
	requestID := strings.TrimSpace(c.Get("X-Request-ID"))
	log.Printf(
		"v1_auth_failure domain_id=%q endpoint=%q method=%q category=%q request_id=%q error=%q",
		domainID,
		c.Path(),
		c.Method(),
		category,
		requestID,
		sanitizeV1AuthError(err),
	)
}

func sanitizeV1AuthError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, sensitive := range []string{"X-API-Secret", "X-Gateway-Signature"} {
		msg = strings.ReplaceAll(msg, sensitive, sensitive)
	}
	return msg
}

func v1RequestReplayAccepted(domain *models.Domain, c fiber.Ctx, timestamp string, signature string) bool {
	if domain == nil {
		return true
	}
	if _, err := strconv.ParseInt(timestamp, 10, 64); err != nil {
		return false
	}
	return v1SignedReplayGuard.Accept(domain.ID.String(), c.Method(), c.Path(), timestamp, signature)
}

func v1ReplayError() error {
	return fmt.Errorf("request signature replayed")
}
