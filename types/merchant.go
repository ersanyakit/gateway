package types

import (
	"context"
)

type MerchantParams struct {
	Context        context.Context
	Name           *string
	Email          *string
	EmailRepeat    *string
	Password       *string
	PasswordRepeat *string
	Captcha        *string

	Country   *string
	Latitude  *float64
	Longitude *float64
	Cursor    *int64
	Limit     int
}
