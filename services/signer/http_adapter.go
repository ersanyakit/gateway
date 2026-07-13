package signer

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"core/constants"
)

const (
	defaultHTTPAdapterTimeout = 10 * time.Second

	defaultHTTPHealthPath          = "/v1/health"
	defaultHTTPDeriveAddressPath   = "/v1/derive-address"
	defaultHTTPSignTransactionPath = "/v1/sign-transaction"
	defaultHTTPSignMessagePath     = "/v1/sign-message"
	defaultHTTPKeyReferencePath    = "/v1/key-reference"
)

type HTTPAdapterConfig struct {
	BaseURL     string
	Mode        string
	Provider    string
	BearerToken string
	HMACSecret  string
	Timeout     time.Duration
	Client      *http.Client

	HealthPath          string
	DeriveAddressPath   string
	SignTransactionPath string
	SignMessagePath     string
	KeyReferencePath    string
}

type HTTPAdapter struct {
	baseURL     *url.URL
	mode        string
	provider    string
	bearerToken string
	hmacSecret  string
	client      *http.Client

	healthPath          string
	deriveAddressPath   string
	signTransactionPath string
	signMessagePath     string
	keyReferencePath    string
}

type httpStatusError struct {
	statusCode int
	body       string
}

func (e httpStatusError) Error() string {
	if strings.TrimSpace(e.body) == "" {
		return fmt.Sprintf("custody signer HTTP %d", e.statusCode)
	}
	return fmt.Sprintf("custody signer HTTP %d: %s", e.statusCode, strings.TrimSpace(e.body))
}

type httpSignTransactionResponse struct {
	SignedPayload       string `json:"signed_payload"`
	SignedPayloadBase64 string `json:"signed_payload_base64"`
	TxHash              string `json:"tx_hash"`
	KeyReference        string `json:"key_reference"`
	AuditID             string `json:"audit_id"`
}

type httpSignMessageResponse struct {
	Signature       string `json:"signature"`
	SignatureBase64 string `json:"signature_base64"`
	KeyReference    string `json:"key_reference"`
	AuditID         string `json:"audit_id"`
}

type httpKeyReferenceResponse struct {
	KeyReference string `json:"key_reference"`
}

