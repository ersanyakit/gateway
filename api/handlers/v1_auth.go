package handlers

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"core/helpers"
	"core/models"
	"core/types"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type v1DomainLookup interface {
	FindByAPIKey(types.DomainParams) (*models.Domain, error)
	FindByAPISecret(types.DomainParams) (*models.Domain, error)
}

var v1SignedReplayGuard = newV1SignatureReplayGuard(time.Duration(helpers.TimeSkewSec*2) * time.Second)

const v1ReplayGuardMaxEntries = 10000

var v1SensitiveLogPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(x-api-key\s*[:=]\s*)\S+`),
	regexp.MustCompile(`(?i)(x-api-secret\s*[:=]\s*)\S+`),
	regexp.MustCompile(`(?i)(x-gateway-signature\s*[:=]\s*)\S+`),
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)\S+`),
	regexp.MustCompile(`(?i)(api[_ -]?secret\s*[:=]\s*)\S+`),
	regexp.MustCompile(`(?i)(signature\s*[:=]\s*)\S+`),
}

type v1SignatureReplayGuard struct {
	mu           sync.Mutex
	ttl          time.Duration
	cleanupEvery time.Duration
	lastCleanup  time.Time
	maxEntries   int
	entries      map[string]time.Time
}

func newV1SignatureReplayGuard(ttl time.Duration) *v1SignatureReplayGuard {
	if ttl <= 0 {
		ttl = time.Minute
	}
	cleanupEvery := ttl / 4
	if cleanupEvery < time.Second {
		cleanupEvery = time.Second
	}
	return &v1SignatureReplayGuard{
		ttl:          ttl,
		cleanupEvery: cleanupEvery,
		maxEntries:   v1ReplayGuardMaxEntries,
		entries:      make(map[string]time.Time),
	}
}

func (g *v1SignatureReplayGuard) Accept(domainID string, method string, path string, timestamp string, signature string) bool {
	if g == nil {
		return true
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()

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
	if g.lastCleanup.IsZero() || now.Sub(g.lastCleanup) >= g.cleanupEvery {
		g.cleanupExpiredLocked(now)
		g.lastCleanup = now
	}
	if g.maxEntries > 0 && len(g.entries) >= g.maxEntries {
		g.cleanupExpiredLocked(now)
		if len(g.entries) >= g.maxEntries {
			g.evictOldestLocked(len(g.entries) - g.maxEntries + 1)
		}
	}
	g.entries[key] = now
	return true
}

func (g *v1SignatureReplayGuard) cleanupExpiredLocked(now time.Time) {
	for key, seenAt := range g.entries {
		if now.Sub(seenAt) > g.ttl {
			delete(g.entries, key)
		}
	}
}

func (g *v1SignatureReplayGuard) evictOldestLocked(count int) {
	for count > 0 {
		oldestKey := ""
		var oldest time.Time
		for key, seenAt := range g.entries {
			if oldestKey == "" || seenAt.Before(oldest) {
				oldestKey = key
				oldest = seenAt
			}
		}
		if oldestKey == "" {
			return
		}
		delete(g.entries, oldestKey)
		count--
	}
}

func v1LogAuthFailure(c fiber.Ctx, domain *models.Domain, category string, err error) {
	domainID := ""
	if domain != nil {
		domainID = domain.ID.String()
	}
	requestID := v1RequestID(c)
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

func v1RequestID(c fiber.Ctx) string {
	requestID := strings.TrimSpace(c.Get("X-Request-ID"))
	if requestID == "" {
		requestID = uuid.NewString()
	}
	c.Set("X-Request-ID", requestID)
	return requestID
}

func sanitizeV1AuthError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, pattern := range v1SensitiveLogPatterns {
		msg = pattern.ReplaceAllString(msg, "${1}[redacted]")
	}
	return msg
}

func v1CanonicalRequestTarget(c fiber.Ctx) string {
	target := strings.TrimSpace(c.OriginalURL())
	if target == "" {
		target = strings.TrimSpace(c.Path())
	}
	return target
}

func v1RequestReplayAccepted(domain *models.Domain, c fiber.Ctx, timestamp string, signature string) bool {
	if domain == nil {
		return true
	}
	if _, err := strconv.ParseInt(timestamp, 10, 64); err != nil {
		return false
	}
	return v1SignedReplayGuard.Accept(domain.ID.String(), c.Method(), v1CanonicalRequestTarget(c), timestamp, signature)
}

func v1ReplayError() error {
	return fmt.Errorf("request signature replayed")
}
