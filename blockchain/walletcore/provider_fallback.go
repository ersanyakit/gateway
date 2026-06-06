//go:build !trustwalletcore

package walletcore

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"

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

func (fallbackProvider) DerivePrivateKey(mnemonic, derivationPath string, chainID constants.ChainID) (string, error) {
	if chainID == constants.Solana {
		return deriveSolanaPrivateKey(mnemonic, derivationPath)
	}

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

func deriveSolanaPrivateKey(mnemonic, derivationPath string) (string, error) {
	path, err := parseDerivationPath(derivationPath)
	if err != nil {
		return "", err
	}

	seed := bip39.NewSeed(mnemonic, "")
	h := hmac.New(sha512.New, []byte("ed25519 seed"))
	h.Write(seed)
	sum := h.Sum(nil)

	derivedSeed := sum[:32]
	chain := sum[32:]
	for _, segment := range path {
		derivedSeed, chain = deriveEd25519Child(derivedSeed, chain, segment)
	}

	return hex.EncodeToString(ed25519.NewKeyFromSeed(derivedSeed)), nil
}

func parseDerivationPath(path string) ([]uint32, error) {
	if path == "" {
		path = "m/44'/501'/0'/0'"
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] != "m" {
		return nil, fmt.Errorf("invalid derivation path: %s", path)
	}

	out := make([]uint32, 0, len(parts)-1)
	for _, part := range parts[1:] {
		hardened := strings.HasSuffix(part, "'")
		part = strings.TrimSuffix(part, "'")
		value, err := strconv.ParseUint(part, 10, 31)
		if err != nil {
			return nil, fmt.Errorf("invalid derivation segment %q: %w", part, err)
		}
		segment := uint32(value)
		if hardened {
			segment += 0x80000000
		}
		out = append(out, segment)
	}
	return out, nil
}

func deriveEd25519Child(key []byte, chainCode []byte, segment uint32) ([]byte, []byte) {
	buf := []byte{0}
	buf = append(buf, key...)
	buf = append(buf, big.NewInt(int64(segment)).Bytes()...)
	h := hmac.New(sha512.New, chainCode)
	h.Write(buf)
	sum := h.Sum(nil)
	return sum[:32], sum[32:]
}
