package addrutil

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ripemd160"
)

const (
	bech32Const  = 1
	bech32MConst = 0x2bc830a3
)

var bech32Charset = []byte("qpzry9x8gf2tvdw0s3jn54khce6mua7l")

var bech32CharsetRev = [128]int{}

func init() {
	for i := range bech32CharsetRev {
		bech32CharsetRev[i] = -1
	}
	for i, c := range bech32Charset {
		bech32CharsetRev[c] = i
	}
}

type WitnessProgram struct {
	HRP     string
	Version byte
	Program []byte
}

func Hash160(data []byte) []byte {
	sha := sha256.Sum256(data)
	ripemd := ripemd160.New()
	_, _ = ripemd.Write(sha[:])
	return ripemd.Sum(nil)
}

func BitcoinWitnessScript(address string) ([]byte, *WitnessProgram, error) {
	witness, err := DecodeSegWitAddress(address)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case witness.Version == 0 && len(witness.Program) == 20:
		script := append([]byte{0x00, 0x14}, witness.Program...)
		return script, witness, nil
	case witness.Version == 1 && len(witness.Program) == 32:
		script := append([]byte{0x51, 0x20}, witness.Program...)
		return script, witness, nil
	default:
		return nil, nil, fmt.Errorf("unsupported bitcoin witness program version=%d length=%d", witness.Version, len(witness.Program))
	}
}

func DecodeSegWitAddress(address string) (*WitnessProgram, error) {
	hrp, data, checksum, err := decodeBech32(address)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("segwit address missing witness version")
	}
	version := data[0]
	if version > 16 {
		return nil, fmt.Errorf("invalid segwit witness version %d", version)
	}
	program, err := convertBits(data[1:], 5, 8, false)
	if err != nil {
		return nil, err
	}
	if len(program) < 2 || len(program) > 40 {
		return nil, fmt.Errorf("invalid segwit witness program length %d", len(program))
	}
	if version == 0 {
		if checksum != bech32Const {
			return nil, errors.New("segwit v0 requires bech32 checksum")
		}
		if len(program) != 20 && len(program) != 32 {
			return nil, fmt.Errorf("invalid segwit v0 program length %d", len(program))
		}
	} else if checksum != bech32MConst {
		return nil, errors.New("segwit v1+ requires bech32m checksum")
	}
	return &WitnessProgram{HRP: hrp, Version: byte(version), Program: program}, nil
}

func EncodeSegWitAddress(hrp string, version byte, program []byte) (string, error) {
	if version > 16 {
		return "", fmt.Errorf("invalid segwit witness version %d", version)
	}
	converted, err := convertBits(program, 8, 5, true)
	if err != nil {
		return "", err
	}
	data := append([]byte{version}, converted...)
	checksum := bech32MConst
	if version == 0 {
		checksum = bech32Const
	}
	return encodeBech32(strings.ToLower(hrp), data, checksum)
}

func IsBitcoinMainnetAddress(address string) bool {
	if witness, err := DecodeSegWitAddress(address); err == nil {
		return witness.HRP == "bc"
	}
	version, payload, err := Base58CheckDecode(address)
	if err != nil {
		return false
	}
	return len(payload) == 20 && (version == 0x00 || version == 0x05)
}

func decodeBech32(value string) (string, []byte, int, error) {
	if value == "" {
		return "", nil, 0, errors.New("empty bech32 string")
	}
	lower := strings.ToLower(value)
	upper := strings.ToUpper(value)
	if value != lower && value != upper {
		return "", nil, 0, errors.New("mixed-case bech32 string")
	}
	value = lower
	separator := strings.LastIndexByte(value, '1')
	if separator < 1 || separator+7 > len(value) {
		return "", nil, 0, errors.New("invalid bech32 separator")
	}
	hrp := value[:separator]
	rawData := value[separator+1:]
	data := make([]byte, len(rawData))
	for i := range rawData {
		c := rawData[i]
		if c > 127 || bech32CharsetRev[c] < 0 {
			return "", nil, 0, fmt.Errorf("invalid bech32 character %q", c)
		}
		data[i] = byte(bech32CharsetRev[c])
	}
	checksum := bech32Polymod(append(bech32HrpExpand(hrp), data...))
	if checksum != bech32Const && checksum != bech32MConst {
		return "", nil, 0, errors.New("invalid bech32 checksum")
	}
	payload := make([]byte, len(data)-6)
	copy(payload, data[:len(data)-6])
	return hrp, payload, checksum, nil
}

func encodeBech32(hrp string, data []byte, checksumConst int) (string, error) {
	checksum := bech32CreateChecksum(hrp, data, checksumConst)
	combined := append(append([]byte{}, data...), checksum...)
	var out strings.Builder
	out.Grow(len(hrp) + 1 + len(combined))
	out.WriteString(hrp)
	out.WriteByte('1')
	for _, value := range combined {
		if int(value) >= len(bech32Charset) {
			return "", fmt.Errorf("invalid bech32 value %d", value)
		}
		out.WriteByte(bech32Charset[value])
	}
	return out.String(), nil
}

func bech32CreateChecksum(hrp string, data []byte, checksumConst int) []byte {
	values := append(bech32HrpExpand(hrp), data...)
	values = append(values, 0, 0, 0, 0, 0, 0)
	polymod := bech32Polymod(values) ^ checksumConst
	checksum := make([]byte, 6)
	for i := 0; i < 6; i++ {
		checksum[i] = byte((polymod >> uint(5*(5-i))) & 31)
	}
	return checksum
}

func bech32HrpExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

func bech32Polymod(values []byte) int {
	generator := [5]int{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := 1
	for _, value := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ int(value)
		for i := 0; i < 5; i++ {
			if (top>>uint(i))&1 == 1 {
				chk ^= generator[i]
			}
		}
	}
	return chk
}

func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	var acc uint
	var bits uint
	maxv := uint((1 << toBits) - 1)
	maxAcc := uint((1 << (fromBits + toBits - 1)) - 1)
	out := make([]byte, 0, len(data)*int(fromBits)/int(toBits))
	for _, value := range data {
		v := uint(value)
		if v>>fromBits != 0 {
			return nil, fmt.Errorf("invalid data range %d", value)
		}
		acc = ((acc << fromBits) | v) & maxAcc
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			out = append(out, byte((acc>>bits)&maxv))
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, errors.New("invalid incomplete group")
	}
	return out, nil
}
