package repositories

import (
	"context"
	"testing"
	"time"

	"core/models"
)

func TestWorkerLeaseAcquireBlocksUntilExpiredThenReleases(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WorkerLease{}); err != nil {
		t.Fatalf("automigrate worker leases: %v", err)
	}
	repo := NewWorkerLeaseRepo(db)
	ctx := context.Background()

	first, acquired, err := repo.TryAcquire(ctx, WorkerLeaseRequest{
		LeaseKey: "worker:test",
		Purpose:  "test_worker",
		OwnerID:  "owner-a",
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if !acquired {
		t.Fatal("first owner should acquire lease")
	}

	second, acquired, err := repo.TryAcquire(ctx, WorkerLeaseRequest{
		LeaseKey: "worker:test",
		Purpose:  "test_worker",
		OwnerID:  "owner-b",
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("second acquire before expiry: %v", err)
	}
	if acquired {
		t.Fatal("second owner acquired active lease before expiry")
	}
	if second == nil || second.OwnerID != "owner-a" {
		t.Fatalf("busy lease owner = %#v, want owner-a", second)
	}

	if err := db.WithContext(ctx).Model(&models.WorkerLease{}).
		Where("id = ?", first.ID).
		Update("lease_until", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	renewed, acquired, err := repo.TryAcquire(ctx, WorkerLeaseRequest{
		LeaseKey: "worker:test",
		Purpose:  "test_worker",
		OwnerID:  "owner-b",
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("second acquire after expiry: %v", err)
	}
	if !acquired || renewed == nil || renewed.OwnerID != "owner-b" {
		t.Fatalf("expired lease acquire = lease=%#v acquired=%v, want owner-b", renewed, acquired)
	}
	if renewed.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", renewed.Attempts)
	}

	if err := repo.Release(ctx, renewed.ID, "owner-b"); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	counts, err := repo.CountByStatus(ctx, models.WorkerLeaseStatusReleased)
	if err != nil {
		t.Fatalf("count released: %v", err)
	}
	if counts[models.WorkerLeaseStatusReleased] != 1 {
		t.Fatalf("released leases = %d, want 1", counts[models.WorkerLeaseStatusReleased])
	}
}

func TestWorkerLeaseEnsureRowsSeedsReleasedLeaseForAcquire(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WorkerLease{}); err != nil {
		t.Fatalf("automigrate worker leases: %v", err)
	}
	repo := NewWorkerLeaseRepo(db)
	ctx := context.Background()

	if err := repo.EnsureRows(ctx, []WorkerLeaseRequest{{
		LeaseKey: "worker:seeded",
		Purpose:  "seeded_worker",
		OwnerID:  "seed-owner",
	}}); err != nil {
		t.Fatalf("ensure rows: %v", err)
	}

	var seeded models.WorkerLease
	if err := db.WithContext(ctx).First(&seeded, "lease_key = ?", "worker:seeded").Error; err != nil {
		t.Fatalf("find seeded lease: %v", err)
	}
	if seeded.Status != models.WorkerLeaseStatusReleased || seeded.Attempts != 0 {
		t.Fatalf("seeded lease status/attempts = %s/%d, want released/0", seeded.Status, seeded.Attempts)
	}

	lease, acquired, err := repo.TryAcquire(ctx, WorkerLeaseRequest{
		LeaseKey: "worker:seeded",
		Purpose:  "seeded_worker",
		OwnerID:  "owner-a",
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("acquire seeded lease: %v", err)
	}
	if !acquired || lease == nil {
		t.Fatalf("acquire seeded lease = lease=%#v acquired=%v, want acquired", lease, acquired)
	}
	if lease.OwnerID != "owner-a" || lease.Status != models.WorkerLeaseStatusActive || lease.Attempts != 1 {
		t.Fatalf("lease after acquire = owner=%s status=%s attempts=%d, want owner-a active 1", lease.OwnerID, lease.Status, lease.Attempts)
	}

	if err := repo.EnsureRows(ctx, []WorkerLeaseRequest{{
		LeaseKey: "worker:seeded",
		Purpose:  "seeded_worker",
		OwnerID:  "seed-owner",
	}}); err != nil {
		t.Fatalf("ensure existing row: %v", err)
	}
	var count int64
	if err := db.WithContext(ctx).Model(&models.WorkerLease{}).Where("lease_key = ?", "worker:seeded").Count(&count).Error; err != nil {
		t.Fatalf("count lease rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("lease row count = %d, want 1", count)
	}
}
