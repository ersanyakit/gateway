package repositories

import (
	"context"
	"core/constants"
	"core/models"
	"errors"
	"time"

	"gorm.io/gorm"
)

type ChainStateRepo struct {
	db *gorm.DB
}

func NewChainStateRepo(db *gorm.DB) *ChainStateRepo {
	return &ChainStateRepo{db: db}
}

func (r *ChainStateRepo) Get(ctx context.Context, chainID constants.ChainID) (*models.ChainState, error) {
	var state models.ChainState
	if err := r.db.WithContext(ctx).First(&state, "chain_id = ?", chainID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			state = models.ChainState{
				ChainID:            chainID,
				LastProcessedBlock: 0,
				LastConfirmedBlock: 0,
				UpdatedAt:          time.Now(),
			}
			if err := r.db.WithContext(ctx).Create(&state).Error; err != nil {
				return nil, err
			}
			return &state, nil
		}
		return nil, err
	}
	return &state, nil
}

func (r *ChainStateRepo) Update(ctx context.Context, state *models.ChainState) error {
	state.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Save(state).Error
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
