package types

import (
	"context"
)

type MerchantParams struct {
	Context        context.Context `json:"-"`
	Name           *string         `json:"name,omitempty"`
	Email          *string         `json:"email,omitempty"`
	EmailRepeat    *string         `json:"email_repeat,omitempty"`
	Password       *string         `json:"password,omitempty"`
	PasswordRepeat *string         `json:"password_repeat,omitempty"`
	Captcha        *string         `json:"captcha,omitempty"`

	Country   *string  `json:"country,omitempty"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
	Cursor    *int64   `json:"cursor,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}
