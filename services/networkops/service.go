package networkops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"core/constants"
	"core/models"
)

var (
	ErrDepositsUnavailable    = errors.New("network deposits are unavailable")
	ErrWithdrawalsUnavailable = errors.New("network withdrawals are unavailable")
)

// StateReader is the minimal persisted-state contract needed by operational
// guards. The concrete repository returns an active state when no override has
// been stored for a supported chain.
type StateReader interface {
	GetByChain(context.Context, constants.ChainID) (*models.NetworkOperationalState, error)
}

// UnavailableError carries the public maintenance reason without making
// callers parse an error string to decide on a 503 response or a job deferral.
type UnavailableError struct {
	ChainID constants.ChainID
	Mode    models.NetworkOperationalMode
	Reason  string
	Cause   error
}

func (e *UnavailableError) Error() string {
	if e == nil {
		return "network is temporarily unavailable"
	}
	operation := "operations"
	switch {
	case errors.Is(e.Cause, ErrDepositsUnavailable):
		operation = "deposits"
	case errors.Is(e.Cause, ErrWithdrawalsUnavailable):
		operation = "withdrawals"
	}
	chain := constants.ChainName(e.ChainID)
	if chain == "" {
		chain = fmt.Sprintf("chain %d", e.ChainID)
	}
	message := fmt.Sprintf("%s %s are temporarily unavailable", chain, operation)
	if reason := strings.TrimSpace(e.Reason); reason != "" {
		message += ": " + reason
	}
	return message
}

func (e *UnavailableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// State returns the effective persisted state. A nil reader is treated as
// active for isolated callers/tests; the production composition root always
// wires the repository.
func State(ctx context.Context, reader StateReader, chainID constants.ChainID) (*models.NetworkOperationalState, error) {
	if !constants.IsSupportedChainID(chainID) {
		return nil, fmt.Errorf("%w: %d", models.ErrNetworkOperationalChainUnsupported, chainID)
	}
	if reader == nil {
		return &models.NetworkOperationalState{
			ChainID: chainID,
			Mode:    models.NetworkOperationalModeActive,
		}, nil
	}
	state, err := reader.GetByChain(ctx, chainID)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, errors.New("network operational state repository returned nil state")
	}
	state.Normalize()
	if err := state.Validate(); err != nil {
		return nil, err
	}
	return state, nil
}

func RequireDeposits(ctx context.Context, reader StateReader, chainID constants.ChainID) error {
	state, err := State(ctx, reader, chainID)
	if err != nil {
		return err
	}
	if !state.BlocksDeposits() {
		return nil
	}
	return &UnavailableError{
		ChainID: chainID,
		Mode:    state.Mode,
		Reason:  state.Reason,
		Cause:   ErrDepositsUnavailable,
	}
}

func RequireWithdrawals(ctx context.Context, reader StateReader, chainID constants.ChainID) error {
	state, err := State(ctx, reader, chainID)
	if err != nil {
		return err
	}
	if !state.BlocksWithdrawals() {
		return nil
	}
	return &UnavailableError{
		ChainID: chainID,
		Mode:    state.Mode,
		Reason:  state.Reason,
		Cause:   ErrWithdrawalsUnavailable,
	}
}

func IsUnavailable(err error) bool {
	return errors.Is(err, ErrDepositsUnavailable) || errors.Is(err, ErrWithdrawalsUnavailable)
}
