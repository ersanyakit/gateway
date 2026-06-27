package chains

import (
	"context"
	"errors"
	"io"
	"testing"

	"core/blockchain"
	"core/constants"
	"core/services/signer"
)

func TestEVMSendNativeChecksSignerPolicyBeforeRPCAndPrivateKey(t *testing.T) {
	silenceSignerAudit(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "software")

	wallet := blockchain.WalletDetails{
		Address:        "0x1111111111111111111111111111111111111111",
		PrivateKey:     "not-a-private-key",
		KeyReference:   "chain:1:path:m/44'/60'/1'/0/2",
		DerivationPath: "m/44'/60'/1'/0/2",
	}
	_, err := evmWithdrawNative(
		context.Background(),
		"ethereum",
		constants.Ethereum,
		nil,
		wallet,
		"100",
		"0x2222222222222222222222222222222222222222",
	)
	if !errors.Is(err, signer.ErrProductionSoftwareSignerDisabled) {
		t.Fatalf("evm withdraw err=%v, want ErrProductionSoftwareSignerDisabled before RPC/private key", err)
	}
}

func TestBitcoinSendChecksSignerPolicyBeforePrivateKey(t *testing.T) {
	silenceSignerAudit(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "software")

	chain := NewBitcoinChain()
	wallet := blockchain.WalletDetails{
		Address:        "bc1qexample",
		PrivateKey:     "not-a-private-key",
		KeyReference:   "chain:0:path:m/86'/0'/1'/0/2",
		DerivationPath: "m/86'/0'/1'/0/2",
	}
	_, err := chain.sendTo(context.Background(), wallet, "bc1qrecipient", 1000)
	if !errors.Is(err, signer.ErrProductionSoftwareSignerDisabled) {
		t.Fatalf("bitcoin send err=%v, want ErrProductionSoftwareSignerDisabled before private key", err)
	}
}

func TestSolanaWithdrawChecksSignerPolicyBeforeRPCAndPrivateKey(t *testing.T) {
	silenceSignerAudit(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "software")

	chain := NewSolanaChain()
	wallet := blockchain.WalletDetails{
		Address:        "So11111111111111111111111111111111111111112",
		PrivateKey:     "not-a-private-key",
		KeyReference:   "chain:501:path:m/44'/501'/1'/2'",
		DerivationPath: "m/44'/501'/1'/2'",
	}
	_, err := chain.Withdraw(context.Background(), wallet, "42", "So11111111111111111111111111111111111111112")
	if !errors.Is(err, signer.ErrProductionSoftwareSignerDisabled) {
		t.Fatalf("solana withdraw err=%v, want ErrProductionSoftwareSignerDisabled before RPC/private key", err)
	}
}

func TestTronSendChecksSignerPolicyBeforeRPCAndPrivateKey(t *testing.T) {
	silenceSignerAudit(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "software")

	chain := NewTronChain()
	wallet := blockchain.WalletDetails{
		Address:        tronTestAddress,
		PrivateKey:     "not-a-private-key",
		KeyReference:   "chain:728126428:path:m/44'/195'/1'/0/2",
		DerivationPath: "m/44'/195'/1'/0/2",
	}
	_, err := chain.Withdraw(context.Background(), wallet, "1000", tronTestAddress)
	if !errors.Is(err, signer.ErrProductionSoftwareSignerDisabled) {
		t.Fatalf("tron withdraw err=%v, want ErrProductionSoftwareSignerDisabled before RPC/private key", err)
	}
}

func silenceSignerAudit(t *testing.T) {
	t.Helper()
	restore := signer.SetAuditOutput(io.Discard)
	t.Cleanup(restore)
}
