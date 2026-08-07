package dbmigrations

import (
	"context"
	"core/models"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

type Artifact struct {
	ID                string
	Models            []any
	PreAutoMigrate    []SQLStep
	Summary           string
	ForwardPlan       string
	LockImpact        string
	Backfill          string
	Rollback          string
	VerificationQuery string
	Verify            string
}

// SQLStep is reserved for invariants that GORM cannot establish safely by
// itself. In particular, a unique index cannot be created until legacy
// duplicates have been reconciled. Steps are executed transactionally before
// any artifact model is passed to AutoMigrate.
type SQLStep struct {
	Name           string
	Dialect        string
	RequiredTables []string
	Statements     []string
}

func Artifacts() []Artifact {
	return []Artifact{
		{
			ID: "202606280001_provider_health_wallet_address_lookup",
			Models: []any{
				&models.ProviderHealthSnapshot{},
				&models.WalletAddressLookup{},
			},
			Summary:           "Registers provider health snapshots and normalized wallet address lookup models for Epic 5 provider resilience and large wallet-set lookup.",
			ForwardPlan:       "Forward-only additive GORM migration for provider_health_snapshots and wallet_address_lookups.",
			LockImpact:        "GORM creates or updates the two owned tables and their indexes in a controlled migration job, separate from application startup.",
			Backfill:          "Wallet address lookup backfill is performed by WalletAddressLookupRepo.BackfillWallets using GORM batches and conflict checks.",
			Rollback:          "Rollback deploys prior code and leaves these additive tables in place, or drops the two tables through an operator-approved GORM migration if no reader remains.",
			VerificationQuery: "database.VerifySchema must report provider_health_snapshots and wallet_address_lookups columns and indexes present.",
			Verify:            "Run database.VerifySchema plus provider health and wallet address lookup repository tests after applying.",
		},
		{
			ID: "202606280002_wallet_address_lifecycle_pool",
			Models: []any{
				&models.WalletAddressReservation{},
				&models.WalletAddress{},
				&models.WalletAddressGapScanCursor{},
				&models.WalletAddressGapScanAnomaly{},
			},
			Summary:           "Registers normalized wallet address lifecycle, reservation pool, and gap-limit scan state models for Epic 6 address safety.",
			ForwardPlan:       "Forward-only additive GORM migration for wallet address lifecycle and gap-scan tables.",
			LockImpact:        "GORM creates additive wallet address pool tables and unique indexes in a controlled migration job; index reservation locking is handled at write time.",
			Backfill:          "WalletAddressRepo.BackfillWallets populates reservations and normalized address rows from legacy wallets and wallet_address_lookups with conflict checks.",
			Rollback:          "Rollback deploys prior readers and leaves additive lifecycle tables in place, or drops them through an operator-approved GORM migration after lookup parity is no longer required.",
			VerificationQuery: "database.VerifySchema must report wallet address lifecycle columns and unique indexes present.",
			Verify:            "Run database.VerifySchema plus wallet address reservation, backfill, gap scan, and reuse policy tests after applying.",
		},
		{
			ID: "202606280003_immutable_ledger_journal_projection",
			Models: []any{
				&models.LedgerBalanceProjection{},
			},
			Summary:           "Registers ledger-owned balance projection schema for immutable ledger journal rebuilds.",
			ForwardPlan:       "Forward-only additive GORM migration for ledger balance projections and ledger guard reconciliation.",
			LockImpact:        "GORM creates the additive ledger balance projection table and indexes; ledger entry immutability trigger reconciliation runs through database.ApplyGORMMigrations.",
			Backfill:          "Run LedgerRepo.RebuildBalanceProjections after deployment to project active ledger balances from ledger_entries.",
			Rollback:          "Rollback deploys prior readers and leaves the additive projection table in place, or drops it through an operator-approved GORM migration after readers no longer use it.",
			VerificationQuery: "database.VerifySchema must report ledger balance projection indexes and ledger entry guards present.",
			Verify:            "Run database.VerifySchema plus ledger append-only, projection rebuild, and invariant reconciliation repository tests after applying.",
		},
		{
			ID: "202606280004_deposit_settlement_allocations",
			Models: []any{
				&models.PaymentDepositAllocation{},
				&models.ChainFact{},
				&models.Deposit{},
				&models.PaymentSession{},
			},
			Summary:           "Registers deposit observation, memo matching, payment settlement policy, and payment deposit allocation schema for aggregate deposit settlement.",
			ForwardPlan:       "Forward-only additive GORM migration for chain fact, deposit, payment session and allocation fields.",
			LockImpact:        "GORM applies additive columns and the payment allocation unique index in a controlled migration job; existing payment/deposit rows remain readable.",
			Backfill:          "Existing sessions default to single-transaction settlement policy; allocation rows are created only for newly processed finalized deposits after deployment.",
			Rollback:          "Rollback deploys prior code and leaves additive columns/table in place, or drops them through an operator-approved GORM migration after allocation readers are disabled.",
			VerificationQuery: "database.VerifySchema must report chain fact, deposit, payment session and allocation schema present.",
			Verify:            "Run database.VerifySchema plus chain fact, deposit, payment aggregation, and memo matching repository tests after applying.",
		},
		{
			ID: "202606280005_durable_outbound_transaction_manager",
			Models: []any{
				&models.OutboundTransaction{},
				&models.OutboundChainResourceReservation{},
			},
			Summary:           "Registers durable outbound transaction state and chain resource reservation schema for withdrawal, refund and sweep broadcasts.",
			ForwardPlan:       "Forward-only additive GORM migration for outbound transaction manager tables.",
			LockImpact:        "GORM creates additive outbound transaction and resource reservation tables with indexes; existing withdrawal/refund/sweep rows remain readable.",
			Backfill:          "Existing processing withdrawals/refunds with tx hashes continue through finality polling; new approved outbounds create manager rows before signing.",
			Rollback:          "Rollback deploys prior code and leaves additive outbound tables in place, or drops them through an operator-approved GORM migration after workers are disabled.",
			VerificationQuery: "database.VerifySchema must report outbound transaction and chain resource reservation schema present.",
			Verify:            "Run database.VerifySchema plus outbound transaction repository, chain resource reservation, dealer approval and sweep worker tests after applying.",
		},
		{
			ID: "202606290006_reliability_substrate_inbox_worker_leases",
			Models: []any{
				&models.MoneyEventInbox{},
				&models.WorkerLease{},
			},
			Summary:           "Registers durable inbound event consumer inboxes and worker lease rows for Epic 6 reliability recovery semantics.",
			ForwardPlan:       "Forward-only additive GORM migration for inbox and worker lease reliability tables.",
			LockImpact:        "GORM creates additive reliability tables and indexes in a controlled migration job; worker lease contention is handled by advisory and row locks at runtime.",
			Backfill:          "No historical backfill is required; inbox rows are created on first consumer processing and worker leases are acquired by active processes.",
			Rollback:          "Rollback deploys prior workers and leaves additive reliability tables in place, or drops them through an operator-approved GORM migration after no worker references remain.",
			VerificationQuery: "database.VerifySchema must report money event inbox and worker lease columns and indexes present.",
			Verify:            "Run database.VerifySchema plus money event inbox, worker lease, operational metrics, and worker lease contract tests after applying.",
		},
		{
			ID: "202606290007_chain_scanner_continuity_governance",
			Models: []any{
				&models.ChainState{},
				&models.Block{},
			},
			Summary:           "Registers chain scanner continuity checkpoints, canonical block records, and replay governance fields for Epic 7 provider consistency.",
			ForwardPlan:       "Forward-only additive GORM migration for chain_states continuity columns and blocks canonical history indexes.",
			LockImpact:        "GORM adds nullable/defaulted chain state fields and block indexes in an explicit migration job; production operators should apply during a low write window and verify scanner pause before large block history changes.",
			Backfill:          "Existing chain state rows keep current checkpoints; operators set scanner_start_block and continuity_status through controlled replay jobs when historical repair is required.",
			Rollback:          "Rollback deploys prior scanner code and leaves additive checkpoint/block fields in place, or removes them only through an operator-approved migration after replay readers are disabled.",
			VerificationQuery: "database.VerifySchema must report chain_states continuity columns and blocks unique chain/hash indexes present.",
			Verify:            "Run database.VerifySchema plus chain scanner checkpoint, block reorg, provider failover and historical rescan tests after applying.",
		},
		{
			ID: "202606290008_webhook_ordering_resource_sequences",
			Models: []any{
				&models.WebhookDelivery{},
				&models.WebhookResourceSequence{},
			},
			Summary:           "Registers per-resource webhook delivery sequencing and idempotency metadata for ordered merchant callback delivery.",
			ForwardPlan:       "Forward-only additive GORM migration for webhook delivery metadata columns and webhook_resource_sequences.",
			LockImpact:        "GORM adds webhook delivery metadata indexes and a small sequence table; large delivery tables should be migrated with workers paused or drained before index creation.",
			Backfill:          "Existing queued deliveries may keep sequence zero and are claimed after new per-resource sequence rows are created for new events.",
			Rollback:          "Rollback deploys prior webhook code and leaves additive sequence metadata in place, or drops it after all new workers and consumers are disabled.",
			VerificationQuery: "database.VerifySchema must report webhook_deliveries ordering columns and webhook_resource_sequences unique index present.",
			Verify:            "Run database.VerifySchema plus webhook delivery repository, notifier metadata and ordering contract tests after applying.",
		},
		{
			ID: "202606290009_merchant_api_security_controls",
			Models: []any{
				&models.Domain{},
				&models.APIRateLimitCounter{},
			},
			Summary:           "Registers merchant API scopes, IP allowlist, secret rotation metadata and distributed API rate-limit counters.",
			ForwardPlan:       "Forward-only additive GORM migration for domain API security columns and api_rate_limit_counters.",
			LockImpact:        "GORM adds domain security columns and rate-limit counter indexes in a controlled migration job; rate-limit writes start only after the application deploy points at the DB-backed limiter.",
			Backfill:          "Existing domains receive default API scopes through model defaults and repository updates; no historical API counter backfill is required.",
			Rollback:          "Rollback deploys prior API auth code and leaves additive security columns/counters in place, or drops them after partner API traffic is confirmed on prior code.",
			VerificationQuery: "database.VerifySchema must report domain API security columns and api_rate_limit_counters indexes present.",
			Verify:            "Run database.VerifySchema plus merchant API auth, IP allowlist, signed scope and distributed rate-limit repository tests after applying.",
		},
		{
			ID: "202606290010_sweep_batching_gas_funding_recovery",
			Models: []any{
				&models.SweepJob{},
			},
			Summary:           "Registers sweep batch planning metadata, gas funding retry limits and operator recovery state on durable sweep jobs.",
			ForwardPlan:       "Forward-only additive GORM migration for sweep_jobs batch, prefund max-attempt and operator recovery columns.",
			LockImpact:        "GORM adds nullable/defaulted sweep job columns and secondary indexes in a controlled migration job; pause sweep workers before applying to a large active queue.",
			Backfill:          "Existing sweep jobs keep empty batch/recovery metadata and receive prefund max-attempt defaults on new writes; operators may attach batch metadata only to pending or failed jobs.",
			Rollback:          "Rollback deploys prior sweep worker code and leaves additive metadata in place, or removes it through an operator-approved migration after all recovery readers are disabled.",
			VerificationQuery: "database.VerifySchema must report sweep_jobs batch, prefund limit and operator recovery columns and indexes present.",
			Verify:            "Run database.VerifySchema plus sweep planner, sweep job repository, prefund and sweep worker recovery tests after applying.",
		},
		{
			ID: "202607130011_payment_asset_metadata_product_snapshot_notification_targets",
			Models: []any{
				&models.Domain{},
				&models.Product{},
				&models.PaymentSession{},
			},
			Summary:           "Registers payment-link default asset, immutable product snapshots for payment sessions, and per-domain webhook/NATS notification target settings.",
			ForwardPlan:       "Forward-only additive GORM migration for domain notification routing, product default asset metadata, and payment session product_snapshot JSONB.",
			LockImpact:        "GORM adds nullable/defaulted columns and lightweight indexes to domains, products, and payment_sessions; existing payment and delivery rows remain readable.",
			Backfill:          "Existing domains retain webhook mode and existing webhook settings; existing products keep an empty default asset and existing payment sessions keep an empty product snapshot.",
			Rollback:          "Rollback deploys prior readers and leaves additive routing and metadata columns in place, or removes them through an operator-approved migration after all new readers are disabled.",
			VerificationQuery: "database.VerifySchema must report domain notification columns, product default asset columns, and payment_sessions.product_snapshot present.",
			Verify:            "Run database.VerifySchema plus payment-link asset selection, product snapshot webhook, domain routing, and NATS notifier contract tests after applying.",
		},
		{
			ID: "202607130012_wallet_chiliz_spicy_partial_unique",
			Models: []any{
				&models.Wallet{},
			},
			Summary:           "Allows multiple wallets without an optional Chiliz Spicy address while preserving uniqueness for populated addresses.",
			ForwardPlan:       "Reconcile idx_wallets_chiliz_spicy_address as a transactional partial unique index restricted to non-empty addresses.",
			LockImpact:        "The controlled migration rebuilds one small wallet index transactionally and briefly takes the PostgreSQL index/table locks required by DROP INDEX and CREATE UNIQUE INDEX.",
			Backfill:          "No row backfill is required; existing empty values are excluded and existing non-empty values remain protected by uniqueness.",
			Rollback:          "Rollback application code while leaving the partial unique index in place; restoring the stale full index is unsafe after multiple empty values exist.",
			VerificationQuery: "database.VerifySchema must report idx_wallets_chiliz_spicy_address as unique with predicate chiliz_spicy_address <> ''.",
			Verify:            "Run the wallet Chiliz Spicy partial-index migration regression and create multiple wallets with empty optional addresses.",
		},
		{
			ID: "202607130013_network_operational_states",
			Models: []any{
				&models.NetworkOperationalState{},
			},
			Summary:           "Registers the persisted per-network operational mode used to disable deposits, withdrawals, or both during maintenance.",
			ForwardPlan:       "Forward-only additive GORM migration for network_operational_states with one unique row per supported chain.",
			LockImpact:        "GORM creates one small additive table, its unique chain index, mode index, and mode check constraint without changing active transaction tables.",
			Backfill:          "No row backfill is required; absent supported-chain rows are interpreted as active until an administrator persists an explicit mode.",
			Rollback:          "Rollback application readers and leave the additive table in place, or drop it after all maintenance-mode readers and writers are disabled.",
			VerificationQuery: "database.VerifySchema must report network_operational_states columns, chain uniqueness, mode index, and mode check constraint present.",
			Verify:            "Run database.VerifySchema plus network operational state model and repository default, validation, and upsert tests after applying.",
		},
		{
			ID: canonicalBlockMoneyEventSequenceMigrationID,
			Models: []any{
				&models.Block{},
				&models.MoneyEventOutbox{},
				&models.WebhookDelivery{},
				&models.NetworkOperationalState{},
			},
			PreAutoMigrate:    canonicalBlockMoneyEventSequencePreflight(),
			Summary:           "Adds durable aggregate ordering and immutable target snapshots to outbound delivery state, enforces exactly one canonical block per chain height, and gates affected networks until authoritative repair is acknowledged.",
			ForwardPlan:       "In one PostgreSQL transaction, pause competing writers, safely create the network operational gate table when upgrading a partial legacy schema, set every chain with duplicate canonical heights to maintenance, rewind its checkpoint, deterministically mark duplicate canonical block rows and their unprocessed chain facts reorged, quarantine their deposit inbox work, add and backfill money_event_outboxes.sequence plus relay lease fencing, add webhook delivery target snapshot and lease-fencing columns, then create the aggregate sequence and partial canonical-height indexes before GORM reconciliation.",
			LockImpact:        "The preflight takes SHARE ROW EXCLUSIVE locks on blocks, network_operational_states, money_event_outboxes, and webhook_deliveries and builds safety indexes; scanners and delivery workers must be paused and the migration must run in a low-write maintenance window.",
			Backfill:          "Existing outbox rows receive deterministic positive sequences partitioned by merchant, domain, aggregate type and aggregate id in created_at/id order. Legacy webhook delivery transport mode is inferred from its persisted target URL and NATS subject is recovered from the domain when available. Chains with duplicate canonical heights are rewound 64 blocks before the earliest ambiguity; each is durably forced to maintenance with migration evidence before facts on discarded branches and their deposit inbox work are quarantined for authoritative scanner replay and money-state reconciliation.",
			Rollback:          "Deploy prior code while leaving the additive sequence column, safety indexes, and migration-created maintenance gates in place. Never automatically reactivate an affected network; only a privileged operator may do so after replay, reconciliation, and evidence acknowledgement.",
			VerificationQuery: "Verify every chain marked rollback_required has network_operational_states.mode = maintenance, updated_by = migration, and the migration replay/reconciliation/operator-acknowledgement reason; verify money_event_outboxes.sequence and lease_token, webhook delivery target snapshots and lease_token, no duplicate canonical height, no observed fact on a discarded branch, and all required safety indexes.",
			Verify:            "Run database.VerifySchema, services/dbmigrations incremental and idempotency tests, admin network-state visibility tests, money-event outbox ordering tests, canonical block concurrency tests, and an authoritative scanner replay plus money-state reconciliation for every chain marked rollback_required before a privileged operator explicitly reactivates it.",
		},
	}
}

func LoadManifest() ([]Artifact, error) {
	return Artifacts(), nil
}

func Validate() error {
	return ValidateArtifacts(Artifacts())
}

func ValidateArtifacts(artifacts []Artifact) error {
	if len(artifacts) == 0 {
		return errors.New("migration manifest has no artifacts")
	}
	seen := make(map[string]struct{}, len(artifacts))
	ids := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.ID) == "" {
			return errors.New("migration artifact id is required")
		}
		if _, ok := seen[artifact.ID]; ok {
			return fmt.Errorf("duplicate migration artifact id %s", artifact.ID)
		}
		seen[artifact.ID] = struct{}{}
		ids = append(ids, artifact.ID)
		for label, value := range map[string]string{
			"summary":            artifact.Summary,
			"forward_plan":       artifact.ForwardPlan,
			"lock_impact":        artifact.LockImpact,
			"backfill":           artifact.Backfill,
			"rollback":           artifact.Rollback,
			"verification_query": artifact.VerificationQuery,
			"verify":             artifact.Verify,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("migration artifact %s missing %s", artifact.ID, label)
			}
		}
		if len(artifact.Models) == 0 {
			return fmt.Errorf("migration artifact %s has no GORM models", artifact.ID)
		}
		for stepIndex, step := range artifact.PreAutoMigrate {
			if strings.TrimSpace(step.Name) == "" {
				return fmt.Errorf("migration artifact %s pre-auto-migrate step %d has no name", artifact.ID, stepIndex)
			}
			if strings.TrimSpace(step.Dialect) == "" {
				return fmt.Errorf("migration artifact %s pre-auto-migrate step %s has no dialect", artifact.ID, step.Name)
			}
			if len(step.RequiredTables) == 0 {
				return fmt.Errorf("migration artifact %s pre-auto-migrate step %s has no required tables", artifact.ID, step.Name)
			}
			for _, table := range step.RequiredTables {
				if strings.TrimSpace(table) == "" {
					return fmt.Errorf("migration artifact %s pre-auto-migrate step %s has a blank required table", artifact.ID, step.Name)
				}
			}
			if len(step.Statements) == 0 {
				return fmt.Errorf("migration artifact %s pre-auto-migrate step %s has no statements", artifact.ID, step.Name)
			}
			for _, statement := range step.Statements {
				if strings.TrimSpace(statement) == "" {
					return fmt.Errorf("migration artifact %s pre-auto-migrate step %s has a blank statement", artifact.ID, step.Name)
				}
			}
		}
	}
	if !sort.StringsAreSorted(ids) {
		return errors.New("migration artifact ids must be sorted")
	}
	return nil
}

