package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestWalletAddressReservationSerializesConcurrentIndexes(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(4)
	}
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WalletAddressReservation{}, &models.WalletAddress{}); err != nil {
		t.Fatalf("automigrate wallet address pool: %v", err)
	}

	merchantID, domainID, hdAccountID := seedWalletAddressPoolScope(t, db)
	repo := NewWalletAddressRepo(db)
	ctx := context.Background()
	const workers = 6
	start := make(chan struct{})
	indexes := make(chan uint32, workers)
	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			reservation, err := repo.ReserveNextHDIndex(ctx, WalletAddressReservationRequest{
				MerchantID:  merchantID,
				DomainID:    domainID,
				ProductID:   "checkout:" + uuid.NewString(),
				UserID:      "user:" + uuid.NewString(),
				HDAccountID: hdAccountID,
				Purpose:     models.WalletAddressPurposeCheckout,
			})
			if err != nil {
				errs <- err
				return
			}
			indexes <- reservation.HDAddressID
		}(i)
	}
	close(start)
	wg.Wait()
	close(indexes)
	close(errs)
	for err := range errs {
		t.Fatalf("reserve concurrent index: %v", err)
	}
	seen := map[uint32]bool{}
	for index := range indexes {
		if seen[index] {
			t.Fatalf("duplicate reserved hd index %d", index)
		}
		seen[index] = true
	}
	for want := uint32(1); want <= workers; want++ {
		if !seen[want] {
			t.Fatalf("reserved indexes = %#v, missing %d", seen, want)
		}
	}
}

func TestWalletAddressReservationIsIdempotentAndUsesPoolState(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WalletAddressReservation{}, &models.WalletAddress{}); err != nil {
		t.Fatalf("automigrate wallet address pool: %v", err)
	}

	merchantID, domainID, hdAccountID := seedWalletAddressPoolScope(t, db)
	legacyWallet := models.Wallet{
		ID:              uuid.New(),
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       "legacy",
		UserID:          "legacy-user",
		HDAccountID:     hdAccountID,
		HDAddressId:     8,
		EthereumAddress: "0xlegacy",
	}
	if err := db.Create(&legacyWallet).Error; err != nil {
		t.Fatalf("seed legacy wallet: %v", err)
	}

	repo := NewWalletAddressRepo(db)
	ctx := context.Background()
	first, err := repo.ReserveNextHDIndex(ctx, WalletAddressReservationRequest{
		MerchantID:  merchantID,
		DomainID:    domainID,
		ProductID:   "checkout:1",
		UserID:      "user:1",
		HDAccountID: hdAccountID,
		Purpose:     models.WalletAddressPurposeCheckout,
	})
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if first.HDAddressID != 9 {
		t.Fatalf("first hd index = %d, want 9", first.HDAddressID)
	}
	retry, err := repo.ReserveNextHDIndex(ctx, WalletAddressReservationRequest{
		MerchantID:  merchantID,
		DomainID:    domainID,
		ProductID:   "checkout:1",
		UserID:      "user:1",
		HDAccountID: hdAccountID,
		Purpose:     models.WalletAddressPurposeCheckout,
	})
	if err != nil {
		t.Fatalf("reserve retry: %v", err)
	}
	if retry.ID != first.ID || retry.HDAddressID != first.HDAddressID {
		t.Fatalf("retry reservation = %s/%d, want %s/%d", retry.ID, retry.HDAddressID, first.ID, first.HDAddressID)
	}
}

