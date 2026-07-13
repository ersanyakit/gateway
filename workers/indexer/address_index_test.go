package addressindex

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAddressIndexLoadMergesSourcesAndSkipsTerminalPoolRows(t *testing.T) {
	t.Setenv("ADDRESS_INDEX_PRELOAD_LIMIT", "")
	db := openAddressIndexPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Merchant{},
		&models.Domain{},
		&models.Wallet{},
		&models.WalletAddressLookup{},
		&models.WalletAddress{},
	); err != nil {
		t.Fatalf("automigrate address index models: %v", err)
	}

	merchantID := uuid.New()
	domainID := uuid.New()
	legacyWalletID := uuid.New()
	poolWalletID := uuid.New()
	lookupWalletID := uuid.New()
	if err := db.Create(&models.Merchant{ID: merchantID, Name: "Index Merchant", Email: uuid.NewString() + "@example.test"}).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.Create(&models.Domain{ID: domainID, MerchantID: merchantID, DomainURL: "index.example.test", APIKey: "key", APISecret: "secret", HDAccountID: 17}).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if err := db.Create(&models.Wallet{
		ID:              legacyWalletID,
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       "legacy",
		UserID:          "user",
		HDAccountID:     17,
		HDAddressId:     1,
		EthereumAddress: "0xShared",
		BitcoinAddress:  "bc1legacy",
	}).Error; err != nil {
		t.Fatalf("seed legacy wallet: %v", err)
	}
	if err := db.Create(&models.WalletAddressLookup{
		ID:                uuid.New(),
		ChainID:           constants.Base,
		ChainName:         constants.ChainName(constants.Base),
		Address:           "0xLookup",
		NormalizedAddress: "0xlookup",
		Asset:             "native",
		MerchantID:        merchantID,
		DomainID:          domainID,
		WalletID:          lookupWalletID,
		ProductID:         "lookup",
		UserID:            "user",
		Source:            "test",
	}).Error; err != nil {
		t.Fatalf("seed lookup row: %v", err)
	}
	if err := db.Create(&models.WalletAddress{
		ID:                uuid.New(),
		ChainID:           constants.Ethereum,
		ChainName:         constants.ChainName(constants.Ethereum),
		Address:           "0xShared",
		NormalizedAddress: "0xshared",
		Asset:             "native",
		MerchantID:        merchantID,
		DomainID:          domainID,
		WalletID:          poolWalletID,
		ProductID:         "pool",
		UserID:            "user",
		HDAccountID:       17,
		HDAddressID:       2,
		Purpose:           models.WalletAddressPurposeCheckout,
		LifecycleStatus:   models.WalletAddressStatusAssigned,
		ReusePolicy:       models.WalletAddressReusePolicyFresh,
		Source:            "test",
	}).Error; err != nil {
		t.Fatalf("seed pool row: %v", err)
	}
	if err := db.Create(&models.WalletAddress{
		ID:                uuid.New(),
		ChainID:           constants.Avalanche,
		ChainName:         constants.ChainName(constants.Avalanche),
		Address:           "0xExpired",
		NormalizedAddress: "0xexpired",
		Asset:             "native",
		MerchantID:        merchantID,
		DomainID:          domainID,
		WalletID:          uuid.New(),
		ProductID:         "expired",
		UserID:            "user",
		HDAccountID:       17,
		HDAddressID:       3,
		Purpose:           models.WalletAddressPurposeCheckout,
		LifecycleStatus:   models.WalletAddressStatusExpired,
		ReusePolicy:       models.WalletAddressReusePolicyFresh,
		Source:            "test",
	}).Error; err != nil {
		t.Fatalf("seed expired pool row: %v", err)
	}

	index := NewAddressIndex(context.Background(), db)
	if err := index.Load(); err != nil {
		t.Fatalf("load address index: %v", err)
	}
	if !index.Ready() {
		t.Fatal("fully loaded address index must be ready for authoritative lookups")
	}

	if info, ok := index.Get(constants.Ethereum, "0xshared"); !ok || info.WalletID != poolWalletID {
		t.Fatalf("ethereum shared info = %#v/%v, want pool wallet %s", info, ok, poolWalletID)
	}
	if info, ok := index.Get(constants.Bitcoin, "bc1legacy"); !ok || info.WalletID != legacyWalletID {
		t.Fatalf("bitcoin legacy info = %#v/%v, want legacy wallet %s", info, ok, legacyWalletID)
	}
	if info, ok := index.Get(constants.Base, "0xlookup"); !ok || info.WalletID != lookupWalletID {
		t.Fatalf("base lookup info = %#v/%v, want lookup wallet %s", info, ok, lookupWalletID)
	}
	if _, ok := index.Get(constants.Avalanche, "0xexpired"); ok {
		t.Fatal("terminal pool address must not be indexed")
	}
}

