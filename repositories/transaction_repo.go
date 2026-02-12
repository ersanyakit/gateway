package repositories

import "gorm.io/gorm"

type TransactionRepo struct {
	db *gorm.DB
}

func (r *TransactionRepo) DB() *gorm.DB {
	return r.db
}

func NewTransactionRepo(db *gorm.DB) *TransactionRepo {
	return &TransactionRepo{db: db}
}

func (r *TransactionRepo) Create() error {
	return nil
}