func TestWalletAddressBackfillCreatesPoolRowsAndLookupParity(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Merchant{},
		&models.Domain{},
		&models.Wallet{},
		&models.WalletAddressLookup{},
		&models.WalletAddressReservation{},
		&models.WalletAddress{},
	); err != nil {
		t.Fatalf("automigrate wallet address pool: %v", err)
	}

	wallet := seedWalletAddressLookupWallet(t, db, "0xAbC123")
	repo := NewWalletAddressRepo(db)
	if count, err := repo.BackfillWallets(context.Background(), 100); err != nil {
		t.Fatalf("backfill pool: %v", err)
	} else if count != 1 {
		t.Fatalf("backfilled wallets = %d, want 1", count)
	}

	found, err := repo.FindWallet(context.Background(), constants.Ethereum, "0xabc123")
	if err != nil {
		t.Fatalf("find wallet address pool: %v", err)
	}
	if found.ID != wallet.ID {
		t.Fatalf("found wallet = %s, want %s", found.ID, wallet.ID)
	}

	var addressRow models.WalletAddress
	if err := db.First(&addressRow, "chain_id = ? AND normalized_address = ?", constants.Ethereum, "0xabc123").Error; err != nil {
		t.Fatalf("load wallet address row: %v", err)
	}
	if addressRow.LifecycleStatus != models.WalletAddressStatusAssigned || addressRow.HDAddressID != wallet.HDAddressId {
		t.Fatalf("address status/index = %s/%d, want assigned/%d", addressRow.LifecycleStatus, addressRow.HDAddressID, wallet.HDAddressId)
	}

	var reservation models.WalletAddressReservation
	if err := db.First(&reservation, "wallet_id = ?", wallet.ID).Error; err != nil {
		t.Fatalf("load reservation row: %v", err)
	}
	if reservation.LifecycleStatus != models.WalletAddressStatusAssigned || reservation.HDAddressID != wallet.HDAddressId {
		t.Fatalf("reservation status/index = %s/%d, want assigned/%d", reservation.LifecycleStatus, reservation.HDAddressID, wallet.HDAddressId)
	}
}

func TestWalletAddressGapScanPersistsUsedIndexesAndAnomalies(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.WalletAddress{}, &models.WalletAddressGapScanCursor{}, &models.WalletAddressGapScanAnomaly{}); err != nil {
		t.Fatalf("automigrate wallet address gap scan: %v", err)
	}

	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	row := models.WalletAddress{
		ID:                uuid.New(),
		ChainID:           constants.Ethereum,
		ChainName:         constants.ChainName(constants.Ethereum),
		Address:           "0xUsed",
		NormalizedAddress: "0xused",
		Asset:             "native",
		MerchantID:        merchantID,
		DomainID:          domainID,
		WalletID:          walletID,
		ProductID:         "checkout",
		UserID:            "user",
		HDAccountID:       77,
		HDAddressID:       3,
		Purpose:           models.WalletAddressPurposeCheckout,
		ReusePolicy:       models.WalletAddressReusePolicyFresh,
		LifecycleStatus:   models.WalletAddressStatusAssigned,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed wallet address: %v", err)
	}

	scannedAt := time.Now().UTC()
	err := NewWalletAddressRepo(db).PersistGapScanResult(context.Background(), WalletAddressGapScanResult{
		ChainID:          constants.Ethereum,
		HDAccountID:      77,
		Purpose:          models.WalletAddressPurposeCheckout,
		Lookahead:        20,
		LastScannedIndex: 25,
		UsedIndexes:      []uint32{3, 9},
		Anomalies: []WalletAddressGapScanAnomalyInput{{
			HDAddressID: 9,
			Category:    models.WalletAddressGapAnomalyUsedUnreserved,
			Detail:      "used address observed beyond reserved pool",
		}},
		ScannedAt: scannedAt,
	})
	if err != nil {
		t.Fatalf("persist gap scan: %v", err)
	}

	var updated models.WalletAddress
	if err := db.First(&updated, "id = ?", row.ID).Error; err != nil {
		t.Fatalf("load updated address: %v", err)
	}
	if updated.LifecycleStatus != models.WalletAddressStatusUsed || updated.UsedAt == nil {
		t.Fatalf("updated status/used_at = %s/%v, want used timestamp", updated.LifecycleStatus, updated.UsedAt)
	}

	var cursor models.WalletAddressGapScanCursor
	if err := db.First(&cursor, "chain_id = ? AND hd_account_id = ? AND purpose = ?", constants.Ethereum, 77, models.WalletAddressPurposeCheckout).Error; err != nil {
		t.Fatalf("load cursor: %v", err)
	}
	var used []uint32
	if err := json.Unmarshal([]byte(cursor.DiscoveredUsedIndexesJSON), &used); err != nil {
		t.Fatalf("unmarshal used indexes: %v", err)
	}
	if len(used) != 2 || used[0] != 3 || used[1] != 9 || cursor.HighestUsedIndex != 9 {
		t.Fatalf("cursor used=%v highest=%d, want [3 9]/9", used, cursor.HighestUsedIndex)
	}

	var anomalies int64
	if err := db.Model(&models.WalletAddressGapScanAnomaly{}).Where("chain_id = ? AND hd_account_id = ?", constants.Ethereum, 77).Count(&anomalies).Error; err != nil {
		t.Fatalf("count anomalies: %v", err)
	}
	if anomalies != 2 {
		t.Fatalf("anomalies = %d, want explicit anomaly plus missing pool anomaly", anomalies)
	}
}

