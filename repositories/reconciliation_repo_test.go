package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestReconciliationRepoCreateScopedOpenIfMissingDedupesActiveScope(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ReconciliationJob{}); err != nil {
		t.Fatalf("automigrate reconciliation jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewReconciliationRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	scope := ReconciliationScope{
		ChainID:             constants.Ethereum,
		FromBlock:           100,
		ToBlock:             105,
		Reason:              "ledger_invariant:test",
		MerchantID:          &merchantID,
		DomainID:            &domainID,
		ScopeKey:            "ledger_invariant:" + uuid.NewString(),
		ResourceType:        "ledger_invariant",
		ResourceID:          "idem-key-1",
		AffectedResourceIDs: []string{"idem-key-1", "ledger-entry-1"},
		Evidence: map[string]any{
			"merchant_id": merchantID.String(),
			"domain_id":   domainID.String(),
			"net_raw":     "10",
		},
	}

	first, created, err := repo.CreateScopedOpenIfMissing(ctx, scope)
	if err != nil {
		t.Fatalf("create scoped job: %v", err)
	}
	if !created {
		t.Fatal("first scoped job should be created")
	}
	if first.ScopeKey != scope.ScopeKey || first.MerchantID == nil || *first.MerchantID != merchantID || first.DomainID == nil || *first.DomainID != domainID {
		t.Fatalf("scoped job fields = %#v", first)
	}
	if first.AffectedResourceIDsJSON == "" || first.EvidenceJSON == "" {
		t.Fatalf("scoped job missing affected/evidence json: %#v", first)
	}

	again, created, err := repo.CreateScopedOpenIfMissing(ctx, scope)
	if err != nil {
		t.Fatalf("dedupe scoped job: %v", err)
	}
	if created || again.ID != first.ID {
		t.Fatalf("active scoped job should dedupe to first row, created=%v id=%s first=%s", created, again.ID, first.ID)
	}

	if err := repo.MarkNeedsOperatorAction(ctx, first.ID, map[string]any{"review": true}, "operator_review"); err != nil {
		t.Fatalf("mark needs operator action: %v", err)
	}
	activeAgain, created, err := repo.CreateScopedOpenIfMissing(ctx, scope)
	if err != nil {
		t.Fatalf("dedupe needs-operator scoped job: %v", err)
	}
	if created || activeAgain.ID != first.ID {
		t.Fatalf("needs_operator_action job should remain active for dedupe, created=%v id=%s first=%s", created, activeAgain.ID, first.ID)
	}

	if err := repo.MarkResolvedWithEvidence(ctx, first.ID, map[string]any{"resolution": "balanced"}, "resolved_clean"); err != nil {
		t.Fatalf("resolve scoped job: %v", err)
	}
	afterResolved, created, err := repo.CreateScopedOpenIfMissing(ctx, scope)
	if err != nil {
		t.Fatalf("create after resolved scoped job: %v", err)
	}
	if !created || afterResolved.ID == first.ID {
		t.Fatalf("resolved job should not suppress new issue, created=%v new=%s first=%s", created, afterResolved.ID, first.ID)
	}

	if err := repo.MarkFailed(ctx, afterResolved.ID, errors.New("operator evidence unavailable")); err != nil {
		t.Fatalf("mark failed scoped job: %v", err)
	}
	afterFailed, created, err := repo.CreateScopedOpenIfMissing(ctx, scope)
	if err != nil {
		t.Fatalf("create after failed scoped job: %v", err)
	}
	if !created || afterFailed.ID == afterResolved.ID {
		t.Fatalf("failed job should not suppress new issue, created=%v new=%s failed=%s", created, afterFailed.ID, afterResolved.ID)
	}
}

func TestReconciliationRepoListPageSourceContract(t *testing.T) {
	sourceBytes, err := os.ReadFile("reconciliation_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	bodyStart := strings.Index(source, "func (r *ReconciliationRepo) ListPage")
	if bodyStart < 0 {
		t.Fatal("ListPage missing")
	}
	body := source[bodyStart:]
	for _, token := range []string{
		`query.Where("status = ?", status)`,
		`query.Count(&total)`,
		`Order("created_at DESC, id DESC")`,
		"Limit(limit)",
		"Offset((page - 1) * limit)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("reconciliation ListPage source missing %q", token)
		}
	}
}

