package addrutil

import "golang.org/x/crypto/sha3"

func TronAddressFromUncompressedPublicKey(pubKey []byte) (string, error) {
	if len(pubKey) != 65 || pubKey[0] != 0x04 {
		return "", ErrBase58CheckFormat
	}
	hash := sha3.NewLegacyKeccak256()
	_, _ = hash.Write(pubKey[1:])
	return Base58CheckEncode(TronAddressVersion, hash.Sum(nil)[12:]), nil
}
