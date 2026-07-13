package chains

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"core/blockchain"
	"core/constants"
	"core/services/chainresource"
	"core/services/signer"
)

type externalSigningEnvelope struct {
	Format         string         `json:"format"`
	Chain          string         `json:"chain"`
	ChainID        int            `json:"chain_id"`
	KeyReference   string         `json:"key_reference"`
	DerivationPath string         `json:"derivation_path,omitempty"`
	Intent         string         `json:"intent"`
	AmountRaw      string         `json:"amount_raw,omitempty"`
	Destination    string         `json:"destination,omitempty"`
	Payload        map[string]any `json:"payload"`
}

func walletSigningBoundary(wallet blockchain.WalletDetails) string {
	if strings.TrimSpace(wallet.PrivateKey) != "" {
		return signer.BoundarySoftwarePrivateKeySigning
	}
	if signer.IsProduction() || signer.IsExternalMode(signer.CurrentMode()) || wallet.WatchOnly {
		return signer.BoundaryExternalCustodySigning
	}
	return signer.BoundarySoftwarePrivateKeySigning
}

func shouldUseExternalTransactionSigner(wallet blockchain.WalletDetails) bool {
	return signer.IsProduction() || signer.IsExternalMode(signer.CurrentMode()) || wallet.WatchOnly
}

func requireDatabaseResourceReservation(ctx context.Context, chainName string, intent string) error {
	if !signer.IsProduction() {
		return nil
	}
	if chainresource.HasDatabaseReservation(ctx) {
		return nil
	}
	return fmt.Errorf("%s %s requires durable outbound resource reservation in production", chainName, intent)
}

func signTransactionWithCustody(ctx context.Context, chainName string, chainID constants.ChainID, wallet blockchain.WalletDetails, intent string, amountRaw string, destination string, payload map[string]any) (signer.SignTransactionResponse, error) {
	adapter := signer.ActiveCustodyAdapter()
	if adapter == nil {
		return signer.SignTransactionResponse{}, signer.ErrExternalSignerIntegrationRequired
	}
	keyReference := walletSigningKeyReference(chainID, wallet)
	envelope := externalSigningEnvelope{
		Format:         "gateway.external_signing.v1",
		Chain:          strings.TrimSpace(chainName),
		ChainID:        int(chainID),
		KeyReference:   keyReference,
		DerivationPath: strings.TrimSpace(wallet.DerivationPath),
		Intent:         strings.TrimSpace(intent),
		AmountRaw:      strings.TrimSpace(amountRaw),
		Destination:    strings.TrimSpace(destination),
		Payload:        payload,
	}
	unsignedPayload, err := json.Marshal(envelope)
	if err != nil {
		return signer.SignTransactionResponse{}, fmt.Errorf("%s external signing payload encode failed: %w", chainName, err)
	}
	return adapter.SignTransaction(ctx, signer.SignTransactionRequest{
		Chain:           chainName,
		ChainID:         int(chainID),
		KeyReference:    keyReference,
		DerivationPath:  strings.TrimSpace(wallet.DerivationPath),
		Intent:          strings.TrimSpace(intent),
		AmountRaw:       strings.TrimSpace(amountRaw),
		Destination:     strings.TrimSpace(destination),
		UnsignedPayload: unsignedPayload,
		PolicyMetadata: map[string]string{
			signer.MetadataBoundary: signer.BoundaryExternalCustodySigning,
		},
	})
}

func signedPayloadBytes(response signer.SignTransactionResponse) ([]byte, error) {
	payload := bytes.TrimSpace(response.SignedPayload)
	if len(payload) == 0 {
		return nil, errors.New("external signer returned empty signed payload")
	}
	text := strings.TrimSpace(string(payload))
	text = strings.TrimPrefix(text, "0x")
	if text != "" && len(text)%2 == 0 && isHexText(text) {
		decoded, err := hex.DecodeString(text)
		if err == nil && len(decoded) > 0 {
			return decoded, nil
		}
	}
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload))); err == nil && len(decoded) > 0 {
		return decoded, nil
	}
	return payload, nil
}

func signedPayloadHex(response signer.SignTransactionResponse) (string, error) {
	payload := bytes.TrimSpace(response.SignedPayload)
	if len(payload) == 0 {
		return "", errors.New("external signer returned empty signed payload")
	}
	text := strings.TrimSpace(string(payload))
	text = strings.TrimPrefix(text, "0x")
	if text != "" && len(text)%2 == 0 && isHexText(text) {
		return strings.ToLower(text), nil
	}
	return hex.EncodeToString(payload), nil
}

func isHexText(value string) bool {
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}
