//go:build !walletcorefallback

package walletcore

/*
#cgo CFLAGS: -I../../third_party/trustwallet/wallet-core/include
#cgo LDFLAGS: -L../../third_party/trustwallet/wallet-core/build -L../../third_party/trustwallet/wallet-core/build/local/lib -L../../third_party/trustwallet/wallet-core/build/trezor-crypto -lTrustWalletCore -lwallet_core_rs -lprotobuf -lTrezorCrypto -lstdc++ -lm

// Trust Wallet Core's public TWBase.h uses Clang's function-like
// __has_feature operator without defining the conventional fallback. cgo uses
// the platform C compiler (GCC on Ubuntu), so provide the no-feature fallback
// before any TrustWalletCore header is parsed. Clang keeps its builtin.
#ifndef __has_feature
#define __has_feature(feature) 0
#endif

#include <stdlib.h>
#include <TrustWalletCore/TWAnyAddress.h>
#include <TrustWalletCore/TWAnySigner.h>
#include <TrustWalletCore/TWCoinType.h>
#include <TrustWalletCore/TWCurve.h>
#include <TrustWalletCore/TWData.h>
#include <TrustWalletCore/TWDerivation.h>
#include <TrustWalletCore/TWHDWallet.h>
#include <TrustWalletCore/TWMnemonic.h>
#include <TrustWalletCore/TWPrivateKey.h>
#include <TrustWalletCore/TWPublicKey.h>
#include <TrustWalletCore/TWString.h>
*/
import "C"

import (
	"encoding/hex"
	"errors"
	"unsafe"

	"core/constants"

	"google.golang.org/protobuf/proto"
)

var provider Provider = trustWalletCoreProvider{}

type trustWalletCoreProvider struct{}

func IsFallbackBuild() bool {
	return false
}

func (trustWalletCoreProvider) GenerateMnemonic(strength int) (string, error) {
	empty := twString("")
	defer C.TWStringDelete(empty)
	wallet := C.TWHDWalletCreate(C.int(strength), empty)
	if wallet == nil {
		return "", errors.New("trustwalletcore: wallet creation failed")
	}
	defer C.TWHDWalletDelete(wallet)
	mnemonic := C.TWHDWalletMnemonic(wallet)
	defer C.TWStringDelete(mnemonic)
	return C.GoString(C.TWStringUTF8Bytes(mnemonic)), nil
}

func (trustWalletCoreProvider) ValidateMnemonic(mnemonic string) bool {
	value := twString(mnemonic)
	defer C.TWStringDelete(value)
	return bool(C.TWMnemonicIsValid(value))
}

func (trustWalletCoreProvider) ValidateAddress(address string, chainID constants.ChainID) bool {
	value := twString(address)
	defer C.TWStringDelete(value)
	return bool(C.TWAnyAddressIsValid(value, coinTypeForChain(chainID)))
}

func (trustWalletCoreProvider) AddressFromPrivateKey(privateKeyHex string, chainID constants.ChainID) (string, error) {
	raw, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return "", err
	}
	if chainID == constants.Solana && len(raw) == 64 {
		raw = raw[:32]
	}
	data := twDataFromBytes(raw)
	defer C.TWDataDelete(data)
	if !bool(C.TWPrivateKeyIsValid(data, curveForChain(chainID))) {
		return "", errors.New("trustwalletcore: invalid private key")
	}
	key := C.TWPrivateKeyCreateWithData(data)
	if key == nil {
		return "", errors.New("trustwalletcore: private key creation failed")
	}
	defer C.TWPrivateKeyDelete(key)
	return trustWalletAddress(key, coinTypeForChain(chainID), chainID)
}

func (trustWalletCoreProvider) DerivePrivateKey(mnemonic, derivationPath string, chainID constants.ChainID) (string, error) {
	key, _, err := deriveTrustWalletKey(mnemonic, derivationPath, chainID)
	if err != nil {
		return "", err
	}
	defer C.TWPrivateKeyDelete(key)

	return trustWalletPrivateKeyHex(key)
}

func (trustWalletCoreProvider) DeriveWallet(mnemonic, derivationPath string, chainID constants.ChainID) (*DerivedWallet, error) {
	key, coin, err := deriveTrustWalletKey(mnemonic, derivationPath, chainID)
	if err != nil {
		return nil, err
	}
	defer C.TWPrivateKeyDelete(key)

	privateKey, err := trustWalletPrivateKeyHex(key)
	if err != nil {
		return nil, err
	}
	address, err := trustWalletAddress(key, coin, chainID)
	if err != nil {
		return nil, err
	}
	return &DerivedWallet{
		PrivateKey: privateKey,
		Address:    address,
	}, nil
}

