//go:build trustwalletcore

package walletcore

/*
#cgo CFLAGS: -I../../third_party/trustwallet/wallet-core/include
#cgo LDFLAGS: -L../../third_party/trustwallet/wallet-core/build -L../../third_party/trustwallet/wallet-core/build/local/lib -L../../third_party/trustwallet/wallet-core/build/trezor-crypto -lTrustWalletCore -lwallet_core_rs -lprotobuf -lTrezorCrypto -lstdc++ -lm
#include <stdlib.h>
#include <TrustWalletCore/TWCoinType.h>
#include <TrustWalletCore/TWData.h>
#include <TrustWalletCore/TWHDWallet.h>
#include <TrustWalletCore/TWMnemonic.h>
#include <TrustWalletCore/TWPrivateKey.h>
#include <TrustWalletCore/TWString.h>
*/
import "C"

import (
	"encoding/hex"
	"errors"
	"unsafe"

	"core/constants"
)

var provider Provider = trustWalletCoreProvider{}

type trustWalletCoreProvider struct{}

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

func (trustWalletCoreProvider) DerivePrivateKey(mnemonic, derivationPath string, chainID constants.ChainID) (string, error) {
	mn := twString(mnemonic)
	empty := twString("")
	defer C.TWStringDelete(mn)
	defer C.TWStringDelete(empty)

	wallet := C.TWHDWalletCreateWithMnemonic(mn, empty)
	if wallet == nil {
		return "", errors.New("trustwalletcore: invalid mnemonic")
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
		return "", errors.New("trustwalletcore: key derivation failed")
	}
	defer C.TWPrivateKeyDelete(key)

	data := C.TWPrivateKeyData(key)
	defer C.TWDataDelete(data)

	size := C.TWDataSize(data)
	raw := C.GoBytes(unsafe.Pointer(C.TWDataBytes(data)), C.int(size))
	return hex.EncodeToString(raw), nil
}

func twString(value string) unsafe.Pointer {
	cstr := C.CString(value)
	defer C.free(unsafe.Pointer(cstr))
	return C.TWStringCreateWithUTF8Bytes(cstr)
}

func coinTypeForChain(chainID constants.ChainID) C.enum_TWCoinType {
	switch chainID {
	case constants.Bitcoin:
		return C.enum_TWCoinType(C.TWCoinTypeBitcoin)
	case constants.Solana:
		return C.enum_TWCoinType(C.TWCoinTypeSolana)
	case constants.TRON:
		return C.enum_TWCoinType(C.TWCoinTypeTron)
	default:
		return C.enum_TWCoinType(C.TWCoinTypeEthereum)
	}
}