func LatestID() (string, error) {
	if err := Validate(); err != nil {
		return "", err
	}
	artifacts := Artifacts()
	return artifacts[len(artifacts)-1].ID, nil
}

func Apply(ctx context.Context, db *gorm.DB) error {
	artifacts := Artifacts()
	if err := ValidateArtifacts(artifacts); err != nil {
		return err
	}
	if err := prepareArtifacts(ctx, db, artifacts); err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if err := db.WithContext(ctx).AutoMigrate(artifact.Models...); err != nil {
			return fmt.Errorf("apply migration %s: %w", artifact.ID, err)
		}
	}
	return nil
}

// Prepare runs data reconciliation and defensive DDL that must precede
// AutoMigrate. It is safe on a blank database: a step is skipped until all of
// its required legacy tables exist, after which AutoMigrate creates the fresh
// schema directly from the models.
func Prepare(ctx context.Context, db *gorm.DB) error {
	artifacts := Artifacts()
	if err := ValidateArtifacts(artifacts); err != nil {
		return err
	}
	return prepareArtifacts(ctx, db, artifacts)
}

func prepareArtifacts(ctx context.Context, db *gorm.DB, artifacts []Artifact) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	for _, artifact := range artifacts {
		if len(artifact.PreAutoMigrate) == 0 {
			continue
		}
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for _, step := range artifact.PreAutoMigrate {
				if tx.Dialector == nil || tx.Dialector.Name() != step.Dialect {
					continue
				}
				ready := true
				for _, table := range step.RequiredTables {
					if !tx.Migrator().HasTable(table) {
						ready = false
						break
					}
				}
				if !ready {
					continue
				}
				for _, statement := range step.Statements {
					if err := tx.Exec(statement).Error; err != nil {
						return fmt.Errorf("step %s: %w", step.Name, err)
					}
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("prepare migration %s: %w", artifact.ID, err)
		}
	}
	return nil
}