func (trustWalletCoreProvider) Sign(input proto.Message, output proto.Message, chainID constants.ChainID) error {
	if input == nil {
		return errors.New("trustwalletcore: signing input is required")
	}
	if output == nil {
		return errors.New("trustwalletcore: signing output is required")
	}

	inputBytes, err := proto.Marshal(input)
	if err != nil {
		return err
	}
	inputData := twDataFromBytes(inputBytes)
	defer C.TWDataDelete(inputData)

	outputData := C.TWAnySignerSign(inputData, coinTypeForChain(chainID))
	if outputData == nil {
		return errors.New("trustwalletcore: signer returned nil output")
	}
	defer C.TWDataDelete(outputData)

	outputBytes := twDataToBytes(outputData)
	if len(outputBytes) == 0 {
		return errors.New("trustwalletcore: signer returned empty output")
	}
	return proto.Unmarshal(outputBytes, output)
}

func deriveTrustWalletKey(mnemonic, derivationPath string, chainID constants.ChainID) (*C.struct_TWPrivateKey, C.enum_TWCoinType, error) {
	mn := twString(mnemonic)
	empty := twString("")
	defer C.TWStringDelete(mn)
	defer C.TWStringDelete(empty)

	wallet := C.TWHDWalletCreateWithMnemonic(mn, empty)
	if wallet == nil {
		return nil, 0, errors.New("trustwalletcore: invalid mnemonic")
	}
	defer C.TWHDWalletDelete(wallet)

	coin := coinTypeForChain(chainID)
	var key *C.struct_TWPrivateKey
	if derivationPath == "" {
		key = C.TWHDWalletGetKeyForCoin(wallet, coin)
	} else {
		path := twString(derivationPath)
		defer C.TWStringDelete(path)
		key = C.TWHDWalletGetKey(wallet, coin, path)
	}
	if key == nil {
		return nil, coin, errors.New("trustwalletcore: key derivation failed")
	}
	return key, coin, nil
}

func trustWalletPrivateKeyHex(key *C.struct_TWPrivateKey) (string, error) {
	data := C.TWPrivateKeyData(key)
	defer C.TWDataDelete(data)

	size := C.TWDataSize(data)
	raw := C.GoBytes(unsafe.Pointer(C.TWDataBytes(data)), C.int(size))
	return hex.EncodeToString(raw), nil
}

func trustWalletAddress(key *C.struct_TWPrivateKey, coin C.enum_TWCoinType, chainID constants.ChainID) (string, error) {
	publicKey := C.TWPrivateKeyGetPublicKey(key, coin)
	if publicKey == nil {
		return "", errors.New("trustwalletcore: public key derivation failed")
	}
	defer C.TWPublicKeyDelete(publicKey)

	var address *C.struct_TWAnyAddress
	if chainID == constants.Bitcoin {
		address = C.TWAnyAddressCreateWithPublicKeyDerivation(publicKey, coin, C.enum_TWDerivation(C.TWDerivationBitcoinTaproot))
	} else {
		address = C.TWAnyAddressCreateWithPublicKey(publicKey, coin)
	}
	if address == nil {
		return "", errors.New("trustwalletcore: address derivation failed")
	}
	defer C.TWAnyAddressDelete(address)

	description := C.TWAnyAddressDescription(address)
	defer C.TWStringDelete(description)

	return C.GoString(C.TWStringUTF8Bytes(description)), nil
}

func twString(value string) unsafe.Pointer {
	cstr := C.CString(value)
	defer C.free(unsafe.Pointer(cstr))
	return C.TWStringCreateWithUTF8Bytes(cstr)
}

func twDataFromBytes(value []byte) unsafe.Pointer {
	if len(value) == 0 {
		return C.TWDataCreateWithSize(0)
	}
	return C.TWDataCreateWithBytes((*C.uint8_t)(unsafe.Pointer(&value[0])), C.size_t(len(value)))
}

func twDataToBytes(data unsafe.Pointer) []byte {
	size := C.TWDataSize(data)
	if size == 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(C.TWDataBytes(data)), C.int(size))
}

func coinTypeForChain(chainID constants.ChainID) C.enum_TWCoinType {
	switch chainID {
	case constants.Bitcoin:
		return C.enum_TWCoinType(C.TWCoinTypeBitcoin)
	case constants.Solana:
		return C.enum_TWCoinType(C.TWCoinTypeSolana)
	case constants.TRON, constants.TRONTestnet:
		return C.enum_TWCoinType(C.TWCoinTypeTron)
	default:
		return C.enum_TWCoinType(C.TWCoinTypeEthereum)
	}
}

func curveForChain(chainID constants.ChainID) C.enum_TWCurve {
	if chainID == constants.Solana {
		return C.enum_TWCurve(C.TWCurveED25519)
	}
	return C.enum_TWCurve(C.TWCurveSECP256k1)
}
