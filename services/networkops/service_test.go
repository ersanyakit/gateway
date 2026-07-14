package networkops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"core/constants"
	"core/models"
)

type staticStateReader struct {
	state *models.NetworkOperationalState
	err   error
}

func (r staticStateReader) GetByChain(context.Context, constants.ChainID) (*models.NetworkOperationalState, error) {
	return r.state, r.err
}

func TestOperationalGuardsRespectModeAndReason(t *testing.T) {
	tests := []struct {
		name              string
		mode              models.NetworkOperationalMode
		depositBlocked    bool
		withdrawalBlocked bool
	}{
		{name: "active", mode: models.NetworkOperationalModeActive},
		{name: "deposits", mode: models.NetworkOperationalModeDepositsOff, depositBlocked: true},
		{name: "withdrawals", mode: models.NetworkOperationalModeWithdrawalsOff, withdrawalBlocked: true},
		{name: "maintenance", mode: models.NetworkOperationalModeMaintenance, depositBlocked: true, withdrawalBlocked: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := staticStateReader{state: &models.NetworkOperationalState{
				ChainID: constants.Ethereum,
				Mode:    tc.mode,
				Reason:  "provider upgrade",
			}}
			depositErr := RequireDeposits(context.Background(), reader, constants.Ethereum)
			if got := errors.Is(depositErr, ErrDepositsUnavailable); got != tc.depositBlocked {
				t.Fatalf("deposit blocked = %v, want %v (err=%v)", got, tc.depositBlocked, depositErr)
			}
			withdrawalErr := RequireWithdrawals(context.Background(), reader, constants.Ethereum)
			if got := errors.Is(withdrawalErr, ErrWithdrawalsUnavailable); got != tc.withdrawalBlocked {
				t.Fatalf("withdrawal blocked = %v, want %v (err=%v)", got, tc.withdrawalBlocked, withdrawalErr)
			}
			if tc.depositBlocked && !strings.Contains(depositErr.Error(), "provider upgrade") {
				t.Fatalf("deposit error must include public reason: %v", depositErr)
			}
		})
	}
}

func TestOperationalGuardPropagatesStateReadFailure(t *testing.T) {
	want := errors.New("database unavailable")
	err := RequireWithdrawals(context.Background(), staticStateReader{err: want}, constants.Ethereum)
	if !errors.Is(err, want) {
		t.Fatalf("RequireWithdrawals error = %v, want %v", err, want)
	}
}

func TestOperationalGuardNilReaderDefaultsActive(t *testing.T) {
	if err := RequireDeposits(context.Background(), nil, constants.Solana); err != nil {
		t.Fatalf("nil reader deposit guard = %v", err)
	}
	if err := RequireWithdrawals(context.Background(), nil, constants.Solana); err != nil {
		t.Fatalf("nil reader withdrawal guard = %v", err)
	}
}
