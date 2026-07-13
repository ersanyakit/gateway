package signer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
)

const (
	MetadataBoundary                  = "boundary"
	BoundaryWalletDerivation          = "wallet_derivation"
	BoundarySoftwarePrivateKeySigning = "software_private_key_signing"
	BoundaryExternalCustodySigning    = "external_custody_signing"
)

var ErrProductionSecretMaterialAccessDisabled = errors.New("production secret material access is disabled")

type DeriveAddressRequest struct {
	Chain          string
	ChainID        int
	KeyReference   string
	DerivationPath string
	Asset          string
	PolicyMetadata map[string]string
}

type DeriveAddressResponse struct {
	Address         string
	KeyReference    string
	DerivationPath  string
	SignerMode      string
	CustodyProvider string
}

type SignTransactionRequest struct {
	Chain           string
	ChainID         int
	KeyReference    string
	DerivationPath  string
	Intent          string
	AmountRaw       string
	Destination     string
	UnsignedPayload []byte
	PolicyMetadata  map[string]string
}

type SignTransactionResponse struct {
	SignedPayload []byte
	TxHash        string
	KeyReference  string
	AuditID       string
}

type SignMessageRequest struct {
	Chain          string
	ChainID        int
	KeyReference   string
	DerivationPath string
	Intent         string
	Message        []byte
	PolicyMetadata map[string]string
}

type SignMessageResponse struct {
	Signature    []byte
	KeyReference string
	AuditID      string
}

type AdapterHealth struct {
	Ready    bool
	Mode     string
	Provider string
	Details  string
}

type CustodyAdapter interface {
	DeriveAddress(context.Context, DeriveAddressRequest) (DeriveAddressResponse, error)
	SignTransaction(context.Context, SignTransactionRequest) (SignTransactionResponse, error)
	SignMessage(context.Context, SignMessageRequest) (SignMessageResponse, error)
	KeyReference(context.Context, DeriveAddressRequest) (string, error)
	Health(context.Context) AdapterHealth
}

var adapterRegistry = struct {
	sync.RWMutex
	adapter CustodyAdapter
}{}

func RegisterCustodyAdapter(adapter CustodyAdapter) func() {
	adapterRegistry.Lock()
	previous := adapterRegistry.adapter
	adapterRegistry.adapter = adapter
	adapterRegistry.Unlock()
	return func() {
		adapterRegistry.Lock()
		adapterRegistry.adapter = previous
		adapterRegistry.Unlock()
	}
}

func ActiveCustodyAdapter() CustodyAdapter {
	adapterRegistry.RLock()
	defer adapterRegistry.RUnlock()
	return adapterRegistry.adapter
}

type RuntimeStatus struct {
	Mode          string
	ExternalMode  bool
	AdapterActive bool
	AdapterReady  bool
	Provider      string
	Details       string
}

func Status(ctx context.Context) RuntimeStatus {
	if err := ensureConfiguredCustodyAdapterFromEnv(); err != nil {
		log.Printf("signer runtime status adapter configuration error=%v", err)
	}
	mode := CurrentMode()
	status := RuntimeStatus{Mode: mode, ExternalMode: IsExternalMode(mode)}
	adapter := ActiveCustodyAdapter()
	if adapter == nil {
		return status
	}
	status.AdapterActive = true
	health := adapter.Health(ctx)
	status.AdapterReady = health.Ready
	status.Provider = strings.TrimSpace(health.Provider)
	status.Details = strings.TrimSpace(health.Details)
	return status
}

func externalAdapterReady(ctx context.Context, mode string) (bool, string, error) {
	adapter := ActiveCustodyAdapter()
	if adapter == nil {
		return false, "", ErrExternalSignerIntegrationRequired
	}
	health := adapter.Health(ctx)
	provider := strings.TrimSpace(health.Provider)
	healthMode := strings.TrimSpace(health.Mode)
	if healthMode != "" && NormalizeMode(healthMode) != NormalizeMode(mode) {
		return false, provider, fmt.Errorf("%w: adapter mode %s does not match SIGNER_MODE=%s", ErrExternalSignerIntegrationRequired, healthMode, mode)
	}
	if !health.Ready {
		return false, provider, ErrExternalSignerIntegrationRequired
	}
	return true, provider, nil
}

func productionLocalSecretBoundary(req Request) bool {
	if !IsProduction() {
		return false
	}
	boundary := ""
	if req.PolicyMetadata != nil {
		boundary = req.PolicyMetadata[MetadataBoundary]
	}
	switch NormalizeMode(boundary) {
	case NormalizeMode(BoundaryWalletDerivation), NormalizeMode(BoundarySoftwarePrivateKeySigning):
		return true
	default:
		return false
	}
}
