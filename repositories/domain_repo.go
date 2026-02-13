package repositories

import (
	"core/models"
	"core/types"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type DomainRepo struct {
	merchantRepo *MerchantRepo
}

func (r *DomainRepo) DB() *gorm.DB {
	return r.merchantRepo.DB()
}

func NewDomainRepo(merchantRepo *MerchantRepo) *DomainRepo {
	return &DomainRepo{merchantRepo: merchantRepo}
}

func (r *DomainRepo) Create(params types.DomainParams) (*models.Domain, error) {

	merchantID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	h := sha256.Sum256([]byte(merchantID.String()))
	merchantIndex := binary.BigEndian.Uint32(h[:4])

	fmt.Println("MERCHANTID", merchantIndex)

	return nil, nil
}
