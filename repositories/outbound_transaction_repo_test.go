package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestOutboundTransactionRepoCreateAndClaimDueAreIdempotent(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)

	params := outboundTransactionRepoTestParams(models.OutboundResourceWithdrawal, constants.Ethereum)
	first, created, err := repo.Create(ctx, params)
	if err != nil {
		t.Fatalf("create outbound: %v", err)
	}
	if !created {
		t.Fatal("first create should create a row")
	}
	if first.Status != models.OutboundStatusPrepared || first.Symbol != "ETH" {
		t.Fatalf("created outbound = %#v, want prepared ETH row", first)
	}

	params.AmountRaw = "999"
	second, created, err := repo.Create(ctx, params)
	if err != nil {
		t.Fatalf("idempotent create: %v", err)
	}
	if created {
		t.Fatal("second create with same idempotency key must return existing row")
	}
	if second.ID != first.ID || second.AmountRaw != "1000000000000000000" {
		t.Fatalf("idempotent create returned %#v, want original row %s", second, first.ID)
	}

	claimed, err := repo.ClaimDue(ctx, []string{models.OutboundResourceWithdrawal}, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != first.ID || claimed[0].LockedUntil == nil {
		t.Fatalf("claimed = %#v, want locked original outbound", claimed)
	}

	claimedAgain, err := repo.ClaimDue(ctx, []string{models.OutboundResourceWithdrawal}, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim locked row again: %v", err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("locked outbound should not be claimed again, got %#v", claimedAgain)
	}
}

func TestOutboundTransactionRepoDefersForNetworkStateWithoutConsumingAttempt(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)
	outbound := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)

	claimed, err := repo.ClaimDue(ctx, []string{models.OutboundResourceWithdrawal}, 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim due = %#v, err=%v", claimed, err)
	}
	if err := repo.DeferForNetworkState(ctx, outbound.ID, "provider upgrade", time.Minute); err != nil {
		t.Fatalf("defer for network state: %v", err)
	}

	var reloaded models.OutboundTransaction
	if err := db.WithContext(ctx).First(&reloaded, "id = ?", outbound.ID).Error; err != nil {
		t.Fatalf("reload outbound: %v", err)
	}
	if reloaded.Status != models.OutboundStatusPrepared || reloaded.Attempts != 0 || reloaded.LockedUntil != nil || reloaded.NextRunAt == nil || !reloaded.NextRunAt.After(time.Now()) {
		t.Fatalf("deferred outbound state = %#v", reloaded)
	}
	if reloaded.ErrorCategory != "network_maintenance" || reloaded.ErrorDetail != "provider upgrade" {
		t.Fatalf("deferred outbound error metadata = %q/%q", reloaded.ErrorCategory, reloaded.ErrorDetail)
	}
}

func TestOutboundTransactionRepoSequenceReservationReleasesAndConsumes(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)
	sourceAddress := "0xreserve"

	first := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	second := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceRefund, constants.Ethereum)
	third := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceSweepJob, constants.Ethereum)

	firstReservation, created, err := repo.ReserveSequence(ctx, first, sourceAddress, time.Minute)
	if err != nil {
		t.Fatalf("reserve first sequence: %v", err)
	}
	if !created || firstReservation.Status != models.OutboundResourceReservationReserved {
		t.Fatalf("first reservation = %#v, created=%v", firstReservation, created)
	}

	replayed, created, err := repo.ReserveSequence(ctx, first, sourceAddress, time.Minute)
	if err != nil {
		t.Fatalf("replay first sequence reservation: %v", err)
	}
	if created || replayed.ID != firstReservation.ID {
		t.Fatalf("same outbound replay = %#v created=%v, want existing %s", replayed, created, firstReservation.ID)
	}

	if _, _, err := repo.ReserveSequence(ctx, second, sourceAddress, time.Minute); !errors.Is(err, ErrOutboundResourceAlreadyReserved) {
		t.Fatalf("competing sequence reservation err = %v, want ErrOutboundResourceAlreadyReserved", err)
	}
	if err := repo.ReleaseResource(ctx, firstReservation.ID); err != nil {
		t.Fatalf("release first sequence: %v", err)
	}

	secondReservation, created, err := repo.ReserveSequence(ctx, second, sourceAddress, time.Minute)
	if err != nil {
		t.Fatalf("reserve second sequence after release: %v", err)
	}
	if !created || secondReservation.ID == firstReservation.ID {
		t.Fatalf("second reservation = %#v created=%v, want new reservation", secondReservation, created)
	}
	if err := repo.ConsumeResource(ctx, secondReservation.ID, "0xtxhash"); err != nil {
		t.Fatalf("consume second sequence: %v", err)
	}

	thirdReservation, created, err := repo.ReserveSequence(ctx, third, sourceAddress, time.Minute)
	if err != nil {
		t.Fatalf("reserve sequence after consumed sequence: %v", err)
	}
	if !created || thirdReservation.ID == secondReservation.ID {
		t.Fatalf("third reservation = %#v created=%v, want consumed sequence not to block", thirdReservation, created)
	}
}

