package dbmigrations

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCanonicalBlockMoneyEventSequencePreflightUpgradesLegacyPostgres(t *testing.T) {
	db := openDBMigrationPostgresTestDB(t)

	legacySchema := []string{
		`CREATE TABLE blocks (
			id text PRIMARY KEY,
			chain_id bigint NOT NULL,
			number bigint NOT NULL,
			hash varchar(128) NOT NULL,
			parent_hash varchar(128) NOT NULL DEFAULT '',
			processed boolean NOT NULL DEFAULT false,
			canonical boolean NOT NULL DEFAULT true,
			status varchar(32) NOT NULL DEFAULT 'canonical',
			reorged_at timestamptz,
			superseded_by_hash varchar(128) NOT NULL DEFAULT '',
			correction_reason varchar(256) NOT NULL DEFAULT '',
			created_at timestamptz,
			updated_at timestamptz
		)`,
		`CREATE TABLE chain_states (
			chain_id bigint PRIMARY KEY,
			last_processed_block bigint NOT NULL DEFAULT 0,
			last_processed_hash varchar(128) NOT NULL DEFAULT '',
			last_processed_parent_hash varchar(128) NOT NULL DEFAULT '',
			last_confirmed_block bigint NOT NULL DEFAULT 0,
			continuity_status varchar(32) NOT NULL DEFAULT '',
			continuity_reason varchar(256) NOT NULL DEFAULT '',
			updated_at timestamptz
		)`,
		`CREATE TABLE chain_facts (
			id text PRIMARY KEY,
			event_id varchar(256) NOT NULL,
			chain_id bigint NOT NULL,
			block_number bigint NOT NULL,
			block_hash varchar(128) NOT NULL,
			status varchar(32) NOT NULL DEFAULT 'observed',
			finalized boolean NOT NULL DEFAULT false,
			reorged_at timestamptz,
			correction_reason varchar(256) NOT NULL DEFAULT '',
			updated_at timestamptz
		)`,
		`CREATE TABLE money_event_inboxes (
			id text PRIMARY KEY,
			event_id varchar(256) NOT NULL,
			consumer_name varchar(120) NOT NULL,
			resource_id varchar(256) NOT NULL DEFAULT '',
			status varchar(32) NOT NULL,
			locked_until timestamptz,
			last_error text NOT NULL DEFAULT '',
			failure_category varchar(80) NOT NULL DEFAULT '',
			updated_at timestamptz
		)`,
		`CREATE TABLE money_event_outboxes (
			id text PRIMARY KEY,
			merchant_id text NOT NULL,
			domain_id text NOT NULL,
			aggregate_type varchar(64) NOT NULL,
			aggregate_id varchar(256) NOT NULL,
			created_at timestamptz NOT NULL
		)`,
		`CREATE TABLE webhook_deliveries (
			id text PRIMARY KEY,
			domain_id text NOT NULL,
			target_url varchar(500) NOT NULL,
			created_at timestamptz NOT NULL
		)`,
		`CREATE TABLE domains (
			id text PRIMARY KEY,
			notification_mode varchar(16) NOT NULL DEFAULT 'webhook',
			nats_subject varchar(255) NOT NULL DEFAULT ''
		)`,
	}
	for _, statement := range legacySchema {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
	}

	if err := db.Exec(`INSERT INTO blocks
		(id, chain_id, number, hash, processed, canonical, status, created_at, updated_at)
		VALUES
		('older', 1, 100, '0xolder', true, true, 'canonical', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		('winner', 1, 100, '0xwinner', true, true, 'canonical', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z'),
		('single', 1, 101, '0xsingle', true, true, 'canonical', '2026-01-03T00:00:00Z', '2026-01-03T00:00:00Z'),
		('older-two', 1, 102, '0xolder-two', false, true, 'canonical', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		('winner-two', 1, 102, '0xwinner-two', true, true, 'canonical', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`).Error; err != nil {
		t.Fatalf("seed duplicate canonical blocks: %v", err)
	}
	if err := db.Exec(`INSERT INTO chain_states
		(chain_id, last_processed_block, last_processed_hash, last_processed_parent_hash,
		 last_confirmed_block, continuity_status, continuity_reason, updated_at)
		VALUES (1, 150, '0xtip', '0xparent', 145, 'ok', '', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("seed chain checkpoint: %v", err)
	}
	if err := db.Exec(`INSERT INTO chain_facts
		(id, event_id, chain_id, block_number, block_hash, status, finalized, updated_at)
		VALUES
		('loser-fact', 'loser-event', 1, 100, '0xolder', 'observed', true, CURRENT_TIMESTAMP),
		('winner-fact', 'winner-event', 1, 100, '0xwinner', 'observed', true, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("seed branch chain facts: %v", err)
	}
	if err := db.Exec(`INSERT INTO money_event_inboxes
		(id, event_id, consumer_name, resource_id, status, updated_at)
		VALUES
		('loser-inbox', 'loser-event', 'deposit_fact_processor', 'loser-fact', 'received', CURRENT_TIMESTAMP),
		('winner-inbox', 'winner-event', 'deposit_fact_processor', 'winner-fact', 'received', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("seed branch deposit inbox: %v", err)
	}
	if err := db.Exec(`INSERT INTO money_event_outboxes
		(id, merchant_id, domain_id, aggregate_type, aggregate_id, created_at)
		VALUES
		('second', 'merchant-a', 'domain-a', 'transaction', 'aggregate-a', '2026-01-02T00:00:00Z'),
		('first', 'merchant-a', 'domain-a', 'transaction', 'aggregate-a', '2026-01-01T00:00:00Z'),
		('other-scope', 'merchant-b', 'domain-a', 'transaction', 'aggregate-a', '2026-01-03T00:00:00Z')`).Error; err != nil {
		t.Fatalf("seed legacy outbox: %v", err)
	}
	if err := db.Exec(`INSERT INTO domains (id, notification_mode, nats_subject)
		VALUES ('legacy-domain', 'nats', 'merchant.current.events')`).Error; err != nil {
		t.Fatalf("seed legacy domain: %v", err)
	}
	if err := db.Exec(`INSERT INTO webhook_deliveries (id, domain_id, target_url, created_at)
		VALUES
		('legacy-delivery', 'legacy-domain', 'https://original.example/webhook', CURRENT_TIMESTAMP),
		('legacy-nats-delivery', 'legacy-domain', 'tls://original-nats.example:4222', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("seed legacy webhook delivery: %v", err)
	}

	if err := Prepare(t.Context(), db); err != nil {
		t.Fatalf("prepare migration: %v", err)
	}
	// A second pass must be harmless; production jobs can safely retry after
	// losing their post-migration evidence update.
	if err := Prepare(t.Context(), db); err != nil {
		t.Fatalf("prepare migration idempotently: %v", err)
	}

	type blockState struct {
		ID               string
		Canonical        bool
		Status           string
		SupersededByHash string
		CorrectionReason string
	}
	var blocks []blockState
	if err := db.Raw(`SELECT id, canonical, status, superseded_by_hash, correction_reason
		FROM blocks WHERE chain_id = 1 AND number = 100 ORDER BY id`).Scan(&blocks).Error; err != nil {
		t.Fatalf("load reconciled blocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("reconciled blocks = %#v", blocks)
	}
	byID := make(map[string]blockState, len(blocks))
	for _, block := range blocks {
		byID[block.ID] = block
	}
	if !byID["winner"].Canonical {
		t.Fatalf("deterministic winner = %#v, want canonical", byID["winner"])
	}
	loser := byID["older"]
	if loser.Canonical || loser.Status != "reorged" || loser.SupersededByHash != "0xwinner" || !strings.Contains(loser.CorrectionReason, "migration_duplicate_canonical_height") {
		t.Fatalf("reconciled loser = %#v", loser)
	}
	var facts []struct {
		ID               string
		Status           string
		CorrectionReason string
	}
	if err := db.Raw(`SELECT id, status, correction_reason FROM chain_facts ORDER BY id`).Scan(&facts).Error; err != nil {
		t.Fatalf("load reconciled chain facts: %v", err)
	}
	factsByID := make(map[string]struct {
		ID               string
		Status           string
		CorrectionReason string
	}, len(facts))
	for _, fact := range facts {
		factsByID[fact.ID] = fact
	}
	if loserFact := factsByID["loser-fact"]; loserFact.Status != "reorged" || !strings.Contains(loserFact.CorrectionReason, "migration_orphan_duplicate_canonical_height") {
		t.Fatalf("loser fact was not quarantined: %#v", loserFact)
	}
	if winnerFact := factsByID["winner-fact"]; winnerFact.Status != "observed" || winnerFact.CorrectionReason != "" {
		t.Fatalf("winner fact was changed: %#v", winnerFact)
	}
	var inboxes []struct {
		ID              string
		Status          string
		FailureCategory string
	}
	if err := db.Raw(`SELECT id, status, failure_category FROM money_event_inboxes ORDER BY id`).Scan(&inboxes).Error; err != nil {
		t.Fatalf("load reconciled deposit inbox: %v", err)
	}
	inboxByID := make(map[string]struct {
		ID              string
		Status          string
		FailureCategory string
	}, len(inboxes))
	for _, inbox := range inboxes {
		inboxByID[inbox.ID] = inbox
	}
	if loserInbox := inboxByID["loser-inbox"]; loserInbox.Status != "dead_letter" || loserInbox.FailureCategory != "migration_orphan_branch" {
		t.Fatalf("loser inbox was not quarantined: %#v", loserInbox)
	}
	if winnerInbox := inboxByID["winner-inbox"]; winnerInbox.Status != "received" {
		t.Fatalf("winner inbox was changed: %#v", winnerInbox)
	}

	var chainState struct {
		LastProcessedBlock      int64
		LastProcessedHash       string
		LastProcessedParentHash string
		LastConfirmedBlock      int64
		ContinuityStatus        string
		ContinuityReason        string
	}
	if err := db.Raw(`SELECT last_processed_block, last_processed_hash,
		last_processed_parent_hash, last_confirmed_block, continuity_status, continuity_reason
		FROM chain_states WHERE chain_id = 1`).Scan(&chainState).Error; err != nil {
		t.Fatalf("load rewound chain state: %v", err)
	}
	if chainState.LastProcessedBlock != 36 || chainState.LastConfirmedBlock != 36 || chainState.LastProcessedHash != "" || chainState.LastProcessedParentHash != "" || chainState.ContinuityStatus != "rollback_required" || !strings.Contains(chainState.ContinuityReason, "duplicate canonical height 100") {
		t.Fatalf("rewound chain state = %#v", chainState)
	}

	var operationalGate struct {
		ID        string
		ChainID   int64
		Mode      string
		Reason    string
		UpdatedBy string
		CreatedAt time.Time
		UpdatedAt time.Time
	}
	if err := db.Raw(`SELECT id::text AS id, chain_id, mode, reason, updated_by, created_at, updated_at
		FROM network_operational_states WHERE chain_id = 1`).Scan(&operationalGate).Error; err != nil {
		t.Fatalf("load migration maintenance gate: %v", err)
	}
	if operationalGate.ID == "" || operationalGate.ChainID != 1 || operationalGate.Mode != "maintenance" ||
		operationalGate.Reason != canonicalBlockMaintenanceReason ||
		operationalGate.UpdatedBy != canonicalBlockMaintenanceUpdatedBy ||
		operationalGate.CreatedAt.IsZero() || operationalGate.UpdatedAt.IsZero() {
		t.Fatalf("migration maintenance gate = %#v", operationalGate)
	}
	assertMigrationIndex(t, db, "ux_network_operational_states_chain_id", true, "chain_id")
	assertMigrationIndex(t, db, "idx_network_operational_states_mode", false, "mode")

	var sequences []struct {
		ID       string
		Sequence int64
	}
	if err := db.Raw(`SELECT id, sequence FROM money_event_outboxes ORDER BY id`).Scan(&sequences).Error; err != nil {
		t.Fatalf("load outbox sequences: %v", err)
	}
	wantSequences := map[string]int64{"first": 1, "second": 2, "other-scope": 1}
	for _, row := range sequences {
		if row.Sequence != wantSequences[row.ID] {
			t.Fatalf("sequence for %s = %d, want %d", row.ID, row.Sequence, wantSequences[row.ID])
		}
	}

	var sequenceColumn struct {
		DataType      string
		IsNullable    string
		ColumnDefault *string
	}
	if err := db.Raw(`SELECT data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'money_event_outboxes'
		  AND column_name = 'sequence'`).Scan(&sequenceColumn).Error; err != nil {
		t.Fatalf("inspect sequence column: %v", err)
	}
	if sequenceColumn.DataType != "bigint" || sequenceColumn.IsNullable != "NO" || sequenceColumn.ColumnDefault == nil || !strings.Contains(*sequenceColumn.ColumnDefault, "0") {
		t.Fatalf("sequence column metadata = %#v", sequenceColumn)
	}
	var moneyEventLeaseTokenColumn struct {
		DataType   string
		IsNullable string
	}
	if err := db.Raw(`SELECT data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'money_event_outboxes'
		  AND column_name = 'lease_token'`).Scan(&moneyEventLeaseTokenColumn).Error; err != nil {
		t.Fatalf("inspect money-event lease token: %v", err)
	}
	if moneyEventLeaseTokenColumn.DataType != "uuid" || moneyEventLeaseTokenColumn.IsNullable != "YES" {
		t.Fatalf("money-event lease token metadata = %#v", moneyEventLeaseTokenColumn)
	}
	assertMigrationColumn(t, db, "webhook_deliveries", "notification_mode", "character varying", 16, "''")
	assertMigrationColumn(t, db, "webhook_deliveries", "target_subject", "character varying", 255, "''")
	var leaseTokenColumn struct {
		DataType   string
		IsNullable string
	}
	if err := db.Raw(`SELECT data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'webhook_deliveries'
		  AND column_name = 'lease_token'`).Scan(&leaseTokenColumn).Error; err != nil {
		t.Fatalf("inspect webhook lease token: %v", err)
	}
	if leaseTokenColumn.DataType != "uuid" || leaseTokenColumn.IsNullable != "YES" {
		t.Fatalf("webhook lease token metadata = %#v", leaseTokenColumn)
	}
	var targetSnapshot struct {
		NotificationMode string
		TargetSubject    string
	}
	if err := db.Raw(`SELECT notification_mode, target_subject
		FROM webhook_deliveries WHERE id = 'legacy-delivery'`).Scan(&targetSnapshot).Error; err != nil {
		t.Fatalf("load legacy delivery target snapshot: %v", err)
	}
	if targetSnapshot.NotificationMode != "webhook" || targetSnapshot.TargetSubject != "" {
		t.Fatalf("legacy delivery target snapshot = %#v", targetSnapshot)
	}
	if err := db.Raw(`SELECT notification_mode, target_subject
		FROM webhook_deliveries WHERE id = 'legacy-nats-delivery'`).Scan(&targetSnapshot).Error; err != nil {
		t.Fatalf("load legacy NATS delivery target snapshot: %v", err)
	}
	if targetSnapshot.NotificationMode != "nats" || targetSnapshot.TargetSubject != "merchant.current.events" {
		t.Fatalf("legacy NATS target snapshot = %#v", targetSnapshot)
	}

	assertMigrationIndex(t, db, "idx_money_event_outbox_aggregate_sequence", false, "merchant_id", "domain_id", "aggregate_type", "aggregate_id", "sequence")
	assertMigrationIndex(t, db, "ux_money_event_outbox_aggregate_sequence", true, "merchant_id", "domain_id", "aggregate_type", "aggregate_id", "sequence", "sequence > 0")
	assertMigrationIndex(t, db, "ux_blocks_one_canonical_height", true, "chain_id", "number", "canonical")
	if err := db.Exec(`INSERT INTO money_event_outboxes
		(id, merchant_id, domain_id, aggregate_type, aggregate_id, sequence, created_at)
		VALUES ('duplicate-sequence', 'merchant-a', 'domain-a', 'transaction', 'aggregate-a', 1, CURRENT_TIMESTAMP)`).Error; err == nil {
		t.Fatal("aggregate sequence unique index accepted a duplicate positive sequence")
	}

	if err := db.Exec(`INSERT INTO blocks
		(id, chain_id, number, hash, processed, canonical, status, created_at, updated_at)
		VALUES ('forbidden', 1, 100, '0xforbidden', true, true, 'canonical', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err == nil {
		t.Fatal("partial unique index accepted a second canonical block at one height")
	}

	// Applying the migration again must never clear the gate. Once an operator
	// has reviewed replay/reconciliation evidence and explicitly reactivates the
	// network, a later idempotent prepare with no ambiguity must preserve that
	// human decision rather than silently changing operational mode either way.
	if err := db.Exec(`UPDATE network_operational_states
		SET mode = 'active', reason = '', updated_by = 'operator@example.com', updated_at = CURRENT_TIMESTAMP
		WHERE chain_id = 1`).Error; err != nil {
		t.Fatalf("simulate privileged operator reactivation: %v", err)
	}
	if err := Prepare(t.Context(), db); err != nil {
		t.Fatalf("prepare after explicit operator reactivation: %v", err)
	}
	if err := db.Raw(`SELECT id::text AS id, chain_id, mode, reason, updated_by, created_at, updated_at
		FROM network_operational_states WHERE chain_id = 1`).Scan(&operationalGate).Error; err != nil {
		t.Fatalf("reload operator-reactivated state: %v", err)
	}
	if operationalGate.Mode != "active" || operationalGate.Reason != "" || operationalGate.UpdatedBy != "operator@example.com" {
		t.Fatalf("idempotent migration overwrote operator reactivation: %#v", operationalGate)
	}
}

func TestCanonicalBlockMigrationFailsClosedForIncompatiblePartialGateSchema(t *testing.T) {
	db := openDBMigrationPostgresTestDB(t)
	for _, statement := range []string{
		`CREATE TABLE blocks (
			id text PRIMARY KEY,
			chain_id bigint NOT NULL,
			number bigint NOT NULL,
			hash varchar(128) NOT NULL,
			processed boolean NOT NULL DEFAULT false,
			canonical boolean NOT NULL DEFAULT true,
			status varchar(32) NOT NULL DEFAULT 'canonical',
			reorged_at timestamptz,
			superseded_by_hash varchar(128) NOT NULL DEFAULT '',
			correction_reason varchar(256) NOT NULL DEFAULT '',
			created_at timestamptz,
			updated_at timestamptz
		)`,
		// Deliberately incompatible partial artifact: Prepare must error instead
		// of reconciling blocks without a usable operational safety gate.
		`CREATE TABLE network_operational_states (
			id uuid PRIMARY KEY,
			chain_id bigint NOT NULL
		)`,
		`INSERT INTO blocks (id, chain_id, number, hash, canonical, status)
		 VALUES ('a', 1, 10, '0xa', true, 'canonical'),
		        ('b', 1, 10, '0xb', true, 'canonical')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed incompatible partial schema: %v", err)
		}
	}

	err := Prepare(t.Context(), db)
	if err == nil || !strings.Contains(err.Error(), "ensure_network_operational_state_gate_schema") {
		t.Fatalf("prepare error = %v, want explicit gate-schema failure", err)
	}
	var canonicalCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM blocks WHERE chain_id = 1 AND number = 10 AND canonical IS TRUE`).Scan(&canonicalCount).Error; err != nil {
		t.Fatalf("count canonical rows after failed migration: %v", err)
	}
	if canonicalCount != 2 {
		t.Fatalf("failed gate preparation partially reconciled canonical history: count=%d", canonicalCount)
	}
}

func TestCanonicalBlockMigrationGatesExistingOperationalStateIncrementalPostgres(t *testing.T) {
	db := openDBMigrationPostgresTestDB(t)
	existingID := uuid.New()
	statements := []string{
		`CREATE TABLE blocks (
			id text PRIMARY KEY,
			chain_id bigint NOT NULL,
			number bigint NOT NULL,
			hash varchar(128) NOT NULL,
			processed boolean NOT NULL DEFAULT false,
			canonical boolean NOT NULL DEFAULT true,
			status varchar(32) NOT NULL DEFAULT 'canonical',
			reorged_at timestamptz,
			superseded_by_hash varchar(128) NOT NULL DEFAULT '',
			correction_reason varchar(256) NOT NULL DEFAULT '',
			created_at timestamptz,
			updated_at timestamptz
		)`,
		`CREATE TABLE network_operational_states (
			id uuid PRIMARY KEY,
			chain_id bigint NOT NULL,
			mode varchar(32) NOT NULL DEFAULT 'active',
			reason varchar(500) NOT NULL DEFAULT '',
			updated_by varchar(255) NOT NULL DEFAULT '',
			created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO blocks (id, chain_id, number, hash, processed, canonical, status, created_at, updated_at)
		 VALUES ('old', 1, 44, '0xold', false, true, 'canonical', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		        ('new', 1, 44, '0xnew', true, true, 'canonical', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed existing incremental schema: %v", err)
		}
	}
	if err := db.Exec(`INSERT INTO network_operational_states
		(id, chain_id, mode, reason, updated_by, created_at, updated_at)
		VALUES (?, 1, 'active', '', 'operator-before-migration', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, existingID).Error; err != nil {
		t.Fatalf("seed existing active operational row: %v", err)
	}

	if err := Prepare(t.Context(), db); err != nil {
		t.Fatalf("prepare existing incremental schema: %v", err)
	}
	var gate struct {
		ID        uuid.UUID
		Mode      string
		Reason    string
		UpdatedBy string
		CreatedAt time.Time
	}
	if err := db.Raw(`SELECT id, mode, reason, updated_by, created_at
		FROM network_operational_states WHERE chain_id = 1`).Scan(&gate).Error; err != nil {
		t.Fatalf("load upgraded existing gate: %v", err)
	}
	if gate.ID != existingID || gate.Mode != "maintenance" || gate.Reason != canonicalBlockMaintenanceReason ||
		gate.UpdatedBy != canonicalBlockMaintenanceUpdatedBy ||
		!gate.CreatedAt.Equal(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("upgraded existing operational gate = %#v", gate)
	}
	var canonicalCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM blocks WHERE chain_id = 1 AND number = 44 AND canonical IS TRUE`).Scan(&canonicalCount).Error; err != nil {
		t.Fatalf("count reconciled existing-schema blocks: %v", err)
	}
	if canonicalCount != 1 {
		t.Fatalf("canonical rows after existing-schema migration = %d, want one", canonicalCount)
	}

	if err := Prepare(t.Context(), db); err != nil {
		t.Fatalf("prepare existing incremental schema idempotently: %v", err)
	}
	var mode string
	if err := db.Raw(`SELECT mode FROM network_operational_states WHERE chain_id = 1`).Scan(&mode).Error; err != nil {
		t.Fatalf("reload idempotent existing gate: %v", err)
	}
	if mode != "maintenance" {
		t.Fatalf("idempotent prepare cleared existing maintenance gate: mode=%q", mode)
	}
}

func assertMigrationColumn(t *testing.T, db *gorm.DB, table, name, dataType string, maxLength int64, defaultFragment string) {
	t.Helper()
	var column struct {
		DataType               string
		IsNullable             string
		CharacterMaximumLength *int64
		ColumnDefault          *string
	}
	if err := db.Raw(`SELECT data_type, is_nullable, character_maximum_length, column_default
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = ?
		  AND column_name = ?`, table, name).Scan(&column).Error; err != nil {
		t.Fatalf("inspect %s.%s: %v", table, name, err)
	}
	if column.DataType != dataType || column.IsNullable != "NO" || column.CharacterMaximumLength == nil || *column.CharacterMaximumLength != maxLength || column.ColumnDefault == nil || !strings.Contains(*column.ColumnDefault, defaultFragment) {
		t.Fatalf("%s.%s metadata = %#v", table, name, column)
	}
}

func assertMigrationIndex(t *testing.T, db *gorm.DB, name string, wantUnique bool, fragments ...string) {
	t.Helper()
	var index struct {
		Definition string
		Predicate  string
		Unique     bool
	}
	if err := db.Raw(`SELECT pg_get_indexdef(i.indexrelid) AS definition,
		COALESCE(pg_get_expr(i.indpred, i.indrelid), '') AS predicate,
		i.indisunique AS unique
		FROM pg_class table_class
		JOIN pg_namespace namespace ON namespace.oid = table_class.relnamespace
		JOIN pg_index i ON i.indrelid = table_class.oid
		JOIN pg_class index_class ON index_class.oid = i.indexrelid
		WHERE namespace.nspname = current_schema()
		  AND index_class.relname = ?`, name).Scan(&index).Error; err != nil {
		t.Fatalf("inspect index %s: %v", name, err)
	}
	if index.Definition == "" || index.Unique != wantUnique {
		t.Fatalf("index %s metadata = %#v, want unique=%t", name, index, wantUnique)
	}
	combined := strings.ToLower(index.Definition + " " + index.Predicate)
	for _, fragment := range fragments {
		if !strings.Contains(combined, strings.ToLower(fragment)) {
			t.Fatalf("index %s definition %q missing %q", name, combined, fragment)
		}
	}
}

func openDBMigrationPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("OUTBOX_TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("MONEY_OUTBOX_TEST_DATABASE_URL"))
	}
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	}
	if dsn == "" {
		t.Skip("set OUTBOX_TEST_DATABASE_URL to run dbmigration PostgreSQL tests")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect test PostgreSQL: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(time.Minute)

	schema := "dbmigrations_test_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	quotedSchema := fmt.Sprintf(`"%s"`, schema)
	if err := db.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	if err := db.Exec("SET search_path TO " + quotedSchema).Error; err != nil {
		t.Fatalf("select test schema: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Exec(`SET search_path TO public`).Error
		_ = db.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error
		_ = sqlDB.Close()
	})
	return db
}
