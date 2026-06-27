package chainresource

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
)

var ErrFeePolicyViolation = errors.New("chain fee/resource policy violation")

func ValidateEVMGasPolicy(chain, intent string, gasPriceWei *big.Int, gasLimit uint64) error {
	_ = strings.TrimSpace(chain)
	_ = strings.TrimSpace(intent)
	if gasPriceWei == nil || gasPriceWei.Sign() <= 0 {
		return fmt.Errorf("%w: evm gas price must be positive", ErrFeePolicyViolation)
	}
	if gasLimit == 0 {
		return fmt.Errorf("%w: evm gas limit must be positive", ErrFeePolicyViolation)
	}
	maxGasPrice, err := envBigInt("EVM_MAX_GAS_PRICE_WEI")
	if err != nil {
		return err
	}
	if maxGasPrice != nil && gasPriceWei.Cmp(maxGasPrice) > 0 {
		return fmt.Errorf("%w: evm gas price exceeds configured cap", ErrFeePolicyViolation)
	}
	return nil
}

func BitcoinFeeRateSatPerVByte() (int64, error) {
	min, err := envInt64Default("BITCOIN_MIN_FEE_RATE_SAT_PER_VBYTE", 1)
	if err != nil {
		return 0, err
	}
	max, err := envInt64Default("BITCOIN_MAX_FEE_RATE_SAT_PER_VBYTE", 10_000)
	if err != nil {
		return 0, err
	}
	rate, err := envInt64Default("BITCOIN_FEE_RATE_SAT_PER_VBYTE", 10)
	if err != nil {
		return 0, err
	}
	if min <= 0 || max <= 0 || min > max {
		return 0, fmt.Errorf("%w: invalid bitcoin fee policy bounds", ErrFeePolicyViolation)
	}
	if rate < min {
		return 0, fmt.Errorf("%w: bitcoin fee rate below configured floor", ErrFeePolicyViolation)
	}
	if rate > max {
		return 0, fmt.Errorf("%w: bitcoin fee rate exceeds configured cap", ErrFeePolicyViolation)
	}
	return rate, nil
}

func SolanaTransferFeeLamports() (uint64, error) {
	return envUint64Positive("SOLANA_TRANSFER_FEE_LAMPORTS", 5_000, "solana transfer fee")
}

func TronTRC20FeeLimitSUN() (int64, error) {
	return envInt64Positive("TRON_TRC20_FEE_LIMIT_SUN", 50_000_000, "tron trc20 fee limit")
}

func TronNativeSweepFeeSUN() (int64, error) {
	return envInt64Positive("TRON_NATIVE_SWEEP_FEE_SUN", 1_100_000, "tron native sweep fee")
}

func envBigInt(name string) (*big.Int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok || value.Sign() <= 0 {
		return nil, fmt.Errorf("%w: %s must be a positive integer", ErrFeePolicyViolation, name)
	}
	return value, nil
}

func envInt64Default(name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrFeePolicyViolation, name)
	}
	return value, nil
}

func envInt64Positive(name string, fallback int64, label string) (int64, error) {
	value, err := envInt64Default(name, fallback)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("%w: %s must be positive", ErrFeePolicyViolation, label)
	}
	return value, nil
}

func envUint64Positive(name string, fallback uint64, label string) (uint64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%w: %s must be a positive integer", ErrFeePolicyViolation, label)
	}
	return value, nil
}
