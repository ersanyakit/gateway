package repositories

import (
	"context"
	"errors"
	"testing"

	"core/constants"
	"core/models"
)

func TestChainStateUpdateRejectsNil(t *testing.T) {
	repo := NewChainStateRepo(nil)
	if err := repo.Update(context.Background(), nil); err == nil {
		t.Fatal("nil chain state should fail")
	}
}

func TestChainStateUpdateRejectsUnsupportedChain(t *testing.T) {
	repo := NewChainStateRepo(nil)
	err := repo.Update(context.Background(), &models.ChainState{ChainID: constants.ChainID(554576)})
	if !errors.Is(err, ErrUnsupportedChainID) {
		t.Fatalf("error = %v, want ErrUnsupportedChainID", err)
	}
}

func TestChainStateUpdatePersistsCheckpointEvidence(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainState{}); err != nil {
		t.Fatalf("automigrate chain state: %v", err)
	}

	ctx := context.Background()
	repo := NewChainStateRepo(db)
	if err := repo.Update(ctx, &models.ChainState{
		ChainID:                 constants.Ethereum,
		LastProcessedBlock:      10,
		LastProcessedHash:       "0x10",
		LastProcessedParentHash: "0x09",
		LastConfirmedBlock:      20,
		ScannerStartBlock:       1,
		ScannerStartPolicy:      "require",
		ContinuityStatus:        "ok",
	}); err != nil {
		t.Fatalf("update first checkpoint: %v", err)
	}
	if err := repo.Update(ctx, &models.ChainState{
		ChainID:                 constants.Ethereum,
		LastProcessedBlock:      9,
		LastProcessedHash:       "0xold",
		LastProcessedParentHash: "0xolder",
		LastConfirmedBlock:      21,
		ScannerStartBlock:       99,
		ScannerStartPolicy:      "tail",
		ContinuityStatus:        "ok",
	}); err != nil {
		t.Fatalf("update stale checkpoint: %v", err)
	}

	state, err := repo.Get(ctx, constants.Ethereum)
	if err != nil {
		t.Fatalf("get chain state: %v", err)
	}
	if state.LastProcessedBlock != 10 || state.LastProcessedHash != "0x10" || state.LastProcessedParentHash != "0x09" {
		t.Fatalf("checkpoint = %+v, want latest block/hash preserved against stale update", state)
	}
	if state.LastConfirmedBlock != 21 {
		t.Fatalf("last confirmed block = %d, want max 21", state.LastConfirmedBlock)
	}
	if state.ScannerStartBlock != 1 || state.ScannerStartPolicy != "require" {
		t.Fatalf("scanner start = %d/%q, want original explicit start", state.ScannerStartBlock, state.ScannerStartPolicy)
	}
}

func TestChainStateUpdateAllowsContinuityRollbackRewind(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainState{}); err != nil {
		t.Fatalf("automigrate chain state: %v", err)
	}

	ctx := context.Background()
	repo := NewChainStateRepo(db)
	if err := repo.Update(ctx, &models.ChainState{
		ChainID:                 constants.ChilizSpicy,
		LastProcessedBlock:      35869016,
		LastProcessedHash:       "0xstale",
		LastProcessedParentHash: "0xparent",
		LastConfirmedBlock:      35869029,
		ScannerStartBlock:       35868000,
		ScannerStartPolicy:      "require",
		ContinuityStatus:        "ok",
	}); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	if err := repo.Update(ctx, &models.ChainState{
		ChainID:            constants.ChilizSpicy,
		LastProcessedBlock: 35869015,
		LastConfirmedBlock: 35869029,
		ContinuityStatus:   "rollback_required",
		ContinuityReason:   "parent mismatch",
	}); err != nil {
		t.Fatalf("rollback checkpoint: %v", err)
	}

	state, err := repo.Get(ctx, constants.ChilizSpicy)
	if err != nil {
		t.Fatalf("get chain state: %v", err)
	}
	if state.LastProcessedBlock != 35869015 {
		t.Fatalf("last processed block = %d, want rollback target", state.LastProcessedBlock)
	}
	if state.LastProcessedHash != "" || state.LastProcessedParentHash != "" {
		t.Fatalf("checkpoint hash = %q/%q, want cleared", state.LastProcessedHash, state.LastProcessedParentHash)
	}
	if state.LastConfirmedBlock != 35869029 {
		t.Fatalf("last confirmed block = %d, want preserved", state.LastConfirmedBlock)
	}
	if state.ContinuityStatus != "rollback_required" || state.ContinuityReason == "" {
		t.Fatalf("continuity evidence = %q/%q, want rollback evidence", state.ContinuityStatus, state.ContinuityReason)
	}
}