func TestWalletAddressReservationInfersPurposeAndRejectsTerminalExisting(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WalletAddressReservation{}, &models.WalletAddress{}); err != nil {
		t.Fatalf("automigrate wallet address pool: %v", err)
	}

	merchantID, domainID, hdAccountID := seedWalletAddressPoolScope(t, db)
	repo := NewWalletAddressRepo(db)
	ctx := context.Background()
	reservation, err := repo.ReserveNextHDIndex(ctx, WalletAddressReservationRequest{
		MerchantID:  merchantID,
		DomainID:    domainID,
		ProductID:   "static:user-1",
		UserID:      "user-1",
		HDAccountID: hdAccountID,
	})
	if err != nil {
		t.Fatalf("reserve static deposit: %v", err)
	}
	if reservation.Purpose != models.WalletAddressPurposeStaticDeposit || reservation.ReusePolicy != models.WalletAddressReusePolicyReuse {
		t.Fatalf("reservation purpose/policy = %s/%s, want static_deposit/reuse", reservation.Purpose, reservation.ReusePolicy)
	}
	if err := db.Model(&models.WalletAddressReservation{}).
		Where("id = ?", reservation.ID).
		Update("lifecycle_status", models.WalletAddressStatusExpired).Error; err != nil {
		t.Fatalf("expire reservation: %v", err)
	}
	_, err = repo.ReserveNextHDIndex(ctx, WalletAddressReservationRequest{
		MerchantID:  merchantID,
		DomainID:    domainID,
		ProductID:   "static:user-1",
		UserID:      "user-1",
		HDAccountID: hdAccountID,
	})
	if !errors.Is(err, ErrWalletAddressReservationTerminal) {
		t.Fatalf("reserve terminal reservation error = %v, want ErrWalletAddressReservationTerminal", err)
	}
}

func TestWalletAddressGapScanMergesCursorAndFeedsAllocator(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Merchant{},
		&models.Domain{},
		&models.Wallet{},
		&models.WalletAddressReservation{},
		&models.WalletAddress{},
		&models.WalletAddressGapScanCursor{},
		&models.WalletAddressGapScanAnomaly{},
	); err != nil {
		t.Fatalf("automigrate wallet address gap scan: %v", err)
	}

	merchantID, domainID, hdAccountID := seedWalletAddressPoolScope(t, db)
	existingUsed, _ := json.Marshal([]uint32{2, 10})
	cursor := models.WalletAddressGapScanCursor{
		ID:                        uuid.New(),
		ChainID:                   constants.Ethereum,
		ChainName:                 constants.ChainName(constants.Ethereum),
		HDAccountID:               hdAccountID,
		Purpose:                   models.WalletAddressPurposeCheckout,
		Lookahead:                 20,
		LastScannedIndex:          12,
		HighestUsedIndex:          10,
		DiscoveredUsedIndexesJSON: string(existingUsed),
		ScannedAt:                 time.Now().Add(-time.Hour).UTC(),
	}
	if err := db.Create(&cursor).Error; err != nil {
		t.Fatalf("seed gap cursor: %v", err)
	}

	repo := NewWalletAddressRepo(db)
	if err := repo.PersistGapScanResult(context.Background(), WalletAddressGapScanResult{
		ChainID:          constants.Ethereum,
		HDAccountID:      hdAccountID,
		Purpose:          models.WalletAddressPurposeCheckout,
		Lookahead:        20,
		LastScannedIndex: 5,
		UsedIndexes:      []uint32{3},
		ScannedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist merged gap scan: %v", err)
	}

	var updated models.WalletAddressGapScanCursor
	if err := db.First(&updated, "id = ?", cursor.ID).Error; err != nil {
		t.Fatalf("load merged cursor: %v", err)
	}
	var used []uint32
	if err := json.Unmarshal([]byte(updated.DiscoveredUsedIndexesJSON), &used); err != nil {
		t.Fatalf("unmarshal merged used indexes: %v", err)
	}
	if len(used) != 3 || used[0] != 2 || used[1] != 3 || used[2] != 10 {
		t.Fatalf("merged used indexes = %v, want [2 3 10]", used)
	}
	if updated.LastScannedIndex != 12 || updated.HighestUsedIndex != 10 {
		t.Fatalf("cursor scan/highest = %d/%d, want 12/10", updated.LastScannedIndex, updated.HighestUsedIndex)
	}

	reservation, err := repo.ReserveNextHDIndex(context.Background(), WalletAddressReservationRequest{
		MerchantID:  merchantID,
		DomainID:    domainID,
		ProductID:   "checkout:gap",
		UserID:      "user:gap",
		HDAccountID: hdAccountID,
		Purpose:     models.WalletAddressPurposeCheckout,
	})
	if err != nil {
		t.Fatalf("reserve after gap scan: %v", err)
	}
	if reservation.HDAddressID != 11 {
		t.Fatalf("reserved hd index = %d, want 11", reservation.HDAddressID)
	}
}

