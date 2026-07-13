package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestReverseLedgerDirection(t *testing.T) {
	if got := reverseLedgerDirection(models.LedgerDirectionCredit); got != models.LedgerDirectionDebit {
		t.Fatalf("reverse credit = %q, want %q", got, models.LedgerDirectionDebit)
	}
	if got := reverseLedgerDirection(models.LedgerDirectionDebit); got != models.LedgerDirectionCredit {
		t.Fatalf("reverse debit = %q, want %q", got, models.LedgerDirectionCredit)
	}
}

func TestLedgerRefundAssetFromSessionRequiresSelectedChain(t *testing.T) {
	_, _, _, err := ledgerRefundAssetFromSession(models.PaymentSession{})
	if err == nil {
		t.Fatal("missing selected chain should fail")
	}
}

func TestLedgerRefundAssetFromSessionNormalizesSymbol(t *testing.T) {
	chainID := constants.Ethereum
	_, symbol, decimals, err := ledgerRefundAssetFromSession(models.PaymentSession{
		SelectedChainID:  &chainID,
		SelectedSymbol:   " usdc ",
		SelectedDecimals: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if symbol != "USDC" {
		t.Fatalf("symbol = %q, want USDC", symbol)
	}
	if decimals != 6 {
		t.Fatalf("decimals = %d, want 6", decimals)
	}
}

func TestLedgerRefundAssetFromSessionFallsBackToChainName(t *testing.T) {
	chainID := constants.Bitcoin
	_, symbol, _, err := ledgerRefundAssetFromSession(models.PaymentSession{SelectedChainID: &chainID})
	if err != nil {
		t.Fatal(err)
	}
	if symbol != "BITCOIN" {
		t.Fatalf("symbol = %q, want BITCOIN", symbol)
	}
}

func TestLedgerIdempotencyKeys(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	cases := map[string]string{
		withdrawalHoldKey(id):    "withdrawal-hold:11111111-1111-1111-1111-111111111111",
		withdrawalReleaseKey(id): "withdrawal-release:11111111-1111-1111-1111-111111111111",
		withdrawalDebitKey(id):   "withdrawal-debit:11111111-1111-1111-1111-111111111111",
		refundHoldKey(id):        "refund-hold:11111111-1111-1111-1111-111111111111",
		refundReleaseKey(id):     "refund-release:11111111-1111-1111-1111-111111111111",
		refundDebitKey(id):       "refund-debit:11111111-1111-1111-1111-111111111111",
		sweepHoldKey(id):         "sweep-hold:11111111-1111-1111-1111-111111111111",
		sweepHoldReleaseKey(id):  "sweep-hold-release:11111111-1111-1111-1111-111111111111",
		sweepReleaseKey(id):      "sweep-release:11111111-1111-1111-1111-111111111111",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("key = %q, want %q", got, want)
		}
	}
}

func TestInsufficientAvailableBalanceErrorIsComparable(t *testing.T) {
	err := errors.Join(ErrInsufficientAvailableBalance)
	if !errors.Is(err, ErrInsufficientAvailableBalance) {
		t.Fatal("insufficient balance sentinel should be comparable")
	}
}

func TestLedgerPostingFunctionsUseStableIdempotencyGuards(t *testing.T) {
	sourceBytes, err := os.ReadFile("ledger_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	cases := map[string][]string{
		"CreateDepositPending": {
			"deposit-pending:",
			"r.exists(ctx, key)",
		},
		"PostDepositAvailable": {
			"deposit-available:",
			"r.exists(ctx, key)",
		},
		"PostStandaloneDepositAvailable": {
			"deposit-standalone-available:",
			"r.existsWithDB(ctx, dbtx, key)",
		},
		"createWithdrawalHold": {
			"withdrawalHoldKey",
			"r.existsWithDB(ctx, tx, key)",
		},
		"postWithdrawalDebit": {
			"withdrawalDebitKey",
			"r.existsWithDB(ctx, tx, key)",
		},
		"createRefundHold": {
			"refundHoldKey",
			"r.existsWithDB(ctx, tx, key)",
		},
		"PostRefundDebitWithDB": {
			"refundDebitKey",
			"r.existsWithDB(ctx, tx, key)",
		},
		"createSweepHold": {
			"sweepHoldKey",
			"r.existsWithDB(ctx, tx, key)",
		},
		"PostSweepReleaseWithDB": {
			"sweepReleaseKey",
			"r.existsWithDB(ctx, tx, key)",
		},
	}
	for functionName, required := range cases {
		body := extractLedgerFunctionBody(t, source, functionName)
		for _, token := range required {
			if !strings.Contains(body, token) {
				t.Fatalf("%s missing idempotency guard token %q", functionName, token)
			}
		}
	}
}

func TestLedgerBalanceQueriesExcludeVoidedAndNonNumericRows(t *testing.T) {
	sourceBytes, err := os.ReadFile("ledger_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	for _, functionName := range []string{"MerchantBalances", "PlatformBalances", "DomainBalances", "WalletBalances", "WalletBalancesByWalletIDs", "WalletIDsWithPositiveAvailableBalance"} {
		body := extractLedgerFunctionBody(t, source, functionName)
		for _, token := range []string{
			"status IN ('pending', 'posted')",
			"amount_raw ~ '^[0-9]+$'",
			"CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END",
		} {
			if !strings.Contains(body, token) {
				t.Fatalf("%s missing ledger balance query guard %q", functionName, token)
			}
		}
	}
}

func TestLedgerBalanceQueriesPreserveExpectedTenantScope(t *testing.T) {
	sourceBytes, err := os.ReadFile("ledger_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)

	merchantBody := extractLedgerFunctionBody(t, source, "MerchantBalances")
	for _, token := range []string{
		"WHERE merchant_id = ?",
		"GROUP BY merchant_id, chain_id, token, symbol, decimals, account",
	} {
		if !strings.Contains(merchantBody, token) {
			t.Fatalf("MerchantBalances missing merchant aggregate token %q", token)
		}
	}
	if strings.Contains(merchantBody, "GROUP BY merchant_id, domain_id") {
		t.Fatal("MerchantBalances must aggregate merchant balances across domains")
	}

	platformBody := extractLedgerFunctionBody(t, source, "PlatformBalances")
	for _, token := range []string{
		"account IN ('merchant_pending', 'merchant_available', 'withdrawal_transit', 'refund_transit', 'sweep_transit')",
		"GROUP BY chain_id, token, symbol, decimals, account",
	} {
		if !strings.Contains(platformBody, token) {
			t.Fatalf("PlatformBalances missing platform aggregate token %q", token)
		}
	}
	if strings.Contains(platformBody, "merchant_id") || strings.Contains(platformBody, "platform_clearing") {
		t.Fatal("PlatformBalances must aggregate across merchants and exclude platform clearing")
	}

	for _, tc := range []struct {
		name     string
		required []string
	}{
		{
			name: "DomainBalances",
			required: []string{
				"SELECT merchant_id,\n\t\t       domain_id,",
				"WHERE merchant_id = ?\n\t\t  AND domain_id = ?",
				"GROUP BY merchant_id, domain_id, chain_id, token, symbol, decimals, account",
			},
		},
		{
			name: "WalletBalances",
			required: []string{
				"SELECT merchant_id,\n\t\t       domain_id,\n\t\t       wallet_id,",
				"WHERE merchant_id = ?\n\t\t  AND domain_id = ?\n\t\t  AND wallet_id = ?",
				"GROUP BY merchant_id, domain_id, wallet_id, chain_id, token, symbol, decimals, account",
			},
		},
		{
			name: "WalletBalancesByWalletIDs",
			required: []string{
				"SELECT merchant_id,\n\t\t       domain_id,\n\t\t       wallet_id,",
				"WHERE wallet_id IN ?",
				"GROUP BY merchant_id, domain_id, wallet_id, chain_id, token, symbol, decimals, account",
			},
		},
	} {
		body := extractLedgerFunctionBody(t, source, tc.name)
		for _, token := range tc.required {
			if !strings.Contains(body, token) {
				t.Fatalf("%s missing scoped ledger query token %q", tc.name, token)
			}
		}
	}
}

func TestLedgerEntryAppendOnlyMutationsAreRejected(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	row := testLedgerEntry("append-only-"+uuid.NewString(), uuid.New(), nil, nil, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "1")
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("seed ledger row: %v", err)
	}

	err := db.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("id = ?", row.ID).
		Update("status", models.LedgerStatusVoided).Error
	if !errors.Is(err, models.ErrLedgerEntryAppendOnly) {
		t.Fatalf("ledger update err = %v, want ErrLedgerEntryAppendOnly", err)
	}
	err = db.WithContext(ctx).Delete(&row).Error
	if !errors.Is(err, models.ErrLedgerEntryAppendOnly) {
		t.Fatalf("ledger delete err = %v, want ErrLedgerEntryAppendOnly", err)
	}
	if err := db.WithContext(ctx).
		Set(models.LedgerEntryMutationContextKey, true).
		Model(&models.LedgerEntry{}).
		Where("id = ?", row.ID).
		Update("description", "migration repair").Error; err != nil {
		t.Fatalf("explicit migration-context ledger update: %v", err)
	}
}

func TestLedgerEntryGORMConstraintsCreated(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	for _, name := range []string{
		"ledger_entries_entry_type_check",
		"ledger_entries_account_check",
		"ledger_entries_direction_check",
		"ledger_entries_status_check",
	} {
		if !db.Migrator().HasConstraint(&models.LedgerEntry{}, name) {
			t.Fatalf("ledger_entries constraint %s was not created", name)
		}
	}
}

func TestLedgerRepoWalletBalancesByWalletIDsAggregatesLedgerAccounts(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	otherWalletID := uuid.New()
	prefix := "wallet-balance-test-" + uuid.NewString()

	entries := []models.LedgerEntry{
		testLedgerEntryWithType(prefix+":available-credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "100"),
		testLedgerEntryWithType(prefix+":available-debit", merchantID, &domainID, &walletID, models.LedgerEntryTypeWithdrawalHold, models.LedgerAccountMerchantAvailable, models.LedgerDirectionDebit, models.LedgerStatusPending, "25"),
		testLedgerEntryWithType(prefix+":pending-credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositPending, models.LedgerAccountMerchantPending, models.LedgerDirectionCredit, models.LedgerStatusPending, "50"),
		testLedgerEntryWithType(prefix+":withdrawal-transit-credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeWithdrawalHold, models.LedgerAccountWithdrawalTransit, models.LedgerDirectionCredit, models.LedgerStatusPending, "25"),
		testLedgerEntryWithType(prefix+":refund-transit-credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeRefundHold, models.LedgerAccountRefundTransit, models.LedgerDirectionCredit, models.LedgerStatusPending, "13"),
		testLedgerEntryWithType(prefix+":sweep-transit-credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeSweepHold, models.LedgerAccountSweepTransit, models.LedgerDirectionCredit, models.LedgerStatusPending, "8"),
		testLedgerEntryWithType(prefix+":reversal-debit", merchantID, &domainID, &walletID, models.LedgerEntryTypeReorgReversal, models.LedgerAccountMerchantAvailable, models.LedgerDirectionDebit, models.LedgerStatusPosted, "5"),
		testLedgerEntryWithType(prefix+":adjustment-credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeAdjustment, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "3"),
		testLedgerEntryWithType(prefix+":voided-credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeAdjustment, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusVoided, "999"),
		testLedgerEntryWithType(prefix+":nonnumeric-credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeAdjustment, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "not-a-number"),
		testLedgerEntry(prefix+":other-wallet", merchantID, &domainID, &otherWalletID, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "999"),
	}
	if err := db.WithContext(ctx).Create(&entries).Error; err != nil {
		t.Fatalf("seed ledger entries: %v", err)
	}

	rows, err := repo.WalletBalancesByWalletIDs(ctx, []uuid.UUID{walletID})
	if err != nil {
		t.Fatal(err)
	}
	got := ledgerRowsByAccount(rows)
	if got[models.LedgerAccountMerchantAvailable] != "73" {
		t.Fatalf("available = %q, want 73 rows=%#v", got[models.LedgerAccountMerchantAvailable], rows)
	}
	if got[models.LedgerAccountMerchantPending] != "50" {
		t.Fatalf("pending = %q, want 50 rows=%#v", got[models.LedgerAccountMerchantPending], rows)
	}
	if got[models.LedgerAccountWithdrawalTransit] != "25" {
		t.Fatalf("transit = %q, want 25 rows=%#v", got[models.LedgerAccountWithdrawalTransit], rows)
	}
	if got[models.LedgerAccountRefundTransit] != "13" {
		t.Fatalf("refund transit = %q, want 13 rows=%#v", got[models.LedgerAccountRefundTransit], rows)
	}
	if got[models.LedgerAccountSweepTransit] != "8" {
		t.Fatalf("sweep transit = %q, want 8 rows=%#v", got[models.LedgerAccountSweepTransit], rows)
	}
	for _, row := range rows {
		if row.WalletID == nil || *row.WalletID != walletID {
			t.Fatalf("unexpected wallet row: %#v", row)
		}
	}
}

func TestLedgerRepoWalletIDsWithPositiveAvailableBalanceFiltersAssetAndPaginates(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	highWalletID := uuid.New()
	lowWalletID := uuid.New()
	lockedWalletID := uuid.New()
	zeroWalletID := uuid.New()
	tokenWalletID := uuid.New()
	voidedWalletID := uuid.New()
	token := "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"
	prefix := "positive-wallet-filter-" + uuid.NewString()

	tokenRow := testLedgerEntryWithType(prefix+":token-credit", merchantID, &domainID, &tokenWalletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "1000")
	tokenRow.Token = &token
	tokenRow.Symbol = "USDC"
	tokenRow.Decimals = 6
	entries := []models.LedgerEntry{
		testLedgerEntryWithType(prefix+":native-high", merchantID, &domainID, &highWalletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "200"),
		testLedgerEntryWithType(prefix+":native-low", merchantID, &domainID, &lowWalletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "75"),
		testLedgerEntryWithType(prefix+":locked-only", merchantID, &domainID, &lockedWalletID, models.LedgerEntryTypeSweepHold, models.LedgerAccountSweepTransit, models.LedgerDirectionCredit, models.LedgerStatusPending, "25"),
		testLedgerEntryWithType(prefix+":zero-credit", merchantID, &domainID, &zeroWalletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "50"),
		testLedgerEntryWithType(prefix+":zero-debit", merchantID, &domainID, &zeroWalletID, models.LedgerEntryTypeWithdrawalHold, models.LedgerAccountMerchantAvailable, models.LedgerDirectionDebit, models.LedgerStatusPending, "50"),
		testLedgerEntryWithType(prefix+":voided", merchantID, &domainID, &voidedWalletID, models.LedgerEntryTypeAdjustment, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusVoided, "999"),
		tokenRow,
	}
	if err := db.WithContext(ctx).Create(&entries).Error; err != nil {
		t.Fatalf("seed ledger entries: %v", err)
	}

	ids, total, err := repo.WalletIDsWithPositiveAvailableBalance(ctx, constants.Ethereum, nil, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(ids) != 1 || ids[0] != highWalletID {
		t.Fatalf("native page 1 ids=%v total=%d, want [%s] total=3", ids, total, highWalletID)
	}
	ids, total, err = repo.WalletIDsWithPositiveAvailableBalance(ctx, constants.Ethereum, nil, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(ids) != 1 || ids[0] != lowWalletID {
		t.Fatalf("native page 2 ids=%v total=%d, want [%s] total=3", ids, total, lowWalletID)
	}
	ids, total, err = repo.WalletIDsWithPositiveAvailableBalance(ctx, constants.Ethereum, nil, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(ids) != 1 || ids[0] != lockedWalletID {
		t.Fatalf("native page 3 ids=%v total=%d, want [%s] total=3", ids, total, lockedWalletID)
	}

	ids, total, err = repo.WalletIDsWithPositiveAvailableBalance(ctx, constants.Ethereum, &token, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(ids) != 1 || ids[0] != tokenWalletID {
		t.Fatalf("token ids=%v total=%d, want [%s] total=1", ids, total, tokenWalletID)
	}
}

func TestLedgerRepoDomainAndWalletBalancesPreserveScopeAndAssetMetadata(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	token := "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"
	prefix := "wallet-scope-test-" + uuid.NewString()
	row := testLedgerEntry(prefix+":usdc", merchantID, &domainID, &walletID, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "123456")
	row.Token = &token
	row.Symbol = "USDC"
	row.Decimals = 6
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("seed scoped ledger row: %v", err)
	}

	domainRows, err := repo.DomainBalances(ctx, merchantID, domainID)
	if err != nil {
		t.Fatal(err)
	}
	if len(domainRows) != 1 || domainRows[0].DomainID == nil || *domainRows[0].DomainID != domainID {
		t.Fatalf("domain balance scope = %#v", domainRows)
	}
	if domainRows[0].Token == nil || *domainRows[0].Token != token || domainRows[0].Symbol != "USDC" || domainRows[0].Decimals != 6 {
		t.Fatalf("domain balance asset metadata = %#v", domainRows[0])
	}

	walletRows, err := repo.WalletBalances(ctx, merchantID, domainID, walletID)
	if err != nil {
		t.Fatal(err)
	}
	if len(walletRows) != 1 || walletRows[0].DomainID == nil || *walletRows[0].DomainID != domainID || walletRows[0].WalletID == nil || *walletRows[0].WalletID != walletID {
		t.Fatalf("wallet balance scope = %#v", walletRows)
	}
}

func TestLedgerRepoPostingIdempotencyAndNegativeBalanceGuard(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	prefix := "posting-idempotency-test-" + uuid.NewString()

	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	if err := repo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post standalone deposit: %v", err)
	}
	if err := repo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("duplicate standalone deposit: %v", err)
	}
	requireLedgerCount(t, db, 2, "idempotency_key = ?", "deposit-standalone-available:"+depositTx.UniqueHash)

	withdrawal := models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   &domainID,
		WalletID:   walletID,
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		AmountRaw:  "20",
		Status:     models.WithdrawalStatusApproved,
	}
	if err := repo.CreateWithdrawalHold(ctx, withdrawal); err != nil {
		t.Fatalf("create withdrawal hold: %v", err)
	}
	if err := repo.CreateWithdrawalHold(ctx, withdrawal); err != nil {
		t.Fatalf("duplicate withdrawal hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "idempotency_key = ?", withdrawalHoldKey(withdrawal.ID))
	if err := repo.PostWithdrawalDebit(ctx, withdrawal, prefix+":withdrawal-tx"); err != nil {
		t.Fatalf("post withdrawal debit: %v", err)
	}
	if err := repo.PostWithdrawalDebit(ctx, withdrawal, prefix+":withdrawal-tx"); err != nil {
		t.Fatalf("duplicate withdrawal debit: %v", err)
	}
	requireLedgerCount(t, db, 2, "idempotency_key = ?", withdrawalDebitKey(withdrawal.ID))
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return NewLedgerRepo(tx).RequireWithdrawalHoldForRequestWithDB(ctx, tx, withdrawal)
	})
	if !errors.Is(err, ErrLedgerReservationRequired) {
		t.Fatalf("consumed withdrawal hold requirement err = %v, want ErrLedgerReservationRequired", err)
	}
	if err := repo.VoidWithdrawalHold(ctx, withdrawal.ID); err != nil {
		t.Fatalf("void consumed withdrawal hold should no-op: %v", err)
	}
	requireLedgerCount(t, db, 0, "idempotency_key = ?", withdrawalReleaseKey(withdrawal.ID))

	chainID := constants.Ethereum
	session := models.PaymentSession{
		ID:               uuid.New(),
		MerchantID:       merchantID,
		DomainID:         domainID,
		WalletID:         walletID,
		SelectedChainID:  &chainID,
		SelectedSymbol:   "ETH",
		SelectedDecimals: 18,
	}
	refund := models.Refund{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   domainID,
		PaymentID:  session.ID,
		AmountRaw:  "10",
		Status:     models.RefundStatusApproved,
	}
	if err := repo.CreateRefundHold(ctx, refund, session); err != nil {
		t.Fatalf("create refund hold: %v", err)
	}
	if err := repo.CreateRefundHold(ctx, refund, session); err != nil {
		t.Fatalf("duplicate refund hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "idempotency_key = ?", refundHoldKey(refund.ID))
	if err := repo.PostRefundDebit(ctx, refund, session, prefix+":refund-tx"); err != nil {
		t.Fatalf("post refund debit: %v", err)
	}
	if err := repo.PostRefundDebit(ctx, refund, session, prefix+":refund-tx"); err != nil {
		t.Fatalf("duplicate refund debit: %v", err)
	}
	requireLedgerCount(t, db, 2, "idempotency_key = ?", refundDebitKey(refund.ID))
	if err := repo.VoidRefundHold(ctx, refund.ID); err != nil {
		t.Fatalf("void consumed refund hold should no-op: %v", err)
	}
	requireLedgerCount(t, db, 0, "idempotency_key = ?", refundReleaseKey(refund.ID))

	reversalTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":reversal", "7")
	originalCredit := testLedgerEntryWithType(prefix+":original-credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "7")
	originalDebit := testLedgerEntryWithType(prefix+":original-debit", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountPlatformClearing, models.LedgerDirectionDebit, models.LedgerStatusPosted, "7")
	originalCredit.TransactionUniqueHash = reversalTx.UniqueHash
	originalCredit.TransactionHash = reversalTx.Hash
	originalDebit.TransactionUniqueHash = reversalTx.UniqueHash
	originalDebit.TransactionHash = reversalTx.Hash
	if err := db.WithContext(ctx).Create(&[]models.LedgerEntry{originalCredit, originalDebit}).Error; err != nil {
		t.Fatalf("seed reversal originals: %v", err)
	}
	if err := repo.PostTransactionReversal(ctx, reversalTx); err != nil {
		t.Fatalf("post transaction reversal: %v", err)
	}
	if err := repo.PostTransactionReversal(ctx, reversalTx); err != nil {
		t.Fatalf("duplicate transaction reversal: %v", err)
	}
	requireLedgerCount(t, db, 1, "entry_type = ? AND reference = ?", models.LedgerEntryTypeReorgReversal, originalCredit.ID.String())
	requireLedgerCount(t, db, 1, "entry_type = ? AND reference = ?", models.LedgerEntryTypeReorgReversal, originalDebit.ID.String())

	adjustment := testLedgerEntryWithType(prefix+":adjustment", merchantID, &domainID, &walletID, models.LedgerEntryTypeAdjustment, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "1")
	if err := db.WithContext(ctx).Create(&adjustment).Error; err != nil {
		t.Fatalf("seed adjustment: %v", err)
	}
	duplicateAdjustment := adjustment
	duplicateAdjustment.ID = uuid.New()
	if err := db.WithContext(ctx).Create(&duplicateAdjustment).Error; err == nil {
		t.Fatal("duplicate adjustment idempotency key/account should be rejected by GORM-owned unique index")
	}

	poorWithdrawal := models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: uuid.New(),
		DomainID:   &domainID,
		WalletID:   uuid.New(),
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		AmountRaw:  "1",
		Status:     models.WithdrawalStatusApproved,
	}
	err = repo.CreateWithdrawalHold(ctx, poorWithdrawal)
	if !errors.Is(err, ErrInsufficientAvailableBalance) {
		t.Fatalf("poor withdrawal err = %v, want ErrInsufficientAvailableBalance", err)
	}
	requireLedgerCount(t, db, 0, "idempotency_key = ?", withdrawalHoldKey(poorWithdrawal.ID))
}

func TestLedgerRepoSweepHoldIsIdempotentAndReleasable(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	prefix := "sweep-hold-test-" + uuid.NewString()

	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	if err := repo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post standalone deposit: %v", err)
	}
	job := models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: depositTx.UniqueHash,
		TransactionHash:       depositTx.Hash,
		WalletID:              walletID,
		MerchantID:            merchantID,
		ChainID:               depositTx.ChainID,
		Token:                 depositTx.Token,
		Status:                models.SweepJobStatusProcessing,
	}

	if err := repo.CreateSweepHold(ctx, job, depositTx); err != nil {
		t.Fatalf("create sweep hold: %v", err)
	}
	if err := repo.CreateSweepHold(ctx, job, depositTx); err != nil {
		t.Fatalf("duplicate sweep hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "idempotency_key = ?", sweepHoldKey(job.ID))

	rows, err := repo.DomainBalances(ctx, merchantID, domainID)
	if err != nil {
		t.Fatal(err)
	}
	got := ledgerRowsByAccount(rows)
	if got[models.LedgerAccountMerchantAvailable] != "0" || got[models.LedgerAccountSweepTransit] != "100" {
		t.Fatalf("balances with sweep hold = %#v", got)
	}

	if err := repo.VoidSweepHold(ctx, job.ID); err != nil {
		t.Fatalf("void sweep hold: %v", err)
	}
	if err := repo.VoidSweepHold(ctx, job.ID); err != nil {
		t.Fatalf("duplicate void sweep hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "sweep_job_id = ? AND entry_type = ? AND status = ?", job.ID, models.LedgerEntryTypeSweepHold, models.LedgerStatusPending)
	requireLedgerCount(t, db, 0, "sweep_job_id = ? AND entry_type = ? AND status = ?", job.ID, models.LedgerEntryTypeSweepHold, models.LedgerStatusVoided)
	requireLedgerCount(t, db, 2, "sweep_job_id = ? AND entry_type = ? AND idempotency_key = ?", job.ID, models.LedgerEntryTypeSweepRelease, sweepHoldReleaseKey(job.ID))
	rows, err = repo.DomainBalances(ctx, merchantID, domainID)
	if err != nil {
		t.Fatal(err)
	}
	got = ledgerRowsByAccount(rows)
	if got[models.LedgerAccountMerchantAvailable] != "100" {
		t.Fatalf("available after sweep hold release = %#v", got)
	}
}

func TestLedgerRepoSweepHoldRejectsAssetMismatch(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	prefix := "sweep-hold-mismatch-" + uuid.NewString()

	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	if err := repo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post standalone deposit: %v", err)
	}

	wrongChainJob := models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: depositTx.UniqueHash,
		TransactionHash:       depositTx.Hash,
		WalletID:              walletID,
		MerchantID:            merchantID,
		ChainID:               constants.Base,
		Token:                 depositTx.Token,
		Status:                models.SweepJobStatusProcessing,
	}
	if err := repo.CreateSweepHold(ctx, wrongChainJob, depositTx); err == nil || !strings.Contains(err.Error(), "sweep job chain mismatch") {
		t.Fatalf("wrong chain hold err = %v, want chain mismatch", err)
	}
	requireLedgerCount(t, db, 0, "idempotency_key = ?", sweepHoldKey(wrongChainJob.ID))

	token := "0xtoken"
	wrongTokenJob := models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: depositTx.UniqueHash,
		TransactionHash:       depositTx.Hash,
		WalletID:              walletID,
		MerchantID:            merchantID,
		ChainID:               depositTx.ChainID,
		Token:                 &token,
		Status:                models.SweepJobStatusProcessing,
	}
	if err := repo.CreateSweepHold(ctx, wrongTokenJob, depositTx); err == nil || !strings.Contains(err.Error(), "sweep job token mismatch") {
		t.Fatalf("wrong token hold err = %v, want token mismatch", err)
	}
	requireLedgerCount(t, db, 0, "idempotency_key = ?", sweepHoldKey(wrongTokenJob.ID))
}

func TestLedgerRepoSweepReleaseRestoresAvailableAfterSuccess(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	prefix := "sweep-release-test-" + uuid.NewString()

	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	if err := repo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post standalone deposit: %v", err)
	}
	job := models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: depositTx.UniqueHash,
		TransactionHash:       depositTx.Hash,
		WalletID:              walletID,
		MerchantID:            merchantID,
		ChainID:               depositTx.ChainID,
		Token:                 depositTx.Token,
		Status:                models.SweepJobStatusProcessing,
	}

	if err := repo.CreateSweepHold(ctx, job, depositTx); err != nil {
		t.Fatalf("create sweep hold: %v", err)
	}
	if err := repo.PostSweepRelease(ctx, job, depositTx, " "); err == nil || !strings.Contains(err.Error(), "sweep transaction hash is required") {
		t.Fatalf("empty sweep release err = %v, want tx hash required", err)
	}
	requireLedgerCount(t, db, 0, "idempotency_key = ?", sweepReleaseKey(job.ID))

	if err := repo.PostSweepRelease(ctx, job, depositTx, "0xsweep"); err != nil {
		t.Fatalf("post sweep release: %v", err)
	}
	if err := repo.PostSweepRelease(ctx, job, depositTx, "0xsweep"); err != nil {
		t.Fatalf("duplicate sweep release: %v", err)
	}
	requireLedgerCount(t, db, 2, "idempotency_key = ?", sweepReleaseKey(job.ID))
	if err := repo.VoidSweepHold(ctx, job.ID); err != nil {
		t.Fatalf("void released sweep hold should no-op: %v", err)
	}
	requireLedgerCount(t, db, 0, "idempotency_key = ?", sweepHoldReleaseKey(job.ID))

	rows, err := repo.DomainBalances(ctx, merchantID, domainID)
	if err != nil {
		t.Fatal(err)
	}
	got := ledgerRowsByAccount(rows)
	if got[models.LedgerAccountMerchantAvailable] != "100" || got[models.LedgerAccountSweepTransit] != "0" {
		t.Fatalf("balances after sweep release = %#v", got)
	}
}

func TestLedgerRepoSweepReleaseRejectsMismatchedTransaction(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	otherWalletID := uuid.New()
	prefix := "sweep-release-mismatch-" + uuid.NewString()

	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	if err := repo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post standalone deposit: %v", err)
	}
	job := models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: depositTx.UniqueHash,
		TransactionHash:       depositTx.Hash,
		WalletID:              walletID,
		MerchantID:            merchantID,
		ChainID:               depositTx.ChainID,
		Token:                 depositTx.Token,
		Status:                models.SweepJobStatusProcessing,
	}
	if err := repo.CreateSweepHold(ctx, job, depositTx); err != nil {
		t.Fatalf("create sweep hold: %v", err)
	}

	amountMismatchTx := depositTx
	amountMismatchTx.Amount = "1"
	err := repo.PostSweepRelease(ctx, job, amountMismatchTx, "0xsweep")
	if !errors.Is(err, ErrLedgerReservationRequired) {
		t.Fatalf("PostSweepRelease amount mismatch err = %v, want ErrLedgerReservationRequired", err)
	}
	requireLedgerCount(t, db, 0, "idempotency_key = ?", sweepReleaseKey(job.ID))

	mismatchedTx := ledgerTestTransaction(merchantID, domainID, otherWalletID, prefix+":other-deposit", "100")
	err = repo.PostSweepRelease(ctx, job, mismatchedTx, "0xsweep")
	if err == nil || !strings.Contains(err.Error(), "sweep job transaction mismatch") {
		t.Fatalf("PostSweepRelease mismatch err = %v, want transaction mismatch", err)
	}
	requireLedgerCount(t, db, 0, "idempotency_key = ?", sweepReleaseKey(job.ID))
}

func TestLedgerRepoWithdrawalHoldRequirementRejectsMismatchedAmount(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	prefix := "withdrawal-hold-requirement-" + uuid.NewString()
	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	if err := repo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post standalone deposit: %v", err)
	}

	request := models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   &domainID,
		WalletID:   walletID,
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		AmountRaw:  "40",
		Status:     models.WithdrawalStatusPending,
		ToAddress:  "0xto",
	}
	if err := repo.CreateWithdrawalHold(ctx, request); err != nil {
		t.Fatalf("create withdrawal hold: %v", err)
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return NewLedgerRepo(tx).RequireWithdrawalHoldForRequestWithDB(ctx, tx, request)
	}); err != nil {
		t.Fatalf("matching withdrawal hold requirement: %v", err)
	}

	request.AmountRaw = "41"
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return NewLedgerRepo(tx).RequireWithdrawalHoldForRequestWithDB(ctx, tx, request)
	})
	if !errors.Is(err, ErrLedgerReservationRequired) {
		t.Fatalf("mismatched withdrawal hold requirement err = %v, want ErrLedgerReservationRequired", err)
	}
}

