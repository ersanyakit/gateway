package chains

import (
	"context"
	"errors"
	"testing"

	"core/blockchain"
	"core/constants"
	"core/services/signer"
)

func TestAuthorizeWalletSigningRejectsProductionLocalPrivateKeyBoundary(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "vault")
	restore := signer.RegisterCustodyAdapter(testCustodyAdapter{})
	defer restore()

	err := authorizeWalletSigning(context.Background(), "ethereum", constants.Ethereum, blockchain.WalletDetails{
		Address:        "0x1111111111111111111111111111111111111111",
		PrivateKey:     "secret-private-key",
		KeyReference:   "vault:key:ethereum-hot",
		DerivationPath: "m/44'/60'/0'/0/1",
	}, "transfer.native", "100", "0x2222222222222222222222222222222222222222")
	if !errors.Is(err, signer.ErrProductionSecretMaterialAccessDisabled) {
		t.Fatalf("authorizeWalletSigning err=%v, want ErrProductionSecretMaterialAccessDisabled", err)
	}
}

type testCustodyAdapter struct{}

func (testCustodyAdapter) DeriveAddress(context.Context, signer.DeriveAddressRequest) (signer.DeriveAddressResponse, error) {
	return signer.DeriveAddressResponse{}, nil
}

func (testCustodyAdapter) SignTransaction(context.Context, signer.SignTransactionRequest) (signer.SignTransactionResponse, error) {
	return signer.SignTransactionResponse{}, nil
}

func (testCustodyAdapter) SignMessage(context.Context, signer.SignMessageRequest) (signer.SignMessageResponse, error) {
	return signer.SignMessageResponse{}, nil
}

func (testCustodyAdapter) KeyReference(context.Context, signer.DeriveAddressRequest) (string, error) {
	return "vault:key:ref", nil
}

func (testCustodyAdapter) Health(context.Context) signer.AdapterHealth {
	return signer.AdapterHealth{Ready: true, Mode: "vault", Provider: "vault-primary"}
}
