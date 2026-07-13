package repositories

import (
	"context"
	"testing"
	"time"

	"core/constants"
	"core/models"
)

func TestProviderHealthRepoUpsertLatestReplacesProviderSnapshot(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ProviderHealthSnapshot{}); err != nil {
		t.Fatalf("automigrate provider health: %v", err)
	}

	ctx := context.Background()
	repo := NewProviderHealthRepo(db)
	first := models.ProviderHealthSnapshot{
		ChainID:           constants.Ethereum,
		ChainName:         "ethereum",
		ProviderLabel:     "rpc.example.com",
		ProviderURLHash:   "hash-1",
		Reachable:         true,
		Status:            models.ProviderHealthStatusHealthy,
		LatestHeight:      100,
		ResponseLatencyMS: 25,
		CheckedAt:         time.Now().UTC(),
	}
	if err := repo.UpsertLatest(ctx, []models.ProviderHealthSnapshot{first}); err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	first.Status = models.ProviderHealthStatusDegraded
	first.LatestHeight = 90
	first.LagFromReference = 10
	first.ErrorCategory = "stale_head"
	if err := repo.UpsertLatest(ctx, []models.ProviderHealthSnapshot{first}); err != nil {
		t.Fatalf("upsert second: %v", err)
	}

	rows, err := repo.ListByChain(ctx, constants.Ethereum)
	if err != nil {
		t.Fatalf("list by chain: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Status != models.ProviderHealthStatusDegraded || rows[0].LagFromReference != 10 {
		t.Fatalf("row status=%q lag=%d, want degraded lag 10", rows[0].Status, rows[0].LagFromReference)
	}

	first.Reachable = false
	first.Status = models.ProviderHealthStatusUnhealthy
	first.ConsecutiveFailures = 1
	if err := repo.UpsertLatest(ctx, []models.ProviderHealthSnapshot{first}); err != nil {
		t.Fatalf("upsert first failure: %v", err)
	}
	if err := repo.UpsertLatest(ctx, []models.ProviderHealthSnapshot{first}); err != nil {
		t.Fatalf("upsert second failure: %v", err)
	}
	rows, err = repo.ListByChain(ctx, constants.Ethereum)
	if err != nil {
		t.Fatalf("list by chain after failure: %v", err)
	}
	if rows[0].ConsecutiveFailures != 2 {
		t.Fatalf("consecutive failures = %d, want 2", rows[0].ConsecutiveFailures)
	}
}

func TestProviderHealthRepoShortHashFallbackLabelAndInitialFailure(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ProviderHealthSnapshot{}); err != nil {
		t.Fatalf("automigrate provider health: %v", err)
	}

	repo := NewProviderHealthRepo(db)
	if err := repo.UpsertLatest(context.Background(), []models.ProviderHealthSnapshot{{
		ChainID:         constants.Bitcoin,
		ProviderURLHash: "short",
		Reachable:       false,
		Status:          models.ProviderHealthStatusUnhealthy,
	}}); err != nil {
		t.Fatalf("upsert short hash: %v", err)
	}

	rows, err := repo.ListByChain(context.Background(), constants.Bitcoin)
	if err != nil {
		t.Fatalf("list by chain: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].ProviderLabel != "short" {
		t.Fatalf("provider label = %q, want short", rows[0].ProviderLabel)
	}
	if rows[0].ConsecutiveFailures != 1 {
		t.Fatalf("initial consecutive failures = %d, want 1", rows[0].ConsecutiveFailures)
	}
}

func TestProviderHealthRepoCountsReachableUnhealthySnapshots(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ProviderHealthSnapshot{}); err != nil {
		t.Fatalf("automigrate provider health: %v", err)
	}

	ctx := context.Background()
	repo := NewProviderHealthRepo(db)
	snapshot := models.ProviderHealthSnapshot{
		ChainID:         constants.Ethereum,
		ChainName:       "ethereum",
		ProviderLabel:   "rpc.example.com",
		ProviderURLHash: "hash-reachable-unhealthy",
		Reachable:       true,
		Status:          models.ProviderHealthStatusUnhealthy,
		ErrorCategory:   "inconsistent_head",
		LatestHeight:    100,
		CheckedAt:       time.Now().UTC(),
	}
	if err := repo.UpsertLatest(ctx, []models.ProviderHealthSnapshot{snapshot}); err != nil {
		t.Fatalf("upsert first unhealthy snapshot: %v", err)
	}
	if err := repo.UpsertLatest(ctx, []models.ProviderHealthSnapshot{snapshot}); err != nil {
		t.Fatalf("upsert second unhealthy snapshot: %v", err)
	}

	rows, err := repo.ListByChain(ctx, constants.Ethereum)
	if err != nil {
		t.Fatalf("list by chain: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].ConsecutiveFailures != 2 {
		t.Fatalf("consecutive failures = %d, want 2", rows[0].ConsecutiveFailures)
	}

	snapshot.Status = models.ProviderHealthStatusHealthy
	snapshot.ErrorCategory = ""
	if err := repo.UpsertLatest(ctx, []models.ProviderHealthSnapshot{snapshot}); err != nil {
		t.Fatalf("upsert healthy snapshot: %v", err)
	}
	rows, err = repo.ListByChain(ctx, constants.Ethereum)
	if err != nil {
		t.Fatalf("list by chain after recovery: %v", err)
	}
	if rows[0].ConsecutiveFailures != 0 {
		t.Fatalf("consecutive failures after recovery = %d, want 0", rows[0].ConsecutiveFailures)
	}
}