func TestReconciliationRepoCreateScopedOpenIfMissingDedupesConcurrentActiveScope(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ReconciliationJob{}); err != nil {
		t.Fatalf("automigrate reconciliation jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewReconciliationRepo(db)
	scopeKey := "ledger_invariant:concurrent:" + uuid.NewString()
	scope := ReconciliationScope{
		ChainID:             constants.Ethereum,
		FromBlock:           200,
		ToBlock:             205,
		Reason:              "ledger_invariant:concurrent",
		ScopeKey:            scopeKey,
		ResourceType:        "ledger_invariant",
		ResourceID:          "concurrent-idem-key",
		AffectedResourceIDs: []string{"concurrent-idem-key"},
		Evidence: map[string]any{
			"net_raw": "10",
		},
	}

	type createResult struct {
		id      uuid.UUID
		created bool
		err     error
	}
	const callers = 12
	start := make(chan struct{})
	results := make(chan createResult, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			job, created, err := repo.CreateScopedOpenIfMissing(ctx, scope)
			result := createResult{created: created, err: err}
			if job != nil {
				result.id = job.ID
			}
			results <- result
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	createdCount := 0
	ids := map[uuid.UUID]struct{}{}
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent create scoped job: %v", result.err)
		}
		if result.created {
			createdCount++
		}
		ids[result.id] = struct{}{}
	}
	if createdCount != 1 || len(ids) != 1 {
		t.Fatalf("concurrent scoped dedupe created=%d unique_ids=%d ids=%v", createdCount, len(ids), ids)
	}

	var count int64
	if err := db.WithContext(ctx).Model(&models.ReconciliationJob{}).Where("scope_key = ?", scopeKey).Count(&count).Error; err != nil {
		t.Fatalf("count scoped jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("scoped job count = %d, want 1", count)
	}
}

func TestReconciliationRepoCreateOpenIfMissingDedupesLegacyActiveJobWithoutScopeKey(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ReconciliationJob{}); err != nil {
		t.Fatalf("automigrate reconciliation jobs: %v", err)
	}
	ctx := context.Background()
	now := time.Now()
	legacy := models.ReconciliationJob{
		ID:        uuid.New(),
		ChainID:   constants.Ethereum,
		FromBlock: 10,
		ToBlock:   12,
		Reason:    "legacy-reason",
		Status:    models.ReconciliationStatusOpen,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.WithContext(ctx).Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy job: %v", err)
	}

	found, created, err := NewReconciliationRepo(db).CreateOpenIfMissing(ctx, constants.Ethereum, 10, 12, "legacy-reason")
	if err != nil {
		t.Fatalf("dedupe legacy job: %v", err)
	}
	if created || found.ID != legacy.ID {
		t.Fatalf("legacy active job should dedupe created=%v found=%s legacy=%s", created, found.ID, legacy.ID)
	}
}

