package types

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type MerchantParams struct {
	Context        context.Context `json:"-"`
	ID             *uuid.UUID      `json:"id,omitempty"`
	Name           *string         `json:"name,omitempty"`
	Email          *string         `json:"email,omitempty"`
	EmailRepeat    *string         `json:"email_repeat,omitempty"`
	Password       *string         `json:"password,omitempty"`
	PasswordRepeat *string         `json:"password_repeat,omitempty"`
	Captcha        *string         `json:"captcha,omitempty"`

	Cursor *uuid.UUID `json:"cursor,omitempty"`
	Limit  int        `json:"limit,omitempty"`
}

func (p *MerchantParams) ValidateEmail() error {
	if p.Context == nil {
		return errors.New("context is required")
	}

	if p.Email == nil || *p.Email == "" {
		return errors.New("email is required")
	}

	return nil
}

func (p *MerchantParams) ValidateID() error {
	if p.Context == nil {
		return errors.New("context is required")
	}

	if p.ID == nil {
		return errors.New("id is required")
	}

	if *p.ID == uuid.Nil {
		return errors.New("invalid id")
	}

	return nil
}