func TestWalletAddressFindWalletIgnoresTerminalRows(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Wallet{}, &models.WalletAddress{}); err != nil {
		t.Fatalf("automigrate wallet address: %v", err)
	}
	walletID := uuid.New()
	row := models.WalletAddress{
		ID:                uuid.New(),
		ChainID:           constants.Ethereum,
		ChainName:         constants.ChainName(constants.Ethereum),
		Address:           "0xExpired",
		NormalizedAddress: "0xexpired",
		Asset:             "native",
		MerchantID:        uuid.New(),
		DomainID:          uuid.New(),
		WalletID:          walletID,
		ProductID:         "checkout",
		UserID:            "user",
		HDAccountID:       99,
		HDAddressID:       1,
		Purpose:           models.WalletAddressPurposeCheckout,
		LifecycleStatus:   models.WalletAddressStatusExpired,
		ReusePolicy:       models.WalletAddressReusePolicyFresh,
		Source:            "test",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed terminal wallet address: %v", err)
	}
	_, err := NewWalletAddressRepo(db).FindWallet(context.Background(), constants.Ethereum, "0xexpired")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("find terminal wallet error = %v, want record not found", err)
	}
}

func TestWalletAddressReusePolicyDecision(t *testing.T) {
	cases := []struct {
		name             string
		purpose          string
		hasActiveAddress bool
		wantPolicy       string
		wantReuse        bool
		wantFresh        bool
	}{
		{"checkout always fresh", models.WalletAddressPurposeCheckout, true, models.WalletAddressReusePolicyFresh, false, true},
		{"static deposit reuses active", models.WalletAddressPurposeStaticDeposit, true, models.WalletAddressReusePolicyReuse, true, false},
		{"cex deposit reuses active", models.WalletAddressPurposeCEXDeposit, true, models.WalletAddressReusePolicyReuse, true, false},
		{"static deposit allocates without active", models.WalletAddressPurposeStaticDeposit, false, models.WalletAddressReusePolicyReuse, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideWalletAddressReusePolicy(tc.purpose, tc.hasActiveAddress)
			if got.Policy != tc.wantPolicy || got.ReuseExisting != tc.wantReuse || got.RequiresFresh != tc.wantFresh {
				t.Fatalf("decision = %#v, want policy=%s reuse=%v fresh=%v", got, tc.wantPolicy, tc.wantReuse, tc.wantFresh)
			}
		})
	}
}

func seedWalletAddressPoolScope(t *testing.T, db *gorm.DB) (uuid.UUID, uuid.UUID, uint32) {
	t.Helper()
	merchantID := uuid.New()
	domainID := uuid.New()
	hdAccountID := uint32(7000) + uint32(domainID[0])
	merchant := models.Merchant{ID: merchantID, Name: "Pool Merchant " + merchantID.String(), Email: merchantID.String() + "@example.test"}
	domain := models.Domain{
		ID:          domainID,
		MerchantID:  merchantID,
		DomainURL:   "pool-" + domainID.String() + ".example.test",
		APIKey:      "key-" + domainID.String(),
		APISecret:   "secret-" + domainID.String(),
		HDAccountID: hdAccountID,
	}
	if err := db.Create(&merchant).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.Create(&domain).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	return merchantID, domainID, hdAccountID
}
