package addrutil

import (
	"crypto/sha256"
	"errors"

	"github.com/mr-tron/base58"
)

const TronAddressVersion byte = 0x41

var (
	ErrBase58CheckFormat   = errors.New("base58check: invalid format")
	ErrBase58CheckChecksum = errors.New("base58check: invalid checksum")
)

func Base58CheckEncode(version byte, payload []byte) string {
	data := make([]byte, 0, 1+len(payload)+4)
	data = append(data, version)
	data = append(data, payload...)
	checksum := doubleSHA256(data)
	data = append(data, checksum[:4]...)
	return base58.Encode(data)
}

func Base58CheckDecode(value string) (byte, []byte, error) {
	decoded, err := base58.Decode(value)
	if err != nil {
		return 0, nil, err
	}
	if len(decoded) < 5 {
		return 0, nil, ErrBase58CheckFormat
	}
	body := decoded[:len(decoded)-4]
	want := decoded[len(decoded)-4:]
	got := doubleSHA256(body)
	for i := range want {
		if want[i] != got[i] {
			return 0, nil, ErrBase58CheckChecksum
		}
	}
	payload := make([]byte, len(body)-1)
	copy(payload, body[1:])
	return body[0], payload, nil
}

func TronAddressHash(address string) ([]byte, error) {
	version, payload, err := Base58CheckDecode(address)
	if err != nil {
		return nil, err
	}
	if version != TronAddressVersion || len(payload) != 20 {
		return nil, ErrBase58CheckFormat
	}
	hash := make([]byte, 1+len(payload))
	hash[0] = version
	copy(hash[1:], payload)
	return hash, nil
}

func TronAddressFromHash(raw []byte) (string, error) {
	switch {
	case len(raw) == 20:
		return Base58CheckEncode(TronAddressVersion, raw), nil
	case len(raw) == 21 && raw[0] == TronAddressVersion:
		return Base58CheckEncode(raw[0], raw[1:]), nil
	default:
		return "", ErrBase58CheckFormat
	}
}

func doubleSHA256(data []byte) [32]byte {
	first := sha256.Sum256(data)
	return sha256.Sum256(first[:])
}
