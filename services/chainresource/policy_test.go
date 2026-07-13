package chainresource

import (
	"math/big"
	"testing"
)

func TestValidateEVMGasPolicyRejectsZeroAndCapExceeded(t *testing.T) {
	t.Setenv("EVM_MAX_GAS_PRICE_WEI", "100")

	if err := ValidateEVMGasPolicy("ethereum", "transfer.native", big.NewInt(0), 21_000); err == nil {
		t.Fatal("expected zero gas price policy failure")
	}
	if err := ValidateEVMGasPolicy("ethereum", "transfer.native", big.NewInt(101), 21_000); err == nil {
		t.Fatal("expected gas price cap policy failure")
	}
	if err := ValidateEVMGasPolicy("ethereum", "transfer.native", big.NewInt(100), 21_000); err != nil {
		t.Fatalf("valid gas policy rejected: %v", err)
	}
}

func TestBitcoinFeeRatePolicyUsesEnvAndRejectsBounds(t *testing.T) {
	t.Setenv("BITCOIN_MIN_FEE_RATE_SAT_PER_VBYTE", "2")
	t.Setenv("BITCOIN_MAX_FEE_RATE_SAT_PER_VBYTE", "20")
	t.Setenv("BITCOIN_FEE_RATE_SAT_PER_VBYTE", "21")
	if _, err := BitcoinFeeRateSatPerVByte(); err == nil {
		t.Fatal("expected fee cap policy failure")
	}

	t.Setenv("BITCOIN_FEE_RATE_SAT_PER_VBYTE", "1")
	if _, err := BitcoinFeeRateSatPerVByte(); err == nil {
		t.Fatal("expected fee floor policy failure")
	}

	t.Setenv("BITCOIN_FEE_RATE_SAT_PER_VBYTE", "12")
	got, err := BitcoinFeeRateSatPerVByte()
	if err != nil {
		t.Fatalf("valid fee rate rejected: %v", err)
	}
	if got != 12 {
		t.Fatalf("fee rate = %d, want 12", got)
	}
}

func TestSolanaAndTronPolicyRejectInvalidResourceFees(t *testing.T) {
	t.Setenv("SOLANA_TRANSFER_FEE_LAMPORTS", "0")
	if _, err := SolanaTransferFeeLamports(); err == nil {
		t.Fatal("expected invalid solana fee policy failure")
	}

	t.Setenv("TRON_TRC20_FEE_LIMIT_SUN", "0")
	if _, err := TronTRC20FeeLimitSUN(); err == nil {
		t.Fatal("expected invalid tron fee limit policy failure")
	}

	t.Setenv("TRON_NATIVE_SWEEP_FEE_SUN", "-1")
	if _, err := TronNativeSweepFeeSUN(); err == nil {
		t.Fatal("expected invalid tron sweep fee policy failure")
	}
}