func TestReconciliationRepoEvidenceOutcomeRetryAndClaimLifecycle(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ReconciliationJob{}); err != nil {
		t.Fatalf("automigrate reconciliation jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewReconciliationRepo(db)
	job, created, err := repo.CreateScopedOpenIfMissing(ctx, ReconciliationScope{
		ChainID:      constants.TRON,
		Reason:       "webhook_drift:delivery",
		ScopeKey:     "webhook:delivery:" + uuid.NewString(),
		ResourceType: "webhook_delivery",
		ResourceID:   uuid.NewString(),
		Evidence: map[string]any{
			"status": "dead_letter",
		},
	})
	if err != nil {
		t.Fatalf("create scoped job: %v", err)
	}
	if !created {
		t.Fatal("first scoped job should be created")
	}

	if err := repo.RecordEvidence(ctx, job.ID, map[string]any{
		"attempts":       4,
		"last_error":     "timeout",
		"webhook_secret": "plain-secret",
		"nested": map[string]any{
			"raw_signature": "sig",
			"signature":     "sig2",
		},
	}); err != nil {
		t.Fatalf("record evidence: %v", err)
	}
	var recorded models.ReconciliationJob
	if err := db.WithContext(ctx).First(&recorded, "id = ?", job.ID).Error; err != nil {
		t.Fatalf("load recorded evidence: %v", err)
	}
	var evidence map[string]any
	if err := json.Unmarshal([]byte(recorded.EvidenceJSON), &evidence); err != nil {
		t.Fatalf("parse evidence json: %v", err)
	}
	if evidence["attempts"] != float64(4) || evidence["last_error"] != "timeout" {
		t.Fatalf("evidence json = %s", recorded.EvidenceJSON)
	}
	nested, _ := evidence["nested"].(map[string]any)
	if evidence["webhook_secret"] != "[redacted]" || nested["raw_signature"] != "[redacted]" || nested["signature"] != "[redacted]" {
		t.Fatalf("evidence sensitive values were not redacted: %s", recorded.EvidenceJSON)
	}
	if strings.Contains(recorded.EvidenceJSON, "plain-secret") || strings.Contains(recorded.EvidenceJSON, "sig2") {
		t.Fatalf("evidence leaked sensitive values: %s", recorded.EvidenceJSON)
	}

	nextRunAt := time.Now().Add(-time.Minute)
	if err := repo.MarkRetryScheduled(ctx, job.ID, nextRunAt, map[string]any{"retry": "rpc"}, "retry_rpc"); err != nil {
		t.Fatalf("mark retry scheduled: %v", err)
	}
	counts, err := repo.CountByStatus(ctx, models.ReconciliationStatusRetryScheduled)
	if err != nil {
		t.Fatalf("count retry scheduled: %v", err)
	}
	if counts[models.ReconciliationStatusRetryScheduled] != 1 {
		t.Fatalf("retry scheduled count = %d, want 1", counts[models.ReconciliationStatusRetryScheduled])
	}

	claimed, err := repo.ClaimOpen(ctx, 10)
	if err != nil {
		t.Fatalf("claim retry scheduled: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != job.ID {
		t.Fatalf("claimed jobs = %#v", claimed)
	}
	var processing models.ReconciliationJob
	if err := db.WithContext(ctx).First(&processing, "id = ?", job.ID).Error; err != nil {
		t.Fatalf("load processing job: %v", err)
	}
	if processing.Status != models.ReconciliationStatusProcessing || processing.Attempts != 1 || processing.NextRunAt != nil {
		t.Fatalf("processing job = %#v", processing)
	}
}

func TestReconciliationRepoRejectsOversizedEvidence(t *testing.T) {
	_, err := prepareReconciliationScope(ReconciliationScope{
		ChainID:  constants.Ethereum,
		Reason:   "oversized_evidence",
		ScopeKey: "oversized:" + uuid.NewString(),
		Evidence: map[string]string{
			"payload": strings.Repeat("x", reconciliationJSONMaxBytes+1),
		},
	})
	if err == nil {
		t.Fatal("oversized evidence should fail")
	}
	if !strings.Contains(err.Error(), "evidence") || !strings.Contains(err.Error(), "payload exceeds") {
		t.Fatalf("oversized evidence error = %v", err)
	}
}

func TestReconciliationRepoOpensWebhookDriftAndStuckLifecycleScopes(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ReconciliationJob{}); err != nil {
		t.Fatalf("automigrate reconciliation jobs: %v", err)
	}
	ctx := context.Background()
	repo := NewReconciliationRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	delivery := models.WebhookDelivery{
		ID:              uuid.New(),
		MerchantID:      merchantID,
		DomainID:        domainID,
		EventID:         "payment-1:payment.failed.v1",
		EventType:       "payment.failed.v1",
		EntityType:      "payment",
		Status:          models.WebhookDeliveryStatusDeadLetter,
		Attempts:        8,
		FailureCategory: "permanent",
		OperatorAction:  "replay_or_investigate",
	}
	webhookJob, created, err := repo.OpenWebhookDeliveryDrift(ctx, delivery, "")
	if err != nil {
		t.Fatalf("open webhook drift: %v", err)
	}
	if !created || webhookJob.ResourceType != "webhook_delivery" || webhookJob.ResourceID != delivery.ID.String() {
		t.Fatalf("webhook drift job = %#v created=%v", webhookJob, created)
	}
	if webhookJob.MerchantID == nil || *webhookJob.MerchantID != merchantID || webhookJob.DomainID == nil || *webhookJob.DomainID != domainID {
		t.Fatalf("webhook tenant scope = %#v", webhookJob)
	}
	again, created, err := repo.OpenWebhookDeliveryDrift(ctx, delivery, "")
	if err != nil {
		t.Fatalf("dedupe webhook drift: %v", err)
	}
	if created || again.ID != webhookJob.ID {
		t.Fatalf("webhook drift should dedupe created=%v again=%s first=%s", created, again.ID, webhookJob.ID)
	}

	stuckJob, created, err := repo.OpenStuckLifecycleJob(ctx, constants.Ethereum, &merchantID, &domainID, "payment", "payment-1", "confirming", "", map[string]any{
		"webhook_secret": "should-not-persist",
		"age_minutes":    90,
	})
	if err != nil {
		t.Fatalf("open stuck lifecycle: %v", err)
	}
	if !created || stuckJob.ResourceType != "payment" || stuckJob.ResourceID != "payment-1" {
		t.Fatalf("stuck lifecycle job = %#v created=%v", stuckJob, created)
	}
	if !strings.Contains(stuckJob.EvidenceJSON, "lifecycle_status") || !strings.Contains(stuckJob.EvidenceJSON, "[redacted]") || strings.Contains(stuckJob.EvidenceJSON, "should-not-persist") {
		t.Fatalf("stuck lifecycle evidence should include status and redact secrets: %s", stuckJob.EvidenceJSON)
	}

	if _, _, err := repo.OpenWebhookDeliveryDrift(ctx, models.WebhookDelivery{}, ""); err == nil {
		t.Fatal("empty webhook delivery drift scope should fail")
	}
	if _, _, err := repo.OpenStuckLifecycleJob(ctx, constants.Ethereum, &merchantID, &domainID, "", "", "stuck", "", nil); err == nil {
		t.Fatal("empty stuck lifecycle resource scope should fail")
	}
}
