package repositories

import (
	"context"
	"errors"
	"strings"
	"testing"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestNetworkOperationalStateRepoDefaultsMissingChainsToActive(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.NetworkOperationalState{}); err != nil {
		t.Fatalf("automigrate network operational states: %v", err)
	}

	repo := NewNetworkOperationalStateRepo(db)
	state, err := repo.GetByChain(context.Background(), constants.Ethereum)
	if err != nil {
		t.Fatalf("get missing state: %v", err)
	}
	if state.Mode != models.NetworkOperationalModeActive || state.ID != uuid.Nil {
		t.Fatalf("missing state = %#v, want ephemeral active state", state)
	}

	states, err := repo.ListAll(context.Background())
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	chainIDs := constants.AllChainIDs()
	if len(states) != len(chainIDs) {
		t.Fatalf("states = %d, want %d", len(states), len(chainIDs))
	}
	for i, chainID := range chainIDs {
		if states[i].ChainID != chainID || states[i].Mode != models.NetworkOperationalModeActive {
			t.Fatalf("states[%d] = %#v, want chain %d active", i, states[i], chainID)
		}
	}
}

func TestNetworkOperationalStateRepoUpsertNormalizesAndUpdatesSingleRow(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.NetworkOperationalState{}); err != nil {
		t.Fatalf("automigrate network operational states: %v", err)
	}

	ctx := context.Background()
	repo := NewNetworkOperationalStateRepo(db)
	created, err := repo.Upsert(ctx, NetworkOperationalStateUpdate{
		ChainID:   constants.Base,
		Mode:      " DEPOSITS_OFF ",
		Reason:    "  scheduled upgrade  ",
		UpdatedBy: "  admin@example.com  ",
	})
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	if created.Mode != models.NetworkOperationalModeDepositsOff || !created.BlocksDeposits() || created.BlocksWithdrawals() {
		t.Fatalf("created state behavior = %#v", created)
	}
	if created.Reason != "scheduled upgrade" || created.UpdatedBy != "admin@example.com" {
		t.Fatalf("created state was not normalized: %#v", created)
	}

	updated, err := repo.Upsert(ctx, NetworkOperationalStateUpdate{
		ChainID:   constants.Base,
		Mode:      models.NetworkOperationalModeWithdrawalsOff,
		Reason:    "outbound signer maintenance",
		UpdatedBy: "security@example.com",
	})
	if err != nil {
		t.Fatalf("update state: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("updated id = %s, want existing id %s", updated.ID, created.ID)
	}
	if updated.Mode != models.NetworkOperationalModeWithdrawalsOff || updated.BlocksDeposits() || !updated.BlocksWithdrawals() {
		t.Fatalf("updated state behavior = %#v", updated)
	}

	reopened, err := repo.Upsert(ctx, NetworkOperationalStateUpdate{
		ChainID: constants.Base,
		Mode:    models.NetworkOperationalModeActive,
		Reason:  "stale reason must not survive",
	})
	if err != nil {
		t.Fatalf("reopen state: %v", err)
	}
	if reopened.Mode != models.NetworkOperationalModeActive || reopened.Reason != "" {
		t.Fatalf("reopened state = %#v, want active with empty reason", reopened)
	}

	var count int64
	if err := db.Model(&models.NetworkOperationalState{}).Where("chain_id = ?", constants.Base).Count(&count).Error; err != nil {
		t.Fatalf("count state rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("state rows = %d, want 1", count)
	}

	states, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("list all after upsert: %v", err)
	}
	for _, state := range states {
		if state.ChainID == constants.Base && state.Mode != models.NetworkOperationalModeActive {
			t.Fatalf("listed base state = %#v", state)
		}
	}
}

func TestNetworkOperationalStateRepoRejectsInvalidInput(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.NetworkOperationalState{}); err != nil {
		t.Fatalf("automigrate network operational states: %v", err)
	}
	repo := NewNetworkOperationalStateRepo(db)

	_, err := repo.Upsert(context.Background(), NetworkOperationalStateUpdate{
		ChainID: constants.Ethereum,
		Mode:    "paused",
	})
	if !errors.Is(err, models.ErrNetworkOperationalModeInvalid) {
		t.Fatalf("invalid mode error = %v", err)
	}

	_, err = repo.Upsert(context.Background(), NetworkOperationalStateUpdate{
		ChainID: constants.Ethereum,
		Mode:    models.NetworkOperationalModeMaintenance,
		Reason:  strings.Repeat("ü", 501),
	})
	if !errors.Is(err, models.ErrNetworkOperationalReasonTooLong) {
		t.Fatalf("long reason error = %v", err)
	}

	_, err = repo.GetByChain(context.Background(), constants.ChainID(123456789))
	if !errors.Is(err, models.ErrNetworkOperationalChainUnsupported) {
		t.Fatalf("unsupported chain error = %v", err)
	}
}

func TestNetworkOperationalStateRepoRequiresDatabase(t *testing.T) {
	repo := NewNetworkOperationalStateRepo(nil)
	if _, err := repo.ListAll(context.Background()); !errors.Is(err, ErrNetworkOperationalStateRepoNotConfigured) {
		t.Fatalf("nil database error = %v", err)
	}
}
