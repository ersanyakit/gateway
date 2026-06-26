package types

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

func (p *MerchantParams) Validate() error {

	var errs ValidationErrors

	// Name
	if p.Name == nil || strings.TrimSpace(*p.Name) == "" {
		errs.Add("name", "name is required")
	} else if len(*p.Name) < 3 {
		errs.Add("name", "name must be at least 3 characters")
	}

	// Email
	if p.Email == nil || strings.TrimSpace(*p.Email) == "" {
		errs.Add("email", "email is required")
	} else {
		emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
		if !emailRegex.MatchString(*p.Email) {
			errs.Add("email", "invalid email format")
		}
	}

	// Email Repeat
	if p.EmailRepeat == nil || *p.Email != *p.EmailRepeat {
		errs.Add("email_repeat", "emails do not match")
	}

	// Password
	if p.Password == nil || len(*p.Password) < 6 {
		errs.Add("password", "password must be at least 6 characters")
	}

	// Password Repeat
	if p.PasswordRepeat == nil || *p.Password != *p.PasswordRepeat {
		errs.Add("password_repeat", "passwords do not match")
	}

	if errs.HasErrors() {
		return errs
	}

	return nil
}

func (r *MerchantParams) VerifyCaptcha(secret string, response string) (bool, error) {
	type recaptchaResponse struct {
		Success bool `json:"success"`
	}
	resp, err := http.PostForm("https://www.google.com/recaptcha/api/siteverify",
		url.Values{"secret": {secret}, "response": {response}})
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var captchaResponse recaptchaResponse
	err = json.Unmarshal(body, &captchaResponse)
	if err != nil {
		return false, err
	}

	return captchaResponse.Success, nil
}