func TestOutboundTransactionRepoUTXOConsumptionBlocksReuse(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)

	first := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Bitcoin)
	second := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceRefund, constants.Bitcoin)
	vout := uint32(1)

	firstReservation, created, err := repo.ReserveResource(ctx, OutboundResourceReservationRequest{
		OutboundTransactionID: first.ID,
		ResourceType:          models.OutboundResourceReservationUTXO,
		ChainID:               constants.Bitcoin,
		WalletID:              first.WalletID,
		WalletAddress:         "bc1qsource",
		OwnerType:             first.ResourceType,
		OwnerID:               first.ResourceID,
		Intent:                "withdrawal:broadcast",
		UTXOTxID:              "0xabc",
		UTXOVout:              &vout,
		UTXOValueRaw:          "1000",
		LeaseFor:              time.Minute,
	})
	if err != nil {
		t.Fatalf("reserve first utxo: %v", err)
	}
	if !created {
		t.Fatal("first UTXO reservation should create a row")
	}
	if err := repo.ConsumeResource(ctx, firstReservation.ID, "0xspend"); err != nil {
		t.Fatalf("consume first utxo: %v", err)
	}

	_, _, err = repo.ReserveResource(ctx, OutboundResourceReservationRequest{
		OutboundTransactionID: second.ID,
		ResourceType:          models.OutboundResourceReservationUTXO,
		ChainID:               constants.Bitcoin,
		WalletID:              second.WalletID,
		WalletAddress:         "bc1qsource",
		OwnerType:             second.ResourceType,
		OwnerID:               second.ResourceID,
		Intent:                "refund:broadcast",
		UTXOTxID:              "0xabc",
		UTXOVout:              &vout,
		UTXOValueRaw:          "1000",
		LeaseFor:              time.Minute,
	})
	if !errors.Is(err, ErrOutboundResourceAlreadyReserved) {
		t.Fatalf("consumed UTXO reuse err = %v, want ErrOutboundResourceAlreadyReserved", err)
	}
}

func TestOutboundTransactionRepoResourceTerminalTransitions(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)

	finalized := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	if err := repo.MarkBroadcasted(ctx, finalized.ID, "0xfinal"); err != nil {
		t.Fatalf("mark broadcasted: %v", err)
	}
	if err := repo.MarkResourceFinalized(ctx, finalized.ResourceType, finalized.ResourceID); err != nil {
		t.Fatalf("mark resource finalized: %v", err)
	}
	var finalizedRow models.OutboundTransaction
	if err := db.WithContext(ctx).First(&finalizedRow, "id = ?", finalized.ID).Error; err != nil {
		t.Fatalf("reload finalized row: %v", err)
	}
	if finalizedRow.Status != models.OutboundStatusFinalized || finalizedRow.FinalizedAt == nil || finalizedRow.NextRunAt != nil || finalizedRow.LockedUntil != nil {
		t.Fatalf("finalized row = %#v, want terminal finalized without retry/lock", finalizedRow)
	}

	failed := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceRefund, constants.Ethereum)
	if err := repo.MarkBroadcasted(ctx, failed.ID, "0xfailed"); err != nil {
		t.Fatalf("mark failed candidate broadcasted: %v", err)
	}
	if err := repo.MarkResourceTerminalFailed(ctx, failed.ResourceType, failed.ResourceID, errors.New("chain terminal failure")); err != nil {
		t.Fatalf("mark resource terminal failed: %v", err)
	}
	var failedRow models.OutboundTransaction
	if err := db.WithContext(ctx).First(&failedRow, "id = ?", failed.ID).Error; err != nil {
		t.Fatalf("reload failed row: %v", err)
	}
	if failedRow.Status != models.OutboundStatusFailed || failedRow.FinalizedAt == nil || failedRow.ErrorCategory != "terminal_failed" || failedRow.NextRunAt != nil || failedRow.LockedUntil != nil {
		t.Fatalf("failed row = %#v, want terminal failed without retry/lock", failedRow)
	}
}

func createOutboundTransactionForTest(t *testing.T, repo *OutboundTransactionRepo, ctx context.Context, resourceType string, chainID constants.ChainID) models.OutboundTransaction {
	t.Helper()
	params := outboundTransactionRepoTestParams(resourceType, chainID)
	params.IdempotencyKey += ":" + uuid.NewString()
	outbound, _, err := repo.Create(ctx, params)
	if err != nil {
		t.Fatalf("create outbound %s: %v", resourceType, err)
	}
	return *outbound
}

func outboundTransactionRepoTestParams(resourceType string, chainID constants.ChainID) OutboundTransactionCreate {
	domainID := uuid.New()
	symbol := "ETH"
	decimals := uint8(18)
	amount := "1000000000000000000"
	toAddress := "0xdestination"
	if chainID == constants.Bitcoin {
		symbol = "BTC"
		decimals = 8
		amount = "100000"
		toAddress = "bc1qdestination"
	}
	return OutboundTransactionCreate{
		IdempotencyKey: "outbound-test:" + resourceType + ":" + uuid.NewString(),
		ResourceType:   resourceType,
		ResourceID:     uuid.New(),
		MerchantID:     uuid.New(),
		DomainID:       &domainID,
		WalletID:       uuid.New(),
		ChainID:        chainID,
		ChainName:      constants.ChainName(chainID),
		Symbol:         symbol,
		Decimals:       decimals,
		AmountRaw:      amount,
		ToAddress:      toAddress,
		ActorID:        "admin@example.com",
		CorrelationID:  "corr-" + uuid.NewString(),
	}
}
