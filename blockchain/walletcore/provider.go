package walletcore

import "core/constants"

type Provider interface {
	GenerateMnemonic(strength int) (string, error)
	ValidateMnemonic(mnemonic string) bool
	DerivePrivateKey(mnemonic, derivationPath string, chainID constants.ChainID) (string, error)
	DeriveWallet(mnemonic, derivationPath string, chainID constants.ChainID) (*DerivedWallet, error)
}

type DerivedWallet struct {
	PrivateKey string
	Address    string
}

func GenerateMnemonic(strength int) (string, error) {
	return provider.GenerateMnemonic(strength)
}

func ValidateMnemonic(mnemonic string) bool {
	return provider.ValidateMnemonic(mnemonic)
}

func DerivePrivateKey(mnemonic, derivationPath string, chainID constants.ChainID) (string, error) {
	return provider.DerivePrivateKey(mnemonic, derivationPath, chainID)
}

func DeriveWallet(mnemonic, derivationPath string, chainID constants.ChainID) (*DerivedWallet, error) {
	return provider.DeriveWallet(mnemonic, derivationPath, chainID)
}
