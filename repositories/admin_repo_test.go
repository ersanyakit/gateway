package repositories

import (
	"context"
	"testing"

	"core/models"
)

func TestEnsureBootstrapAdminPromotesExistingBootstrapWhenNoPrivilegedAdmin(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Admin{}); err != nil {
		t.Fatalf("automigrate admin table: %v", err)
	}

	ctx := context.Background()
	repo := NewAdminRepo(db)
	seed, err := repo.CreateWithRole(ctx, "bootstrap@example.com", "Bootstrap Operator", "secret", models.AdminRoleOperator)
	if err != nil {
		t.Fatalf("seed operator admin: %v", err)
	}

	changed, err := repo.EnsureBootstrapAdmin(ctx, " BOOTSTRAP@example.com ", "Bootstrap Owner", "secret")
	if err != nil {
		t.Fatalf("ensure bootstrap admin: %v", err)
	}
	if changed == nil {
		t.Fatal("expected existing bootstrap admin to be promoted")
	}
	if changed.ID != seed.ID {
		t.Fatalf("promoted admin id = %s, want %s", changed.ID, seed.ID)
	}
	if changed.Role != models.AdminRoleOwner {
		t.Fatalf("returned role = %q, want owner", changed.Role)
	}

	found, err := repo.FindAnyByEmail(ctx, "bootstrap@example.com")
	if err != nil {
		t.Fatalf("find promoted admin: %v", err)
	}
	if found.Role != models.AdminRoleOwner {
		t.Fatalf("stored role = %q, want owner", found.Role)
	}
}

func TestEnsureBootstrapAdminDoesNotPromoteWhenPrivilegedAdminExists(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Admin{}); err != nil {
		t.Fatalf("automigrate admin table: %v", err)
	}

	ctx := context.Background()
	repo := NewAdminRepo(db)
	if _, err := repo.CreateWithRole(ctx, "security@example.com", "Security Admin", "secret", models.AdminRoleSecurity); err != nil {
		t.Fatalf("seed security admin: %v", err)
	}
	seed, err := repo.CreateWithRole(ctx, "bootstrap@example.com", "Bootstrap Operator", "secret", models.AdminRoleOperator)
	if err != nil {
		t.Fatalf("seed operator admin: %v", err)
	}

	changed, err := repo.EnsureBootstrapAdmin(ctx, "bootstrap@example.com", "Bootstrap Owner", "secret")
	if err != nil {
		t.Fatalf("ensure bootstrap admin: %v", err)
	}
	if changed != nil {
		t.Fatalf("expected no bootstrap promotion, got %#v", changed)
	}

	found, err := repo.FindAnyByEmail(ctx, seed.Email)
	if err != nil {
		t.Fatalf("find unchanged admin: %v", err)
	}
	if found.Role != models.AdminRoleOperator {
		t.Fatalf("stored role = %q, want operator", found.Role)
	}
}

func TestAdminRepoSetRoleAndCountActivePrivileged(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Admin{}); err != nil {
		t.Fatalf("automigrate admin table: %v", err)
	}

	ctx := context.Background()
	repo := NewAdminRepo(db)
	owner, err := repo.CreateWithRole(ctx, "owner@example.com", "Owner", "secret", models.AdminRoleOwner)
	if err != nil {
		t.Fatalf("seed owner admin: %v", err)
	}
	if _, err := repo.CreateWithRole(ctx, "operator@example.com", "Operator", "secret", models.AdminRoleOperator); err != nil {
		t.Fatalf("seed operator admin: %v", err)
	}

	count, err := repo.CountActivePrivileged(ctx)
	if err != nil {
		t.Fatalf("count privileged: %v", err)
	}
	if count != 1 {
		t.Fatalf("privileged count = %d, want 1", count)
	}

	if err := repo.SetRole(ctx, owner.ID, models.AdminRoleSecurity); err != nil {
		t.Fatalf("set role: %v", err)
	}
	found, err := repo.FindAnyByEmail(ctx, owner.Email)
	if err != nil {
		t.Fatalf("find updated admin: %v", err)
	}
	if found.Role != models.AdminRoleSecurity {
		t.Fatalf("stored role = %q, want security", found.Role)
	}
	count, err = repo.CountActivePrivileged(ctx)
	if err != nil {
		t.Fatalf("count privileged after update: %v", err)
	}
	if count != 1 {
		t.Fatalf("privileged count after update = %d, want 1", count)
	}
}
