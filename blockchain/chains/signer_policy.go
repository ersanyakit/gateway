package chains

import (
	"context"
	"fmt"
	"strings"

	"core/blockchain"
	"core/constants"
	"core/services/signer"
)

func authorizeWalletSigning(ctx context.Context, chainName string, chainID constants.ChainID, wallet blockchain.WalletDetails, intent string, amountRaw string, destination string) error {
	_, err := signer.Authorize(ctx, signer.Request{
		Chain:          chainName,
		ChainID:        int(chainID),
		KeyReference:   walletSigningKeyReference(chainID, wallet),
		DerivationPath: wallet.DerivationPath,
		Intent:         intent,
		AmountRaw:      amountRaw,
		Destination:    strings.TrimSpace(destination),
		PolicyMetadata: map[string]string{
			signer.MetadataBoundary: walletSigningBoundary(wallet),
		},
	})
	return err
}

func walletSigningKeyReference(chainID constants.ChainID, wallet blockchain.WalletDetails) string {
	if strings.TrimSpace(wallet.KeyReference) != "" {
		return strings.TrimSpace(wallet.KeyReference)
	}
	if strings.TrimSpace(wallet.DerivationPath) != "" {
		return signer.KeyReference(chainID, strings.TrimSpace(wallet.DerivationPath))
	}
	if strings.TrimSpace(wallet.Address) != "" {
		return fmt.Sprintf("chain:%d:address:%s", chainID, strings.TrimSpace(wallet.Address))
	}
	return fmt.Sprintf("chain:%d:unspecified", chainID)
}
