package repositories

import (
	"context"
	"core/constants"
	"core/models"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrNetworkOperationalStateRepoNotConfigured = errors.New("network operational state repository is not configured")

type NetworkOperationalStateRepo struct {
	db *gorm.DB
}

type NetworkOperationalStateUpdate struct {
	ChainID   constants.ChainID
	Mode      models.NetworkOperationalMode
	Reason    string
	UpdatedBy string
}

func NewNetworkOperationalStateRepo(db *gorm.DB) *NetworkOperationalStateRepo {
	return &NetworkOperationalStateRepo{db: db}
}

func (r *NetworkOperationalStateRepo) DB() *gorm.DB {
	if r == nil {
		return nil
	}
	return r.db
}

func (r *NetworkOperationalStateRepo) GetByChain(ctx context.Context, chainID constants.ChainID) (*models.NetworkOperationalState, error) {
	if r == nil || r.db == nil {
		return nil, ErrNetworkOperationalStateRepoNotConfigured
	}
	if !constants.IsSupportedChainID(chainID) {
		return nil, fmt.Errorf("%w: %d", models.ErrNetworkOperationalChainUnsupported, chainID)
	}

	var state models.NetworkOperationalState
	err := r.db.WithContext(ctx).Where("chain_id = ?", chainID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaultNetworkOperationalState(chainID), nil
	}
	if err != nil {
		return nil, err
	}
	state.Normalize()
	if err := state.Validate(); err != nil {
		return nil, err
	}
	return &state, nil
}

func (r *NetworkOperationalStateRepo) ListAll(ctx context.Context) ([]models.NetworkOperationalState, error) {
	if r == nil || r.db == nil {
		return nil, ErrNetworkOperationalStateRepoNotConfigured
	}

	var persisted []models.NetworkOperationalState
	if err := r.db.WithContext(ctx).Find(&persisted).Error; err != nil {
		return nil, err
	}
	byChain := make(map[constants.ChainID]models.NetworkOperationalState, len(persisted))
	for i := range persisted {
		persisted[i].Normalize()
		if err := persisted[i].Validate(); err != nil {
			return nil, err
		}
		byChain[persisted[i].ChainID] = persisted[i]
	}

	chainIDs := constants.AllChainIDs()
	states := make([]models.NetworkOperationalState, 0, len(chainIDs))
	for _, chainID := range chainIDs {
		if state, ok := byChain[chainID]; ok {
			states = append(states, state)
			continue
		}
		states = append(states, *defaultNetworkOperationalState(chainID))
	}
	return states, nil
}

func (r *NetworkOperationalStateRepo) Upsert(ctx context.Context, update NetworkOperationalStateUpdate) (*models.NetworkOperationalState, error) {
	if r == nil || r.db == nil {
		return nil, ErrNetworkOperationalStateRepoNotConfigured
	}

	state := models.NetworkOperationalState{
		ID:        uuid.New(),
		ChainID:   update.ChainID,
		Mode:      models.NormalizeNetworkOperationalMode(update.Mode),
		Reason:    strings.TrimSpace(update.Reason),
		UpdatedBy: strings.TrimSpace(update.UpdatedBy),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	state.Normalize()
	if err := state.Validate(); err != nil {
		return nil, err
	}

	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "chain_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"mode":       state.Mode,
			"reason":     state.Reason,
			"updated_by": state.UpdatedBy,
			"updated_at": state.UpdatedAt,
		}),
	}).Create(&state).Error
	if err != nil {
		return nil, err
	}

	return r.GetByChain(ctx, update.ChainID)
}

func defaultNetworkOperationalState(chainID constants.ChainID) *models.NetworkOperationalState {
	return &models.NetworkOperationalState{
		ChainID: chainID,
		Mode:    models.NetworkOperationalModeActive,
	}
}
