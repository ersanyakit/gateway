package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type v1SignedReplayStore interface {
	AcceptSignedRequestReplay(ctx context.Context, replayKey string, domainID uuid.UUID, expiresAt time.Time) (bool, error)
}

var v1SignedReplayGuard = newV1SignatureReplayGuard(time.Duration(helpers.TimeSkewSec*2) * time.Second)

const v1ReplayGuardMaxEntries = 10000

var v1SensitiveLogPatterns = compileV1SensitiveLogPatterns()

func compileV1SensitiveLogPatterns() []*regexp.Regexp {
	expressions := []string{
		`(?i)(x-api-key\s*[:=]\s*)\S+`,
		`(?i)(x-api-secret\s*[:=]\s*)\S+`,
		`(?i)(x-gateway-signature\s*[:=]\s*)\S+`,
		`(?i)(authorization\s*[:=]\s*bearer\s+)\S+`,
		`(?i)(api[_ -]?secret\s*[:=]\s*)\S+`,
		`(?i)(signature\s*[:=]\s*)\S+`,
	}
	patterns := make([]*regexp.Regexp, 0, len(expressions))
	for _, expression := range expressions {
		pattern, err := regexp.Compile(expression)
		if err != nil {
			log.Printf("v1 auth log sanitizer pattern error=%v", err)
			continue
		}
		patterns = append(patterns, pattern)
	}
	return patterns
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

func v1RequestReplayAccepted(domainRepo v1DomainLookup, domain *models.Domain, c fiber.Ctx, timestamp string, signature string) bool {
	if domain == nil {
		return true
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	method := c.Method()
	target := v1CanonicalRequestTarget(c)
	if store, ok := domainRepo.(v1SignedReplayStore); ok {
		ttl := time.Duration(helpers.TimeSkewSec*2) * time.Second
		expiresAt := time.Unix(ts, 0).Add(ttl)
		accepted, err := store.AcceptSignedRequestReplay(c.Context(), v1ReplayStoreKey(domain.ID.String(), method, target, timestamp, signature), domain.ID, expiresAt)
		if err != nil {
			v1LogAuthFailure(c, domain, "signature_replay_store_error", err)
			return false
		}
		return accepted
	}
	return v1SignedReplayGuard.Accept(domain.ID.String(), method, target, timestamp, signature)
}

func v1ReplayStoreKey(domainID string, method string, path string, timestamp string, signature string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(domainID),
		strings.ToUpper(strings.TrimSpace(method)),
		strings.TrimSpace(path),
		strings.TrimSpace(timestamp),
		strings.TrimSpace(signature),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func v1ReplayError() error {
	return fmt.Errorf("request signature replayed")
}
