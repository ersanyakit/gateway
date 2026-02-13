package repositories

import (
	"core/types"
	"fmt"
	"testing"
)

func Test_Merchant(t *testing.T) {

	merchantName := "ersan"
	merchantEmail := "ersanyakit@gmail.com"

	merchantCreateParams := types.MerchantParams{
		Name:  &merchantName,
		Email: &merchantEmail,
	}

	merchantRepo := NewMerchantRepo(nil, nil)
	merchant, err := merchantRepo.Create(merchantCreateParams)
	if err != nil {
	}

	fmt.Println("MerchantID", merchant.ID)
	//merchantRepo.CreateDomain()

}