func NewHTTPAdapter(cfg HTTPAdapterConfig) (*HTTPAdapter, error) {
	rawURL := strings.TrimSpace(cfg.BaseURL)
	if rawURL == "" {
		return nil, errors.New("custody signer base URL is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("custody signer base URL parse: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("custody signer base URL must use http or https")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil, errors.New("custody signer base URL host is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultHTTPAdapterTimeout
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" {
		mode = CurrentMode()
	}
	provider := strings.TrimSpace(cfg.Provider)
	if provider == "" {
		provider = "first-party-custody"
	}
	return &HTTPAdapter{
		baseURL:             parsed,
		mode:                mode,
		provider:            provider,
		bearerToken:         strings.TrimSpace(cfg.BearerToken),
		hmacSecret:          strings.TrimSpace(cfg.HMACSecret),
		client:              client,
		healthPath:          defaultString(cfg.HealthPath, defaultHTTPHealthPath),
		deriveAddressPath:   defaultString(cfg.DeriveAddressPath, defaultHTTPDeriveAddressPath),
		signTransactionPath: defaultString(cfg.SignTransactionPath, defaultHTTPSignTransactionPath),
		signMessagePath:     defaultString(cfg.SignMessagePath, defaultHTTPSignMessagePath),
		keyReferencePath:    defaultString(cfg.KeyReferencePath, defaultHTTPKeyReferencePath),
	}, nil
}

func NewConfiguredHTTPAdapterFromEnv() (*HTTPAdapter, bool, error) {
	baseURL := firstEnv("CUSTODY_SIGNER_URL", "FIRST_PARTY_SIGNER_URL", "GATEWAY_CUSTODY_SIGNER_URL")
	if strings.TrimSpace(baseURL) == "" {
		return nil, false, nil
	}
	hmacSecret := firstEnv("CUSTODY_SIGNER_HMAC_SECRET", "FIRST_PARTY_SIGNER_HMAC_SECRET", "SIGNER_HMAC_SECRET")
	if IsProduction() && strings.TrimSpace(hmacSecret) == "" {
		return nil, false, errors.New("CUSTODY_SIGNER_HMAC_SECRET is required in production")
	}
	timeout := envDuration(defaultHTTPAdapterTimeout, "CUSTODY_SIGNER_TIMEOUT", "FIRST_PARTY_SIGNER_TIMEOUT", "SIGNER_HTTP_TIMEOUT")
	adapter, err := NewHTTPAdapter(HTTPAdapterConfig{
		BaseURL:             baseURL,
		Mode:                CurrentMode(),
		Provider:            firstEnv("CUSTODY_SIGNER_PROVIDER", "FIRST_PARTY_SIGNER_PROVIDER", "SIGNER_PROVIDER"),
		BearerToken:         firstEnv("CUSTODY_SIGNER_BEARER_TOKEN", "FIRST_PARTY_SIGNER_BEARER_TOKEN", "SIGNER_BEARER_TOKEN"),
		HMACSecret:          hmacSecret,
		Timeout:             timeout,
		HealthPath:          firstEnv("CUSTODY_SIGNER_HEALTH_PATH", "SIGNER_HEALTH_PATH"),
		DeriveAddressPath:   firstEnv("CUSTODY_SIGNER_DERIVE_ADDRESS_PATH", "SIGNER_DERIVE_ADDRESS_PATH"),
		SignTransactionPath: firstEnv("CUSTODY_SIGNER_SIGN_TRANSACTION_PATH", "SIGNER_SIGN_TRANSACTION_PATH"),
		SignMessagePath:     firstEnv("CUSTODY_SIGNER_SIGN_MESSAGE_PATH", "SIGNER_SIGN_MESSAGE_PATH"),
		KeyReferencePath:    firstEnv("CUSTODY_SIGNER_KEY_REFERENCE_PATH", "SIGNER_KEY_REFERENCE_PATH"),
	})
	if err != nil {
		return nil, false, err
	}
	return adapter, true, nil
}

func RegisterConfiguredCustodyAdapterFromEnv() (bool, error) {
	if ActiveCustodyAdapter() != nil {
		return false, nil
	}
	adapter, configured, err := NewConfiguredHTTPAdapterFromEnv()
	if err != nil || !configured {
		return false, err
	}
	adapterRegistry.Lock()
	defer adapterRegistry.Unlock()
	if adapterRegistry.adapter != nil {
		return false, nil
	}
	adapterRegistry.adapter = adapter
	return true, nil
}

func ensureConfiguredCustodyAdapterFromEnv() error {
	if ActiveCustodyAdapter() != nil || !IsExternalMode(CurrentMode()) {
		return nil
	}
	_, err := RegisterConfiguredCustodyAdapterFromEnv()
	return err
}

func (a *HTTPAdapter) DeriveAddress(ctx context.Context, req DeriveAddressRequest) (DeriveAddressResponse, error) {
	var response DeriveAddressResponse
	if err := a.doJSON(ctx, http.MethodPost, a.deriveAddressPath, req, &response); err != nil {
		return DeriveAddressResponse{}, err
	}
	if strings.TrimSpace(response.SignerMode) == "" {
		response.SignerMode = a.mode
	}
	if strings.TrimSpace(response.CustodyProvider) == "" {
		response.CustodyProvider = a.provider
	}
	return response, nil
}

func (a *HTTPAdapter) SignTransaction(ctx context.Context, req SignTransactionRequest) (SignTransactionResponse, error) {
	var response httpSignTransactionResponse
	if err := a.doJSON(ctx, http.MethodPost, a.signTransactionPath, req, &response); err != nil {
		return SignTransactionResponse{}, err
	}
	payload, err := payloadStringBytes(response.SignedPayload, response.SignedPayloadBase64)
	if err != nil {
		return SignTransactionResponse{}, err
	}
	return SignTransactionResponse{
		SignedPayload: payload,
		TxHash:        strings.TrimSpace(response.TxHash),
		KeyReference:  strings.TrimSpace(response.KeyReference),
		AuditID:       strings.TrimSpace(response.AuditID),
	}, nil
}

func (a *HTTPAdapter) SignMessage(ctx context.Context, req SignMessageRequest) (SignMessageResponse, error) {
	var response httpSignMessageResponse
	if err := a.doJSON(ctx, http.MethodPost, a.signMessagePath, req, &response); err != nil {
		return SignMessageResponse{}, err
	}
	signature, err := payloadStringBytes(response.Signature, response.SignatureBase64)
	if err != nil {
		return SignMessageResponse{}, err
	}
	return SignMessageResponse{
		Signature:    signature,
		KeyReference: strings.TrimSpace(response.KeyReference),
		AuditID:      strings.TrimSpace(response.AuditID),
	}, nil
}

func (a *HTTPAdapter) KeyReference(ctx context.Context, req DeriveAddressRequest) (string, error) {
	var response httpKeyReferenceResponse
	if err := a.doJSON(ctx, http.MethodPost, a.keyReferencePath, req, &response); err != nil {
		var statusErr httpStatusError
		if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusNotFound {
			return fallbackKeyReference(req), nil
		}
		return "", err
	}
	if strings.TrimSpace(response.KeyReference) != "" {
		return strings.TrimSpace(response.KeyReference), nil
	}
	return fallbackKeyReference(req), nil
}

func (a *HTTPAdapter) Health(ctx context.Context) AdapterHealth {
	var response AdapterHealth
	if err := a.doJSON(ctx, http.MethodGet, a.healthPath, nil, &response); err != nil {
		return AdapterHealth{
			Ready:    false,
			Mode:     a.mode,
			Provider: a.provider,
			Details:  err.Error(),
		}
	}
	if strings.TrimSpace(response.Mode) == "" {
		response.Mode = a.mode
	}
	if strings.TrimSpace(response.Provider) == "" {
		response.Provider = a.provider
	}
	return response
}

func (a *HTTPAdapter) doJSON(ctx context.Context, method string, path string, payload any, out any) error {
	if a == nil || a.baseURL == nil || a.client == nil {
		return errors.New("custody signer HTTP adapter is not configured")
	}
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("custody signer request encode: %w", err)
		}
	}
	endpoint := a.endpoint(path)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("custody signer request build: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.bearerToken)
	}
	if a.hmacSecret != "" {
		timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
		req.Header.Set("X-Gateway-Signer-Timestamp", timestamp)
		req.Header.Set("X-Gateway-Signer-Signature", "sha256="+httpAdapterSignature(a.hmacSecret, timestamp, method, req.URL.RequestURI(), body))
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("custody signer request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("custody signer response read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpStatusError{statusCode: resp.StatusCode, body: string(responseBody)}
	}
	if out == nil || len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("custody signer response decode: %w", err)
	}
	return nil
}

