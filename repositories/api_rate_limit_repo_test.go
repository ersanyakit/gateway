package repositories

import (
	"context"
	"testing"
	"time"

	"core/models"
)

func TestAPIRateLimitRepoAllowsUntilSharedLimit(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.APIRateLimitCounter{}); err != nil {
		t.Fatalf("automigrate api rate counters: %v", err)
	}

	repo := NewAPIRateLimitRepo(db)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		allowed, err := repo.Allow(ctx, "key-hash", 2, time.Minute)
		if err != nil {
			t.Fatalf("allow %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("allow %d = false, want true", i)
		}
	}
	allowed, err := repo.Allow(ctx, "key-hash", 2, time.Minute)
	if err != nil {
		t.Fatalf("allow over limit: %v", err)
	}
	if allowed {
		t.Fatal("third request should exceed shared limit")
	}

	if err := db.Model(&models.APIRateLimitCounter{}).
		Where("key_hash = ?", "key-hash").
		Update("reset_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("expire counter: %v", err)
	}
	allowed, err = repo.Allow(ctx, "key-hash", 2, time.Minute)
	if err != nil {
		t.Fatalf("allow after reset: %v", err)
	}
	if !allowed {
		t.Fatal("request after reset should be allowed")
	}
}
