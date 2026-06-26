package repositories

import (
	"context"
	"core/constants"
	"core/models"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

var ErrUnsupportedChainID = errors.New("unsupported chain id")

type ChainStateRepo struct {
	db *gorm.DB
}

func NewChainStateRepo(db *gorm.DB) *ChainStateRepo {
	return &ChainStateRepo{db: db}
}

func (r *ChainStateRepo) Get(ctx context.Context, chainID constants.ChainID) (*models.ChainState, error) {
	if !constants.IsSupportedChainID(chainID) {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedChainID, chainID)
	}

	var state models.ChainState
	if err := r.db.WithContext(ctx).First(&state, "chain_id = ?", chainID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			now := time.Now()
			if err := r.db.WithContext(ctx).Exec(
				`INSERT INTO chain_states (chain_id, last_processed_block, last_confirmed_block, updated_at)
				 VALUES (?, 0, 0, ?)
				 ON CONFLICT (chain_id) DO NOTHING`,
				int64(chainID),
				now,
			).Error; err != nil {
				return nil, err
			}
			if err := r.db.WithContext(ctx).First(&state, "chain_id = ?", chainID).Error; err != nil {
				return nil, err
			}
			return &state, nil
		}
		return nil, err
	}
	return &state, nil
}

func (r *ChainStateRepo) Update(ctx context.Context, state *models.ChainState) error {
	if state == nil {
		return errors.New("chain state is nil")
	}
	if !constants.IsSupportedChainID(state.ChainID) {
		return fmt.Errorf("%w: %d", ErrUnsupportedChainID, state.ChainID)
	}

	state.UpdatedAt = time.Now()
	if err := r.db.WithContext(ctx).Exec(
		`INSERT INTO chain_states (chain_id, last_processed_block, last_confirmed_block, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (chain_id) DO UPDATE
		 SET last_processed_block = EXCLUDED.last_processed_block,
		     last_confirmed_block = EXCLUDED.last_confirmed_block,
		     updated_at = EXCLUDED.updated_at`,
		int64(state.ChainID),
		state.LastProcessedBlock,
		state.LastConfirmedBlock,
		state.UpdatedAt,
	).Error; err != nil {
		return err
	}

	return nil
}

func (r *ChainStateRepo) Exists(ctx context.Context, chainID int64) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.ChainState{}).Where("chain_id = ?", chainID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *ChainStateRepo) ListAll(ctx context.Context) ([]models.ChainState, error) {
	var states []models.ChainState
	if err := r.db.WithContext(ctx).Find(&states).Error; err != nil {
		return nil, err
	}
	return states, nil
}

func (r *ChainStateRepo) DeleteUnsupported(ctx context.Context, supported []constants.ChainID) (int64, error) {
	if len(supported) == 0 {
		supported = constants.AllChainIDs()
	}

	ids := make([]int64, 0, len(supported))
	seen := make(map[constants.ChainID]struct{}, len(supported))
	for _, chainID := range supported {
		if !constants.IsSupportedChainID(chainID) {
			return 0, fmt.Errorf("%w: %d", ErrUnsupportedChainID, chainID)
		}
		if _, ok := seen[chainID]; ok {
			continue
		}
		seen[chainID] = struct{}{}
		ids = append(ids, int64(chainID))
	}

	result := r.db.WithContext(ctx).
		Where("chain_id NOT IN ?", ids).
		Delete(&models.ChainState{})
	return result.RowsAffected, result.Error
}
