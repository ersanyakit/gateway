package models

import (
	"errors"
	"strings"
	"testing"

	"core/constants"
)

func TestNetworkOperationalStateModeBehavior(t *testing.T) {
	tests := []struct {
		mode              NetworkOperationalMode
		blocksDeposits    bool
		blocksWithdrawals bool
	}{
		{mode: NetworkOperationalModeActive},
		{mode: NetworkOperationalModeDepositsOff, blocksDeposits: true},
		{mode: NetworkOperationalModeWithdrawalsOff, blocksWithdrawals: true},
		{mode: NetworkOperationalModeMaintenance, blocksDeposits: true, blocksWithdrawals: true},
	}

	for _, tc := range tests {
		t.Run(string(tc.mode), func(t *testing.T) {
			state := NetworkOperationalState{ChainID: constants.Ethereum, Mode: tc.mode}
			if err := state.Validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
			if got := state.BlocksDeposits(); got != tc.blocksDeposits {
				t.Fatalf("BlocksDeposits() = %t, want %t", got, tc.blocksDeposits)
			}
			if got := state.BlocksWithdrawals(); got != tc.blocksWithdrawals {
				t.Fatalf("BlocksWithdrawals() = %t, want %t", got, tc.blocksWithdrawals)
			}
		})
	}
}

func TestNetworkOperationalStateNormalizeAndValidate(t *testing.T) {
	state := NetworkOperationalState{
		ChainID:   constants.Base,
		Mode:      "  MAINTENANCE ",
		Reason:    "  provider upgrade  ",
		UpdatedBy: "  ops@example.com  ",
	}
	state.Normalize()
	if state.Mode != NetworkOperationalModeMaintenance {
		t.Fatalf("mode = %q, want maintenance", state.Mode)
	}
	if state.Reason != "provider upgrade" || state.UpdatedBy != "ops@example.com" {
		t.Fatalf("normalized state = %#v", state)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("validate normalized state: %v", err)
	}
}

func TestNetworkOperationalStateRejectsInvalidModeAndChain(t *testing.T) {
	invalidMode := NetworkOperationalState{ChainID: constants.Ethereum, Mode: "paused"}
	if err := invalidMode.Validate(); !errors.Is(err, ErrNetworkOperationalModeInvalid) {
		t.Fatalf("invalid mode error = %v", err)
	}

	invalidChain := NetworkOperationalState{ChainID: constants.ChainID(123456789), Mode: NetworkOperationalModeActive}
	if err := invalidChain.Validate(); !errors.Is(err, ErrNetworkOperationalChainUnsupported) {
		t.Fatalf("invalid chain error = %v", err)
	}

	longReason := NetworkOperationalState{
		ChainID: constants.Ethereum,
		Mode:    NetworkOperationalModeMaintenance,
		Reason:  strings.Repeat("a", 501),
	}
	if err := longReason.Validate(); !errors.Is(err, ErrNetworkOperationalReasonTooLong) {
		t.Fatalf("long reason error = %v", err)
	}
}

func TestNetworkOperationalStateActiveModeClearsStaleReason(t *testing.T) {
	state := NetworkOperationalState{
		ChainID: constants.Ethereum,
		Mode:    " ACTIVE ",
		Reason:  "old maintenance window",
	}
	state.Normalize()
	if state.Reason != "" {
		t.Fatalf("active reason = %q, want empty", state.Reason)
	}
}
