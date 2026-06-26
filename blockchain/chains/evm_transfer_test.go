package chains

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"core/constants"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestEVMSignNativeUsesTrustWalletCore(t *testing.T) {
	privateKey, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}

	tx, err := evmSignNativeWithTrustWallet(
		"ethereum",
		constants.Ethereum,
		privateKey,
		7,
		big.NewInt(1_000_000_000),
		big.NewInt(123),
		"0x000000000000000000000000000000000000dEaD",
	)
	if err != nil {
		t.Fatal(err)
	}
	if tx.ChainId().Cmp(big.NewInt(int64(constants.Ethereum))) != 0 {
		t.Fatalf("chain id = %s", tx.ChainId())
	}
	if tx.Nonce() != 7 {
		t.Fatalf("nonce = %d", tx.Nonce())
	}
	if tx.Value().Cmp(big.NewInt(123)) != 0 {
		t.Fatalf("value = %s", tx.Value())
	}
	if tx.To() == nil || !strings.EqualFold(tx.To().Hex(), "0x000000000000000000000000000000000000dEaD") {
		t.Fatalf("to = %v", tx.To())
	}
}

func TestEVMSignERC20UsesTrustWalletCore(t *testing.T) {
	privateKey, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	contractAddr := "0xdAC17F958D2ee523a2206206994597C13D831ec7"

	tx, err := evmSignERC20WithTrustWallet(
		"ethereum",
		constants.Ethereum,
		privateKey,
		8,
		big.NewInt(1_000_000_000),
		contractAddr,
		big.NewInt(1_250_000),
		"0x000000000000000000000000000000000000dEaD",
	)
	if err != nil {
		t.Fatal(err)
	}
	if tx.To() == nil || *tx.To() != common.HexToAddress(contractAddr) {
		t.Fatalf("to = %v", tx.To())
	}
	if tx.Value().Sign() != 0 {
		t.Fatalf("value = %s", tx.Value())
	}
	if len(tx.Data()) < 4 || hex.EncodeToString(tx.Data()[:4]) != "a9059cbb" {
		t.Fatalf("unexpected ERC20 calldata: %x", tx.Data())
	}
}