func (a *HTTPAdapter) endpoint(path string) string {
	clone := *a.baseURL
	basePath := strings.TrimRight(clone.Path, "/")
	path = "/" + strings.TrimLeft(defaultString(path, "/"), "/")
	if basePath == "" {
		clone.Path = path
	} else {
		clone.Path = basePath + path
	}
	return clone.String()
}

func httpAdapterSignature(secret string, timestamp string, method string, requestURI string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.TrimSpace(timestamp)))
	mac.Write([]byte("\n"))
	mac.Write([]byte(strings.ToUpper(strings.TrimSpace(method))))
	mac.Write([]byte("\n"))
	mac.Write([]byte(strings.TrimSpace(requestURI)))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func payloadStringBytes(value string, explicitBase64 string) ([]byte, error) {
	if strings.TrimSpace(explicitBase64) != "" {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(explicitBase64))
		if err != nil {
			return nil, fmt.Errorf("custody signer base64 payload decode: %w", err)
		}
		if len(decoded) == 0 {
			return nil, errors.New("custody signer returned empty payload")
		}
		return decoded, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("custody signer returned empty payload")
	}
	return []byte(value), nil
}

func fallbackKeyReference(req DeriveAddressRequest) string {
	if strings.TrimSpace(req.KeyReference) != "" {
		return strings.TrimSpace(req.KeyReference)
	}
	return KeyReference(constants.ChainID(req.ChainID), req.DerivationPath)
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func envDuration(fallback time.Duration, keys ...string) time.Duration {
	for _, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		parsed, err := time.ParseDuration(value)
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}
