package repositories

import "gorm.io/gorm"

type WalletRepo struct {
	db *gorm.DB
}

func (r *WalletRepo) DB() *gorm.DB {
	return r.db
}

func NewWalletRepo(db *gorm.DB) *WalletRepo {
	return &WalletRepo{db: db}
}

func (r *WalletRepo) Create() error {
	return nil
}
