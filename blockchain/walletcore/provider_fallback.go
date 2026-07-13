//go:build walletcorefallback

package walletcore

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"core/blockchain/addrutil"
	"core/constants"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gagliardetto/solana-go"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/sha3"
	"google.golang.org/protobuf/proto"
)

var provider Provider = fallbackProvider{}

type fallbackProvider struct{}

const bip32HardenedOffset = uint32(0x80000000)

func IsFallbackBuild() bool {
	return true
}

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

func (fallbackProvider) ValidateAddress(address string, chainID constants.ChainID) bool {
	address = strings.TrimSpace(address)
	if address == "" {
		return false
	}

	switch chainID {
	case constants.Bitcoin:
		return addrutil.IsBitcoinMainnetAddress(address)
	case constants.Solana:
		_, err := solana.PublicKeyFromBase58(address)
		return err == nil
	case constants.TRON, constants.TRONTestnet:
		_, err := addrutil.TronAddressHash(address)
		return err == nil
	default:
		return fallbackValidateEVMAddress(address)
	}
}

func (fallbackProvider) AddressFromPrivateKey(privateKeyHex string, chainID constants.ChainID) (string, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(privateKeyHex))
	if err != nil {
		return "", err
	}
	switch chainID {
	case constants.Bitcoin:
		privateKey, err := crypto.ToECDSA(raw)
		if err != nil {
			return "", err
		}
		pubKey := crypto.CompressPubkey(&privateKey.PublicKey)
		return addrutil.EncodeSegWitAddress("bc", 0, addrutil.Hash160(pubKey))
	case constants.Solana:
		var publicKey ed25519.PublicKey
		switch len(raw) {
		case ed25519.SeedSize:
			publicKey = ed25519.NewKeyFromSeed(raw).Public().(ed25519.PublicKey)
		case ed25519.PrivateKeySize:
			publicKey = ed25519.PrivateKey(raw).Public().(ed25519.PublicKey)
		default:
			return "", fmt.Errorf("invalid solana private key length %d", len(raw))
		}
		return solana.PublicKeyFromBytes(publicKey).String(), nil
	case constants.TRON, constants.TRONTestnet:
		privateKey, err := crypto.ToECDSA(raw)
		if err != nil {
			return "", err
		}
		pubKey := crypto.FromECDSAPub(&privateKey.PublicKey)
		hash := sha3.NewLegacyKeccak256()
		_, _ = hash.Write(pubKey[1:])
		return addrutil.Base58CheckEncode(addrutil.TronAddressVersion, hash.Sum(nil)[12:]), nil
	default:
		privateKey, err := crypto.ToECDSA(raw)
		if err != nil {
			return "", err
		}
		return crypto.PubkeyToAddress(privateKey.PublicKey).Hex(), nil
	}
}

func (fallbackProvider) DerivePrivateKey(mnemonic, derivationPath string, chainID constants.ChainID) (string, error) {
	if chainID == constants.Solana {
		return deriveSolanaPrivateKey(mnemonic, derivationPath)
	}

	return deriveSecp256k1PrivateKey(mnemonic, derivationPath)
}

func (fallbackProvider) DeriveWallet(mnemonic, derivationPath string, chainID constants.ChainID) (*DerivedWallet, error) {
	return nil, fmt.Errorf("walletcorefallback cannot derive wallet addresses; build with Trust Wallet Core")
}

func (fallbackProvider) Sign(input proto.Message, output proto.Message, chainID constants.ChainID) error {
	return fmt.Errorf("walletcorefallback cannot sign transactions; build with Trust Wallet Core")
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

func deriveSecp256k1PrivateKey(mnemonic, derivationPath string) (string, error) {
	path, err := parseDerivationPath(derivationPath)
	if err != nil {
		return "", err
	}

	seed := bip39.NewSeed(mnemonic, "")
	key, chainCode, err := secp256k1MasterKey(seed)
	if err != nil {
		return "", err
	}
	for _, segment := range path {
		key, chainCode, err = deriveSecp256k1Child(key, chainCode, segment)
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(key), nil
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
			segment += bip32HardenedOffset
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

func secp256k1MasterKey(seed []byte) ([]byte, []byte, error) {
	h := hmac.New(sha512.New, []byte("Bitcoin seed"))
	_, _ = h.Write(seed)
	sum := h.Sum(nil)
	key := append([]byte(nil), sum[:32]...)
	if err := validateSecp256k1PrivateKey(key); err != nil {
		return nil, nil, err
	}
	return key, append([]byte(nil), sum[32:]...), nil
}

func deriveSecp256k1Child(key []byte, chainCode []byte, segment uint32) ([]byte, []byte, error) {
	var data []byte
	if segment >= bip32HardenedOffset {
		data = append([]byte{0}, key...)
	} else {
		privateKey, err := crypto.ToECDSA(key)
		if err != nil {
			return nil, nil, err
		}
		data = append([]byte(nil), crypto.CompressPubkey(&privateKey.PublicKey)...)
	}
	index := make([]byte, 4)
	binary.BigEndian.PutUint32(index, segment)
	data = append(data, index...)

	h := hmac.New(sha512.New, chainCode)
	_, _ = h.Write(data)
	sum := h.Sum(nil)
	left := new(big.Int).SetBytes(sum[:32])
	curveN := crypto.S256().Params().N
	if left.Sign() == 0 || left.Cmp(curveN) >= 0 {
		return nil, nil, fmt.Errorf("invalid BIP32 child key")
	}

	child := new(big.Int).SetBytes(key)
	child.Add(child, left)
	child.Mod(child, curveN)
	if child.Sign() == 0 {
		return nil, nil, fmt.Errorf("invalid BIP32 child key")
	}

	out := child.FillBytes(make([]byte, 32))
	return out, append([]byte(nil), sum[32:]...), nil
}

func validateSecp256k1PrivateKey(key []byte) error {
	value := new(big.Int).SetBytes(key)
	if len(key) != 32 || value.Sign() == 0 || value.Cmp(crypto.S256().Params().N) >= 0 {
		return fmt.Errorf("invalid secp256k1 private key")
	}
	return nil
}

func fallbackValidateEVMAddress(address string) bool {
	address = strings.TrimPrefix(strings.TrimPrefix(address, "0x"), "0X")
	if len(address) != 40 {
		return false
	}
	for i := 0; i < len(address); i++ {
		c := address[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
