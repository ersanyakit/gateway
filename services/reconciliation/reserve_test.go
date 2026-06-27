package reconciliation

import (
	"context"
	"math/big"
	"net/url"
	"os"
	"strings"
	"testing"

	"core/constants"
	"core/models"
	"core/repositories"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestParseBalanceComponents(t *testing.T) {
	components := parseBalanceComponents("ETH:1.500000000000000000 | WETH:0.000000000000000001")
	if components["ETH"] != "1.500000000000000000" {
		t.Fatalf("ETH component = %q", components["ETH"])
	}
	if components["WETH"] != "0.000000000000000001" {
		t.Fatalf("WETH component = %q", components["WETH"])
	}

	raw := parseBalanceComponents("0x10")
	if raw[""] != "0x10" {
		t.Fatalf("raw component = %q, want 0x10", raw[""])
	}
}

func TestAmountToRawConvertsDecimalUnits(t *testing.T) {
	amount, ok := amountToRaw("1.500000000000000000", 18)
	if !ok {
		t.Fatal("decimal amount should be readable")
	}
	if amount.String() != "1500000000000000000" {
		t.Fatalf("amount = %s, want 1500000000000000000", amount)
	}
}

func TestAmountToRawRejectsPrecisionThatCannotBeRepresented(t *testing.T) {
	if _, ok := amountToRaw("0.000000000001000000", 6); ok {
		t.Fatal("18-decimal formatted token amount should not be coerced into a 6-decimal raw value")
	}
}

func TestEvaluateExpectedReserveDetectsDeficit(t *testing.T) {
	expected := reserveExpectedBalance{
		Symbol:     "ETH",
		Decimals:   18,
		BalanceRaw: mustBigInt("2000000000000000000"),
	}
	components := parseBalanceComponents("ETH:1.500000000000000000")
	if got := evaluateExpectedReserve(components, expected, constants.Ethereum); got != reserveIssueDeficit {
		t.Fatalf("issue = %q, want %q", got, reserveIssueDeficit)
	}
}

func TestEvaluateExpectedReserveAcceptsSufficientBalance(t *testing.T) {
	expected := reserveExpectedBalance{
		Symbol:     "ETH",
		Decimals:   18,
		BalanceRaw: mustBigInt("1500000000000000000"),
	}
	components := parseBalanceComponents("ETH:2.000000000000000000")
	if got := evaluateExpectedReserve(components, expected, constants.Ethereum); got != reserveIssueNone {
		t.Fatalf("issue = %q, want none", got)
	}
}

func TestEvaluateExpectedReserveUsesRawNativeComponent(t *testing.T) {
	expected := reserveExpectedBalance{
		Symbol:     "TRX",
		Decimals:   6,
		BalanceRaw: mustBigInt("1000000"),
	}
	components := parseBalanceComponents("0x200000")
	if got := evaluateExpectedReserve(components, expected, constants.TRON); got != reserveIssueNone {
		t.Fatalf("issue = %q, want none", got)
	}
}

func TestEvaluateExpectedReserveRejectsUnreadableComponent(t *testing.T) {
	token := "0x1111111111111111111111111111111111111111"
	expected := reserveExpectedBalance{
		Token:      &token,
		Symbol:     "USDT",
		Decimals:   6,
		BalanceRaw: mustBigInt("1000000"),
	}
	components := parseBalanceComponents("USDT:0.000000000001000000")
	if got := evaluateExpectedReserve(components, expected, constants.Ethereum); got != reserveIssueUnreadable {
		t.Fatalf("issue = %q, want %q", got, reserveIssueUnreadable)
	}
}

func TestReserveServiceOpenJobCreatesScopedReconciliation(t *testing.T) {
	db := openReserveReconciliationPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ReconciliationJob{}); err != nil {
		t.Fatalf("automigrate reconciliation jobs: %v", err)
	}

	ctx := context.Background()
	merchantID := uuid.New()
	service := &ReserveService{ReconciliationRepo: repositories.NewReconciliationRepo(db)}

	created, err := service.openJob(ctx, constants.Ethereum, string(reserveIssueDeficit), merchantID, "eth")
	if err != nil {
		t.Fatalf("open reserve reconciliation job: %v", err)
	}
	if !created {
		t.Fatal("first reserve reconciliation job should be created")
	}

	var job models.ReconciliationJob
	if err := db.WithContext(ctx).First(&job, "merchant_id = ? AND resource_type = ?", merchantID, "reserve_balance").Error; err != nil {
		t.Fatalf("load reserve reconciliation job: %v", err)
	}
	if job.Status != models.ReconciliationStatusOpen || job.ChainID != constants.Ethereum {
		t.Fatalf("reserve reconciliation status/chain = %#v", job)
	}
	if job.MerchantID == nil || *job.MerchantID != merchantID || job.DomainID != nil {
		t.Fatalf("reserve reconciliation tenant scope = %#v", job)
	}
	if job.ScopeKey != "reserve:deficit:1:"+merchantID.String()+":ETH" || job.ResourceID != "ETH" {
		t.Fatalf("reserve reconciliation scope = key %q resource %q", job.ScopeKey, job.ResourceID)
	}
	for _, token := range []string{
		`"kind":"deficit"`,
		`"merchant_id":"` + merchantID.String() + `"`,
		`"chain_id":1`,
		`"symbol":"ETH"`,
	} {
		if !strings.Contains(job.EvidenceJSON, token) {
			t.Fatalf("reserve reconciliation evidence missing %s: %s", token, job.EvidenceJSON)
		}
	}
	for _, token := range []string{merchantID.String(), "ETH"} {
		if !strings.Contains(job.AffectedResourceIDsJSON, token) {
			t.Fatalf("reserve reconciliation affected ids missing %s: %s", token, job.AffectedResourceIDsJSON)
		}
	}

	created, err = service.openJob(ctx, constants.Ethereum, string(reserveIssueDeficit), merchantID, "ETH")
	if err != nil {
		t.Fatalf("dedupe reserve reconciliation job: %v", err)
	}
	if created {
		t.Fatal("duplicate reserve reconciliation job should dedupe by active scope key")
	}

	var count int64
	if err := db.WithContext(ctx).Model(&models.ReconciliationJob{}).Where("scope_key = ?", job.ScopeKey).Count(&count).Error; err != nil {
		t.Fatalf("count reserve reconciliation jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("reserve reconciliation job count = %d, want 1", count)
	}
}

func mustBigInt(raw string) *big.Int {
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		panic(raw)
	}
	return value
}

func openReserveReconciliationPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("OUTBOX_TEST_DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		dsn = os.Getenv("MONEY_OUTBOX_TEST_DATABASE_URL")
	}
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set OUTBOX_TEST_DATABASE_URL to run reserve reconciliation Postgres tests")
	}

	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open admin postgres db: %v", err)
	}
	schemaName := "reserve_reconciliation_test_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	if err := adminDB.Exec(`CREATE SCHEMA "` + schemaName + `"`).Error; err != nil {
		t.Fatalf("create schema %s: %v", schemaName, err)
	}
	t.Cleanup(func() {
		_ = adminDB.Exec(`DROP SCHEMA IF EXISTS "` + schemaName + `" CASCADE`).Error
	})

	db, err := gorm.Open(postgres.Open(reserveReconciliationPostgresDSNWithSearchPath(dsn, schemaName)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open schema postgres db: %v", err)
	}
	return db
}

func reserveReconciliationPostgresDSNWithSearchPath(dsn string, schema string) string {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" {
		sep := " "
		if strings.Contains(dsn, "search_path=") {
			return dsn
		}
		if strings.HasSuffix(strings.TrimSpace(dsn), " ") {
			sep = ""
		}
		return dsn + sep + "search_path=" + schema
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
