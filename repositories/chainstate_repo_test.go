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
