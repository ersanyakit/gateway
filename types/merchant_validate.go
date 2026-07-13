package types

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var merchantEmailPattern, merchantEmailPatternErr = regexp.Compile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

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
	} else if merchantEmailPatternErr != nil {
		return fmt.Errorf("compile merchant email validation pattern: %w", merchantEmailPatternErr)
	} else if !merchantEmailPattern.MatchString(*p.Email) {
		errs.Add("email", "invalid email format")
	}

	// Email Repeat
	if p.Email == nil || p.EmailRepeat == nil || *p.Email != *p.EmailRepeat {
		errs.Add("email_repeat", "emails do not match")
	}

	// Password
	if p.Password == nil || len(*p.Password) < 6 {
		errs.Add("password", "password must be at least 6 characters")
	}

	// Password Repeat
	if p.Password == nil || p.PasswordRepeat == nil || *p.Password != *p.PasswordRepeat {
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
	form := url.Values{"secret": {secret}, "response": {response}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.google.com/recaptcha/api/siteverify", strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return false, fmt.Errorf("captcha verification returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
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