func TestLedgerRepoVoidWithdrawalHoldRejectsIncompleteHoldPair(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	withdrawalID := uuid.New()
	row := testLedgerEntryWithType("incomplete-hold-"+uuid.NewString(), merchantID, &domainID, &walletID, models.LedgerEntryTypeWithdrawalHold, models.LedgerAccountMerchantAvailable, models.LedgerDirectionDebit, models.LedgerStatusPending, "10")
	row.WithdrawalID = &withdrawalID
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("seed incomplete hold: %v", err)
	}

	err := repo.VoidWithdrawalHold(ctx, withdrawalID)
	if !errors.Is(err, ErrLedgerReservationRequired) {
		t.Fatalf("void incomplete withdrawal hold err = %v, want ErrLedgerReservationRequired", err)
	}
	requireLedgerCount(t, db, 0, "withdrawal_id = ? AND entry_type = ?", withdrawalID, models.LedgerEntryTypeWithdrawalRelease)
}

func TestLedgerRepoConcurrentWithdrawalHoldsPreventOverdraw(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	prefix := "concurrent-withdrawal-hold-" + uuid.NewString()
	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	if err := repo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post standalone deposit: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			request := models.WithdrawalRequest{
				ID:         uuid.New(),
				MerchantID: merchantID,
				DomainID:   &domainID,
				WalletID:   walletID,
				Chain:      "ethereum",
				Symbol:     "ETH",
				Decimals:   18,
				AmountRaw:  "80",
				Status:     models.WithdrawalStatusPending,
				ToAddress:  "0xto",
			}
			results <- repo.CreateWithdrawalHold(ctx, request)
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	insufficient := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if errors.Is(err, ErrInsufficientAvailableBalance) {
			insufficient++
			continue
		}
		t.Fatalf("unexpected hold error: %v", err)
	}
	if successes != 1 || insufficient != 1 {
		t.Fatalf("successes=%d insufficient=%d, want 1/1", successes, insufficient)
	}
}

