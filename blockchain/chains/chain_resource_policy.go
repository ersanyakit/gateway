package chains

import (
	"context"
	"strings"

	"core/blockchain"
	"core/services/chainresource"
	"core/services/signer"
)

var chainResources = chainresource.NewManager()

func chainResourceOwnerID(ctx context.Context, wallet blockchain.WalletDetails, intent string) string {
	audit := signer.AuditContextFrom(ctx)
	for _, value := range []string{
		audit.JobID,
		audit.CorrelationID,
		audit.ActorID,
		wallet.KeyReference,
		wallet.Address,
		intent,
	} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "unspecified"
}

func chainResourceWalletID(wallet blockchain.WalletDetails) string {
	for _, value := range []string{
		wallet.KeyReference,
		wallet.Address,
		wallet.DerivationPath,
	} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "unspecified"
}

func chainResourceSequenceLease(ctx context.Context, chainName string, wallet blockchain.WalletDetails, intent string) (*chainresource.SequenceLease, error) {
	return chainResources.AcquireSequence(ctx, chainresource.SequenceRequest{
		Chain:   chainName,
		Wallet:  chainResourceWalletID(wallet),
		Intent:  intent,
		OwnerID: chainResourceOwnerID(ctx, wallet, intent),
	})
}
