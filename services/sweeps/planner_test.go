package sweeps

import (
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestPlannerBatchesCompatibleCandidatesWhenChainSupportsIt(t *testing.T) {
	merchantID := uuid.New()
	reserveID := uuid.New()
	now := time.Now()
	planner := NewPlanner(map[constants.ChainID]ChainCapability{
		constants.Bitcoin: {ChainID: constants.Bitcoin, SupportsBatch: true, MaxBatchSize: 10},
	})

	plans, err := planner.Plan([]Candidate{
		testCandidate(merchantID, reserveID, constants.Bitcoin, nil, "1000", "policy:a", now),
		testCandidate(merchantID, reserveID, constants.Bitcoin, nil, "2000", "policy:a", now.Add(time.Second)),
	}, Policy{Key: "treasury", MinimumRaw: "100", DustRaw: "10", MaxFeeRaw: "500", MaxBatchSize: 10})
	if err != nil {
		t.Fatalf("plan sweeps: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %d, want one batch", len(plans))
	}
	if !plans[0].SupportsBatch || len(plans[0].JobIDs) != 2 || plans[0].TotalAmountRaw != "3000" {
		t.Fatalf("batch plan = %#v", plans[0])
	}
}

func TestPlannerSeparatesIncompatiblePolicyAndAsset(t *testing.T) {
	merchantID := uuid.New()
	reserveID := uuid.New()
	token := "0xToken"
	now := time.Now()
	planner := NewPlanner(map[constants.ChainID]ChainCapability{
		constants.Bitcoin: {ChainID: constants.Bitcoin, SupportsBatch: true, MaxBatchSize: 10},
	})

	plans, err := planner.Plan([]Candidate{
		testCandidate(merchantID, reserveID, constants.Bitcoin, nil, "1000", "policy:a", now),
		testCandidate(merchantID, reserveID, constants.Bitcoin, nil, "1000", "policy:b", now),
		testCandidate(merchantID, reserveID, constants.Bitcoin, &token, "1000", "policy:a", now),
	}, Policy{Key: "treasury", MinimumRaw: "100"})
	if err != nil {
		t.Fatalf("plan sweeps: %v", err)
	}
	if len(plans) != 3 {
		t.Fatalf("plans = %d, want three incompatible groups", len(plans))
	}
}

func TestPlannerAppliesDustMinimumAndFeePolicy(t *testing.T) {
	merchantID := uuid.New()
	reserveID := uuid.New()
	now := time.Now()
	planner := NewPlanner(DefaultCapabilities())

	plans, err := planner.Plan([]Candidate{
		testCandidate(merchantID, reserveID, constants.Bitcoin, nil, "5", "policy:a", now),
		testCandidate(merchantID, reserveID, constants.Bitcoin, nil, "50", "policy:a", now),
		testCandidateWithFee(merchantID, reserveID, constants.Bitcoin, "200", "800", "policy:a", now),
	}, Policy{Key: "treasury", DustRaw: "10", MinimumRaw: "100", MaxFeeRaw: "500"})
	if err != nil {
		t.Fatalf("plan sweeps: %v", err)
	}
	if len(plans) != 1 || len(plans[0].JobIDs) != 0 || len(plans[0].Exclusions) != 3 {
		t.Fatalf("policy plan = %#v", plans)
	}
	reasons := map[string]bool{}
	for _, exclusion := range plans[0].Exclusions {
		reasons[exclusion.Reason] = true
	}
	for _, want := range []string{"dust", "below_minimum_balance", "fee_exceeds_max"} {
		if !reasons[want] {
			t.Fatalf("exclusion reasons = %#v, missing %s", reasons, want)
		}
	}
}

func TestPlannerMarksTokenPlanForGasFunding(t *testing.T) {
	merchantID := uuid.New()
	reserveID := uuid.New()
	token := "0xToken"
	now := time.Now()
	planner := NewPlanner(DefaultCapabilities())

	plans, err := planner.Plan([]Candidate{
		{
			Job:                 testSweepJob(merchantID, constants.Ethereum, &token, now),
			AmountRaw:           "1000",
			FeeEstimateRaw:      "10",
			NativeGasBalanceRaw: "1",
			ReserveWalletID:     reserveID,
			SourcePolicyKey:     "policy:a",
			RequiresNativeGas:   true,
		},
	}, Policy{Key: "treasury", MinimumRaw: "100", MinNativeGasRaw: "10"})
	if err != nil {
		t.Fatalf("plan sweeps: %v", err)
	}
	if len(plans) != 1 || !plans[0].RequiresGasFunding {
		t.Fatalf("gas funding plan = %#v", plans)
	}
}

func testCandidate(merchantID, reserveID uuid.UUID, chainID constants.ChainID, token *string, amount string, policyKey string, createdAt time.Time) Candidate {
	return testCandidateWithFee(merchantID, reserveID, chainID, amount, "10", policyKey, createdAt).withToken(token)
}

func testCandidateWithFee(merchantID, reserveID uuid.UUID, chainID constants.ChainID, amount string, fee string, policyKey string, createdAt time.Time) Candidate {
	return Candidate{
		Job:             testSweepJob(merchantID, chainID, nil, createdAt),
		AmountRaw:       amount,
		FeeEstimateRaw:  fee,
		ReserveWalletID: reserveID,
		SourcePolicyKey: policyKey,
	}
}

func (candidate Candidate) withToken(token *string) Candidate {
	candidate.Job.Token = token
	return candidate
}

func testSweepJob(merchantID uuid.UUID, chainID constants.ChainID, token *string, createdAt time.Time) models.SweepJob {
	return models.SweepJob{
		ID:         uuid.New(),
		MerchantID: merchantID,
		WalletID:   uuid.New(),
		ChainID:    chainID,
		Token:      token,
		CreatedAt:  createdAt,
	}
}