func TestLedgerRepoPostTransactionReversalSkipsReversalVoidedAndUnrelatedRows(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	txModel := ledgerTestTransaction(merchantID, domainID, walletID, "reorg-skip-"+uuid.NewString(), "11")

	original := testLedgerEntryWithType("reorg-skip:original", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "11")
	original.TransactionUniqueHash = txModel.UniqueHash
	original.TransactionHash = txModel.Hash
	existingReversal := testLedgerEntryWithType("reorg-skip:existing-reversal", merchantID, &domainID, &walletID, models.LedgerEntryTypeReorgReversal, models.LedgerAccountMerchantAvailable, models.LedgerDirectionDebit, models.LedgerStatusPosted, "3")
	existingReversal.TransactionUniqueHash = txModel.UniqueHash
	existingReversal.TransactionHash = txModel.Hash
	existingReversal.Reference = txModel.UniqueHash
	voided := testLedgerEntryWithType("reorg-skip:voided", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusVoided, "5")
	voided.TransactionUniqueHash = txModel.UniqueHash
	voided.TransactionHash = txModel.Hash
	unrelated := testLedgerEntryWithType("reorg-skip:unrelated", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "7")
	unrelated.TransactionUniqueHash = txModel.UniqueHash + ":other"
	unrelated.TransactionHash = txModel.Hash + ":other"
	if err := db.WithContext(ctx).Create(&[]models.LedgerEntry{original, existingReversal, voided, unrelated}).Error; err != nil {
		t.Fatalf("seed ledger rows: %v", err)
	}

	if err := repo.PostTransactionReversal(ctx, txModel); err != nil {
		t.Fatalf("post transaction reversal: %v", err)
	}
	if err := repo.PostTransactionReversal(ctx, txModel); err != nil {
		t.Fatalf("duplicate transaction reversal: %v", err)
	}

	key := "reorg-reversal:" + original.ID.String()
	requireLedgerCount(t, db, 1, "idempotency_key = ?", key)
	var reversal models.LedgerEntry
	if err := db.WithContext(ctx).First(&reversal, "idempotency_key = ?", key).Error; err != nil {
		t.Fatalf("load reversal row: %v", err)
	}
	if reversal.Direction != models.LedgerDirectionDebit || reversal.Account != original.Account || reversal.Reference != original.ID.String() {
		t.Fatalf("reversal row = %#v", reversal)
	}
	for _, skipped := range []models.LedgerEntry{existingReversal, voided, unrelated} {
		requireLedgerCount(t, db, 0, "idempotency_key = ?", "reorg-reversal:"+skipped.ID.String())
	}
	requireLedgerCount(t, db, 2, "transaction_unique_hash = ? AND entry_type = ?", txModel.UniqueHash, models.LedgerEntryTypeReorgReversal)
}

