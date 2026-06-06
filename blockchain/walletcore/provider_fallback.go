//go:build !trustwalletcore

package walletcore

import (
	"encoding/hex"

	"core/constants"

	"github.com/okx/go-wallet-sdk/crypto/go-bip32"
	"github.com/okx/go-wallet-sdk/crypto/go-bip39"
)

var provider Provider = fallbackProvider{}

type fallbackProvider struct{}

func (fallbackProvider) GenerateMnemonic(strength int) (string, error) {
	entropy, err := bip39.NewEntropy(strength)
	if err != nil {
		return "", err
	}
	return bip39.NewMnemonic(entropy)
}

func (fallbackProvider) ValidateMnemonic(mnemonic string) bool {
	return bip39.IsMnemonicValid(mnemonic)
}

func (fallbackProvider) DerivePrivateKey(mnemonic, derivationPath string, _ constants.ChainID) (string, error) {
	seed := bip39.NewSeed(mnemonic, "")
	root, err := bip32.NewMasterKey(seed)
	if err != nil {
		return "", err
	}
	child, err := root.NewChildKeyByPathString(derivationPath)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(child.Key), nil
}
