package repositories

import (
	"core/models"
	"core/types"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TransactionRepo struct {
	db *gorm.DB
}

func (r *TransactionRepo) DB() *gorm.DB {
	return r.db
}

func NewTransactionRepo(db *gorm.DB) *TransactionRepo {
	return &TransactionRepo{db: db}
}

func (r *TransactionRepo) Create(params types.TransactionParam) error {
	if params.Hash == nil {
		return errors.New("hash is required")
	}
	if params.Block == nil {
		return errors.New("block number is required")
	}
	if params.From == nil || params.To == nil {
		return errors.New("from/to required")
	}

	return r.DB().Transaction(func(tx *gorm.DB) error {
		logIndexStr := ""
		if params.LogIndex != nil {
			logIndexStr = *params.LogIndex
		}

		uniqueHash := fmt.Sprintf("%d-%s-%s-%s",
			params.ChainID,
			*params.Hash,
			logIndexStr,
			*params.Block,
		)

		txModel := &models.Transaction{
			ID:          uuid.New(),
			ChainID:     params.ChainID,
			Hash:        *params.Hash,
			LogIndex:    params.LogIndex,
			BlockNumber: *params.Block,
			Symbol:      *params.Symbol,
			BlockHash:   "",
			Token:       params.Token,
			FromAddress: *params.From,
			ToAddress:   *params.To,
			Amount:      *params.Amount,
			UniqueHash:  uniqueHash,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "unique_hash"}},
			DoNothing: true,
		}).Create(txModel).Error; err != nil {
			return err
		}

		return nil
	})
}