func TestLedgerRepoRebuildBalanceProjectionsAggregatesActiveLedgerRows(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}, &models.LedgerBalanceProjection{}); err != nil {
		t.Fatalf("automigrate ledger projection tables: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	prefix := "projection-test-" + uuid.NewString()
	entries := []models.LedgerEntry{
		testLedgerEntryWithType(prefix+":credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "100"),
		testLedgerEntryWithType(prefix+":debit", merchantID, &domainID, &walletID, models.LedgerEntryTypeWithdrawalHold, models.LedgerAccountMerchantAvailable, models.LedgerDirectionDebit, models.LedgerStatusPending, "25"),
		testLedgerEntryWithType(prefix+":voided", merchantID, &domainID, &walletID, models.LedgerEntryTypeAdjustment, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusVoided, "999"),
		testLedgerEntryWithType(prefix+":nonnumeric", merchantID, &domainID, &walletID, models.LedgerEntryTypeAdjustment, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "bad"),
	}
	if err := db.WithContext(ctx).Create(&entries).Error; err != nil {
		t.Fatalf("seed ledger rows: %v", err)
	}

	count, err := repo.RebuildBalanceProjections(ctx)
	if err != nil {
		t.Fatalf("rebuild projections: %v", err)
	}
	if count == 0 {
		t.Fatal("projection rebuild did not write rows")
	}
	rows, err := repo.BalanceProjections(ctx, models.LedgerBalanceProjectionScopeWallet, ledgerBalanceProjectionScopeKey(models.LedgerBalanceProjectionScopeWallet, merchantID, &domainID, &walletID))
	if err != nil {
		t.Fatalf("load projection rows: %v", err)
	}
	got := ledgerRowsByAccount(rows)
	if got[models.LedgerAccountMerchantAvailable] != "75" {
		t.Fatalf("projected available = %#v, want 75", got)
	}
}

func TestLedgerRepoAppendUpdatesBalanceProjections(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}, &models.LedgerBalanceProjection{}); err != nil {
		t.Fatalf("automigrate ledger projection tables: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	prefix := "projection-append-test-" + uuid.NewString()

	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	if err := repo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post standalone deposit: %v", err)
	}
	rows, err := repo.WalletBalances(ctx, merchantID, domainID, walletID)
	if err != nil {
		t.Fatalf("wallet balances after deposit: %v", err)
	}
	if got := ledgerRowsByAccount(rows)[models.LedgerAccountMerchantAvailable]; got != "100" {
		t.Fatalf("projected available after deposit = %q, want 100", got)
	}

	withdrawal := models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   &domainID,
		WalletID:   walletID,
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		AmountRaw:  "40",
		Status:     models.WithdrawalStatusApproved,
	}
	if err := repo.CreateWithdrawalHold(ctx, withdrawal); err != nil {
		t.Fatalf("create withdrawal hold: %v", err)
	}
	rows, err = repo.WalletBalances(ctx, merchantID, domainID, walletID)
	if err != nil {
		t.Fatalf("wallet balances after withdrawal hold: %v", err)
	}
	got := ledgerRowsByAccount(rows)
	if got[models.LedgerAccountMerchantAvailable] != "60" || got[models.LedgerAccountWithdrawalTransit] != "40" {
		t.Fatalf("projected balances after withdrawal hold = %#v, want available 60 transit 40", got)
	}
}