func TestAddressIndexAddWalletPublishesEveryChainAddress(t *testing.T) {
	index := NewAddressIndex(context.Background(), nil)
	wallet := models.Wallet{
		ID: uuid.New(), MerchantID: uuid.New(), DomainID: uuid.New(), ProductID: "product", UserID: "user",
		BitcoinAddress: "bc1owned", EthereumAddress: "0xEVM", AvalancheAddress: "0xAVAX",
		BinanceAddress: "0xBSC", BaseAddress: "0xBASE", ArbitrumAddress: "0xARB", UnichainAddress: "0xUNI",
		TronAddress: "TOwned", SolanaAddress: "SolOwned", ChilizAddress: "0xCHZ", ChilizSpicyAddress: "0xSPICY",
	}
	index.AddWallet(wallet)

	for chainID, address := range map[constants.ChainID]string{
		constants.Bitcoin: "bc1owned", constants.Ethereum: "0xevm", constants.Avalanche: "0xavax",
		constants.Binance: "0xbsc", constants.Base: "0xbase", constants.Arbitrum: "0xarb", constants.Unichain: "0xuni",
		constants.TRON: "TOwned", constants.TRONTestnet: "TOwned", constants.Solana: "SolOwned",
		constants.Chiliz: "0xchz", constants.ChilizSpicy: "0xspicy",
	} {
		info, ok := index.Get(chainID, address)
		if !ok || info.WalletID != wallet.ID {
			t.Fatalf("chain %s address %q = %#v/%v, want wallet %s", constants.ChainName(chainID), address, info, ok, wallet.ID)
		}
	}
}

func TestAddressIndexDisabledPreloadIsNotAuthoritative(t *testing.T) {
	t.Setenv("ADDRESS_INDEX_PRELOAD_LIMIT", "0")
	index := NewAddressIndex(context.Background(), nil)
	if err := index.Load(); err != nil {
		t.Fatalf("load disabled index: %v", err)
	}
	if index.Ready() {
		t.Fatal("disabled preload must not make negative lookups authoritative")
	}
}

func TestAddressIndexSameMerchantDistinguishesInternalTransfers(t *testing.T) {
	index := NewAddressIndex(context.Background(), nil)
	merchantA := uuid.New()
	merchantB := uuid.New()
	from := models.Wallet{ID: uuid.New(), MerchantID: merchantA, DomainID: uuid.New(), EthereumAddress: "0xfrom"}
	sameMerchantTo := models.Wallet{ID: uuid.New(), MerchantID: merchantA, DomainID: uuid.New(), EthereumAddress: "0xsame"}
	otherMerchantTo := models.Wallet{ID: uuid.New(), MerchantID: merchantB, DomainID: uuid.New(), EthereumAddress: "0xother"}
	index.AddWallet(from)
	index.AddWallet(sameMerchantTo)
	index.AddWallet(otherMerchantTo)

	if !index.SameMerchant(constants.Ethereum, from.EthereumAddress, sameMerchantTo.EthereumAddress) {
		t.Fatal("same-merchant platform transfer must be classified as internal")
	}
	if index.SameMerchant(constants.Ethereum, from.EthereumAddress, otherMerchantTo.EthereumAddress) {
		t.Fatal("cross-merchant platform transfer must remain an inbound deposit candidate")
	}
	if index.SameMerchant(constants.Ethereum, "0xexternal", sameMerchantTo.EthereumAddress) {
		t.Fatal("external sender must not be classified as an internal transfer")
	}
}

func openAddressIndexPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("OUTBOX_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("MONEY_OUTBOX_TEST_DATABASE_URL")
	}
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set OUTBOX_TEST_DATABASE_URL to run address index Postgres tests")
	}

	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect test postgres: %v", err)
	}
	if err := adminDB.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error; err != nil {
		t.Fatalf("enable uuid extension: %v", err)
	}
	schemaName := "address_index_test_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	quotedSchema := quoteAddressIndexPostgresIdentifier(schemaName)
	if err := adminDB.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}

	db, err := gorm.Open(postgres.Open(addressIndexPostgresDSNWithSearchPath(dsn, schemaName)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect schema-scoped test postgres: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get test sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	t.Cleanup(func() {
		_ = sqlDB.Close()
		_ = adminDB.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error
		if adminSQL, err := adminDB.DB(); err == nil {
			_ = adminSQL.Close()
		}
	})
	return db
}

func addressIndexPostgresDSNWithSearchPath(dsn, schemaName string) string {
	searchPath := schemaName + ",public"
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err == nil {
			query := parsed.Query()
			query.Set("search_path", searchPath)
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}
	return strings.TrimSpace(dsn) + " search_path=" + searchPath
}

func quoteAddressIndexPostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
