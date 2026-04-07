package helpers

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type Keystore struct {
	Address string `json:"address"`
}

type AddressInput struct {
	Address     string `json:"address"`
	PrivateKey  string `json:"private_key"`
	PrivateKey2 string `json:"privateKey"`
	Key         string `json:"key"`
}

func LoadAddressesFromDir(root string) ([]string, error) {
	var addresses []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var ks Keystore
		if err := json.Unmarshal(data, &ks); err != nil {
			return nil
		}

		if addr, ok := normalizeAddress(ks.Address); ok {
			addresses = append(addresses, addr)
		}

		return nil
	})

	return addresses, err
}

func LoadAddressesFromJSON(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	addresses, err := parseAddressesJSON(data)
	if err != nil {
		return nil, fmt.Errorf("parse addresses json: %w", err)
	}

	return addresses, nil
}

func LoadAddressesFromPrivateKeyList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	addresses, err := parsePrivateKeyAddresses(data)
	if err != nil {
		return nil, fmt.Errorf("parse private key list: %w", err)
	}

	if len(addresses) == 0 {
		return nil, fmt.Errorf("no valid private keys or addresses found")
	}

	return addresses, nil
}

func parseAddressesJSON(data []byte) ([]string, error) {
	var keystores []Keystore
	if err := json.Unmarshal(data, &keystores); err == nil {
		return collectKeystoreAddresses(keystores), nil
	}

	var singleKeystore Keystore
	if err := json.Unmarshal(data, &singleKeystore); err == nil {
		return collectKeystoreAddresses([]Keystore{singleKeystore}), nil
	}

	var rawAddresses []string
	if err := json.Unmarshal(data, &rawAddresses); err == nil {
		return collectRawAddresses(rawAddresses), nil
	}

	return nil, fmt.Errorf("unsupported address json format")
}

func parsePrivateKeyAddresses(data []byte) ([]string, error) {
	var inputs []AddressInput
	if err := json.Unmarshal(data, &inputs); err == nil {
		return collectAddressInputs(inputs), nil
	}

	var singleInput AddressInput
	if err := json.Unmarshal(data, &singleInput); err == nil {
		return collectAddressInputs([]AddressInput{singleInput}), nil
	}

	var rawValues []string
	if err := json.Unmarshal(data, &rawValues); err == nil {
		return collectValuesAsAddresses(rawValues), nil
	}

	var rawValue string
	if err := json.Unmarshal(data, &rawValue); err == nil {
		return collectValuesAsAddresses([]string{rawValue}), nil
	}

	return parsePrivateKeyAddressLines(data), nil
}

func collectKeystoreAddresses(keystores []Keystore) []string {
	addresses := make([]string, 0, len(keystores))
	for _, ks := range keystores {
		if addr, ok := normalizeAddress(ks.Address); ok {
			addresses = append(addresses, addr)
		}
	}

	return addresses
}

func collectRawAddresses(rawAddresses []string) []string {
	addresses := make([]string, 0, len(rawAddresses))
	for _, rawAddress := range rawAddresses {
		if addr, ok := normalizeAddress(rawAddress); ok {
			addresses = append(addresses, addr)
		}
	}

	return addresses
}

func collectAddressInputs(inputs []AddressInput) []string {
	addresses := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if addr, ok := normalizeAddress(input.Address); ok {
			addresses = append(addresses, addr)
			continue
		}

		for _, candidate := range []string{input.PrivateKey, input.PrivateKey2, input.Key} {
			if addr, ok := normalizeAddressOrPrivateKey(candidate); ok {
				addresses = append(addresses, addr)
				break
			}
		}
	}

	return addresses
}

func collectValuesAsAddresses(values []string) []string {
	addresses := make([]string, 0, len(values))
	for _, value := range values {
		if addr, ok := normalizeAddressOrPrivateKey(value); ok {
			addresses = append(addresses, addr)
		}
	}

	return addresses
}

func parsePrivateKeyAddressLines(data []byte) []string {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	addresses := make([]string, 0)

	for scanner.Scan() {
		if addr, ok := normalizeAddressOrPrivateKey(scanner.Text()); ok {
			addresses = append(addresses, addr)
		}
	}

	return addresses
}

func normalizeAddress(address string) (string, bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", false
	}

	if strings.HasPrefix(address, "0x") || strings.HasPrefix(address, "0X") {
		return address, true
	}

	return "0x" + address, true
}

func normalizeAddressOrPrivateKey(value string) (string, bool) {
	if addr, ok := normalizeAddressCandidate(value); ok {
		return addr, true
	}

	return privateKeyToAddress(value)
}

func normalizeAddressCandidate(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}

	if common.IsHexAddress(value) {
		return common.HexToAddress(value).Hex(), true
	}

	noPrefix := strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	if len(noPrefix) == 40 && isHexString(noPrefix) {
		return common.HexToAddress("0x" + noPrefix).Hex(), true
	}

	return "", false
}

func privateKeyToAddress(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}

	noPrefix := strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	if len(noPrefix) != 64 || !isHexString(noPrefix) {
		return "", false
	}

	privateKey, err := crypto.HexToECDSA(noPrefix)
	if err != nil {
		return "", false
	}

	return crypto.PubkeyToAddress(privateKey.PublicKey).Hex(), true
}

func isHexString(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}