func TestLedgerRepoRebuildBalanceProjectionsNormalizesTokenVariants(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}, &models.LedgerBalanceProjection{}); err != nil {
		t.Fatalf("automigrate ledger projection tables: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	tokenUpper := " 0xABC "
	tokenLower := "0xabc"
	prefix := "projection-token-test-" + uuid.NewString()
	credit := testLedgerEntryWithType(prefix+":credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "50")
	credit.Token = &tokenUpper
	credit.Symbol = "USDC"
	credit.Decimals = 6
	debit := testLedgerEntryWithType(prefix+":debit", merchantID, &domainID, &walletID, models.LedgerEntryTypeWithdrawalHold, models.LedgerAccountMerchantAvailable, models.LedgerDirectionDebit, models.LedgerStatusPending, "10")
	debit.Token = &tokenLower
	debit.Symbol = "USDC"
	debit.Decimals = 6
	if err := db.WithContext(ctx).Create(&[]models.LedgerEntry{credit, debit}).Error; err != nil {
		t.Fatalf("seed token variant ledger rows: %v", err)
	}

	if _, err := repo.RebuildBalanceProjections(ctx); err != nil {
		t.Fatalf("rebuild projections: %v", err)
	}
	scopeKey := ledgerBalanceProjectionScopeKey(models.LedgerBalanceProjectionScopeWallet, merchantID, &domainID, &walletID)
	var count int64
	if err := db.WithContext(ctx).
		Model(&models.LedgerBalanceProjection{}).
		Where("scope_type = ? AND scope_key = ? AND token_fingerprint = ? AND account = ?", models.LedgerBalanceProjectionScopeWallet, scopeKey, "0xabc", models.LedgerAccountMerchantAvailable).
		Count(&count).Error; err != nil {
		t.Fatalf("count token projection rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("token projection row count = %d, want 1", count)
	}
	var projection models.LedgerBalanceProjection
	if err := db.WithContext(ctx).
		First(&projection, "scope_type = ? AND scope_key = ? AND token_fingerprint = ? AND account = ?", models.LedgerBalanceProjectionScopeWallet, scopeKey, "0xabc", models.LedgerAccountMerchantAvailable).Error; err != nil {
		t.Fatalf("load token projection row: %v", err)
	}
	if projection.Token == nil || *projection.Token != "0xabc" || projection.BalanceRaw != "40" {
		t.Fatalf("token projection = %#v, want normalized token 0xabc balance 40", projection)
	}
}

func TestLedgerRepoOpenInvariantReconciliationJobsDedupesByScope(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}, &models.ReconciliationJob{}); err != nil {
		t.Fatalf("automigrate ledger reconciliation tables: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	key := "invariant-reconcile-" + uuid.NewString()
	row := testLedgerEntry(key, merchantID, &domainID, &walletID, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "123")
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("seed invariant row: %v", err)
	}

	created, err := repo.OpenInvariantReconciliationJobs(ctx, NewReconciliationRepo(db), 100)
	if err != nil {
		t.Fatalf("open invariant reconciliation jobs: %v", err)
	}
	if created != 1 {
		t.Fatalf("created jobs = %d, want 1", created)
	}
	created, err = repo.OpenInvariantReconciliationJobs(ctx, NewReconciliationRepo(db), 100)
	if err != nil {
		t.Fatalf("dedupe invariant reconciliation jobs: %v", err)
	}
	if created != 0 {
		t.Fatalf("duplicate created jobs = %d, want 0", created)
	}
	var job models.ReconciliationJob
	if err := db.WithContext(ctx).First(&job, "resource_type = ? AND resource_id = ?", "ledger_invariant", key).Error; err != nil {
		t.Fatalf("load reconciliation job: %v", err)
	}
	if job.Status != models.ReconciliationStatusOpen || job.MerchantID == nil || *job.MerchantID != merchantID || job.DomainID == nil || *job.DomainID != domainID {
		t.Fatalf("job scope = %#v", job)
	}
	var evidence map[string]any
	if err := json.Unmarshal([]byte(job.EvidenceJSON), &evidence); err != nil {
		t.Fatalf("decode job evidence: %v", err)
	}
	if evidence["net_raw"] != "123" || strings.Contains(strings.ToLower(job.EvidenceJSON), "secret") {
		t.Fatalf("job evidence = %s", job.EvidenceJSON)
	}
}

func TestLedgerRepoFindInvariantIssuesIncludesTenantScope(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	key := "invariant-scope-test-" + uuid.NewString()

	row := testLedgerEntry(key, merchantID, &domainID, &walletID, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "123")
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("seed invariant row: %v", err)
	}

	issues, err := repo.FindInvariantIssues(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.IdempotencyKey != key {
			continue
		}
		if issue.MerchantID != merchantID || issue.DomainID == nil || *issue.DomainID != domainID || issue.NetRaw != "123" {
			t.Fatalf("issue scope = %#v", issue)
		}
		return
	}
	t.Fatalf("invariant issue %q not found in %#v", key, issues)
}

func testLedgerEntry(key string, merchantID uuid.UUID, domainID *uuid.UUID, walletID *uuid.UUID, account string, direction string, status string, amount string) models.LedgerEntry {
	return testLedgerEntryWithType(key, merchantID, domainID, walletID, models.LedgerEntryTypeAdjustment, account, direction, status, amount)
}

func testLedgerEntryWithType(key string, merchantID uuid.UUID, domainID *uuid.UUID, walletID *uuid.UUID, entryType string, account string, direction string, status string, amount string) models.LedgerEntry {
	now := time.Now()
	return models.LedgerEntry{
		ID:             uuid.New(),
		MerchantID:     merchantID,
		DomainID:       domainID,
		WalletID:       walletID,
		ChainID:        constants.Ethereum,
		Symbol:         "ETH",
		Decimals:       18,
		EntryType:      entryType,
		Account:        account,
		Direction:      direction,
		Status:         status,
		AmountRaw:      amount,
		IdempotencyKey: key,
		Reference:      key,
		PostedAt:       &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func ledgerTestTransaction(merchantID, domainID, walletID uuid.UUID, uniqueHash string, amount string) models.Transaction {
	return models.Transaction{
		ID:            uuid.New(),
		ChainID:       constants.Ethereum,
		UniqueHash:    uniqueHash,
		Hash:          uniqueHash + ":hash",
		BlockNumber:   "1",
		BlockHash:     uniqueHash + ":block",
		Symbol:        "ETH",
		Decimals:      18,
		FromAddress:   "0xfrom",
		ToAddress:     "0xto",
		Amount:        amount,
		Status:        models.TransactionStatusConfirmed,
		MerchantID:    &merchantID,
		DomainID:      &domainID,
		WalletID:      &walletID,
		Confirmations: 12,
	}
}

func requireLedgerCount(t *testing.T, db *gorm.DB, want int64, query string, args ...any) {
	t.Helper()
	var count int64
	if err := db.Model(&models.LedgerEntry{}).Where(query, args...).Count(&count).Error; err != nil {
		t.Fatalf("count ledger entries: %v", err)
	}
	if count != want {
		t.Fatalf("ledger entry count for %q args=%v = %d, want %d", query, args, count, want)
	}
}

func ledgerRowsByAccount(rows []LedgerBalanceRow) map[string]string {
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Account] = row.BalanceRaw
	}
	return out
}

func extractLedgerFunctionBody(t *testing.T, source, functionName string) string {
	t.Helper()
	start := strings.Index(source, "func ")
	for start != -1 {
		remaining := source[start:]
		open := strings.Index(remaining, "{")
		if open == -1 {
			t.Fatalf("function %s has no opening brace", functionName)
		}
		signature := remaining[:open]
		if strings.Contains(signature, " "+functionName+"(") || strings.Contains(signature, ") "+functionName+"(") {
			index := start + open
			depth := 0
			for i := index; i < len(source); i++ {
				switch source[i] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						return source[index : i+1]
					}
				}
			}
			t.Fatalf("function %s has no closing brace", functionName)
		}
		next := strings.Index(remaining[5:], "func ")
		if next == -1 {
			break
		}
		start += 5 + next
	}
	t.Fatalf("function %s not found", functionName)
	return ""
}
