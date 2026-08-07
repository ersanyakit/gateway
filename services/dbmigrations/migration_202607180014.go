package dbmigrations

const (
	canonicalBlockMoneyEventSequenceMigrationID = "202607180014_canonical_block_money_event_sequence_invariants"
	canonicalBlockMaintenanceUpdatedBy          = "migration"
	canonicalBlockMaintenanceReason             = "migration " + canonicalBlockMoneyEventSequenceMigrationID + " detected duplicate canonical block history; authoritative scanner replay and money-state reconciliation must complete, and an authorized operator must acknowledge the evidence before explicitly reactivating this network"
)

func canonicalBlockMoneyEventSequencePreflight() []SQLStep {
	return []SQLStep{
		{
			// Prepare runs before AutoMigrate, including when an installation jumps
			// directly from an older artifact. Build the small safety table here so
			// duplicate history can never be reconciled without first creating the
			// durable deposit/withdrawal gate. If an existing partial table is
			// incompatible, the later index/upsert fails the whole transaction.
			Name:           "ensure_network_operational_state_gate_schema",
			Dialect:        "postgres",
			RequiredTables: []string{"blocks"},
			Statements: []string{
				`CREATE TABLE IF NOT EXISTS network_operational_states (
					id UUID NOT NULL,
					chain_id BIGINT NOT NULL,
					mode VARCHAR(32) NOT NULL DEFAULT 'active',
					reason VARCHAR(500) NOT NULL DEFAULT '',
					updated_by VARCHAR(255) NOT NULL DEFAULT '',
					created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
					updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
					CONSTRAINT network_operational_states_pkey PRIMARY KEY (id),
					CONSTRAINT network_operational_states_mode_check
						CHECK (mode IN ('active', 'deposits_off', 'withdrawals_off', 'maintenance'))
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_network_operational_states_chain_id
				 ON network_operational_states (chain_id)`,
				`CREATE INDEX IF NOT EXISTS idx_network_operational_states_mode
				 ON network_operational_states (mode)`,
			},
		},
		{
			Name:           "lock_canonical_block_history",
			Dialect:        "postgres",
			RequiredTables: []string{"blocks"},
			Statements: []string{
				`LOCK TABLE blocks IN SHARE ROW EXCLUSIVE MODE`,
			},
		},
		{
			Name:           "rewind_ambiguous_chain_checkpoints",
			Dialect:        "postgres",
			RequiredTables: []string{"blocks", "chain_states"},
			Statements: []string{
				`WITH duplicate_heights AS (
					SELECT chain_id, number
					FROM blocks
					WHERE canonical IS TRUE
					GROUP BY chain_id, number
					HAVING COUNT(*) > 1
				), affected_chains AS (
					SELECT chain_id, MIN(number) AS first_duplicate_height
					FROM duplicate_heights
					GROUP BY chain_id
				), rewind_targets AS (
					SELECT chain_id,
					       first_duplicate_height,
					       GREATEST(first_duplicate_height - 64, 0) AS rewind_to
					FROM affected_chains
				)
				UPDATE chain_states AS state
				SET last_processed_block = LEAST(state.last_processed_block, target.rewind_to),
				    last_processed_hash = CASE
				        WHEN state.last_processed_block > target.rewind_to THEN ''
				        ELSE state.last_processed_hash
				    END,
				    last_processed_parent_hash = CASE
				        WHEN state.last_processed_block > target.rewind_to THEN ''
				        ELSE state.last_processed_parent_hash
				    END,
				    last_confirmed_block = LEAST(state.last_confirmed_block, target.rewind_to),
				    continuity_status = 'rollback_required',
				    continuity_reason = LEFT(
				        'migration 202607180014 detected duplicate canonical height '
				        || target.first_duplicate_height::text
				        || '; checkpoint rewound to '
				        || target.rewind_to::text,
				        256
				    ),
				    updated_at = CURRENT_TIMESTAMP
				FROM rewind_targets AS target
				WHERE state.chain_id = target.chain_id`,
			},
		},
		{
			Name:           "gate_ambiguous_chains_for_authoritative_replay",
			Dialect:        "postgres",
			RequiredTables: []string{"blocks", "network_operational_states"},
			Statements: []string{
				`LOCK TABLE network_operational_states IN SHARE ROW EXCLUSIVE MODE`,
				`WITH duplicate_heights AS (
					SELECT chain_id, number
					FROM blocks
					WHERE canonical IS TRUE
					GROUP BY chain_id, number
					HAVING COUNT(*) > 1
				), affected_chains AS (
					SELECT DISTINCT chain_id
					FROM duplicate_heights
				)
				INSERT INTO network_operational_states (
					id, chain_id, mode, reason, updated_by, created_at, updated_at
				)
				SELECT
					md5('` + canonicalBlockMoneyEventSequenceMigrationID + `:network-maintenance:' || chain_id::text)::uuid,
					chain_id,
					'maintenance',
					'` + canonicalBlockMaintenanceReason + `',
					'` + canonicalBlockMaintenanceUpdatedBy + `',
					CURRENT_TIMESTAMP,
					CURRENT_TIMESTAMP
				FROM affected_chains
				ON CONFLICT (chain_id) DO UPDATE
				SET mode = 'maintenance',
				    reason = EXCLUDED.reason,
				    updated_by = EXCLUDED.updated_by,
				    updated_at = CURRENT_TIMESTAMP`,
			},
		},
		{
			Name:           "reconcile_duplicate_canonical_heights",
			Dialect:        "postgres",
			RequiredTables: []string{"blocks"},
			Statements: []string{
				`WITH ranked AS (
					SELECT id,
					       chain_id,
					       number,
					       hash,
					       ROW_NUMBER() OVER (
					           PARTITION BY chain_id, number
					           ORDER BY processed DESC NULLS LAST,
					                    updated_at DESC NULLS LAST,
					                    created_at DESC NULLS LAST,
					                    hash ASC,
					                    id ASC
					       ) AS canonical_rank
					FROM blocks
					WHERE canonical IS TRUE
				), winners AS (
					SELECT chain_id, number, hash
					FROM ranked
					WHERE canonical_rank = 1
				), losers AS (
					SELECT id, chain_id, number
					FROM ranked
					WHERE canonical_rank > 1
				)
				UPDATE blocks AS block
				SET canonical = FALSE,
				    status = 'reorged',
				    reorged_at = COALESCE(block.reorged_at, CURRENT_TIMESTAMP),
				    superseded_by_hash = winner.hash,
				    correction_reason = LEFT(
				        'migration_duplicate_canonical_height:' || winner.hash,
				        256
				    ),
				    updated_at = CURRENT_TIMESTAMP
				FROM losers AS loser
				JOIN winners AS winner
				  ON winner.chain_id = loser.chain_id
				 AND winner.number = loser.number
				WHERE block.id = loser.id`,
				`DROP INDEX IF EXISTS ux_blocks_one_canonical_height`,
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_blocks_one_canonical_height
				 ON blocks (chain_id, number)
				 WHERE canonical = true`,
			},
		},
		{
			Name:           "quarantine_duplicate_branch_chain_facts",
			Dialect:        "postgres",
			RequiredTables: []string{"blocks", "chain_facts"},
			Statements: []string{
				`UPDATE chain_facts AS fact
				 SET status = 'reorged',
				     reorged_at = COALESCE(fact.reorged_at, CURRENT_TIMESTAMP),
				     correction_reason = LEFT(
				         'migration_orphan_duplicate_canonical_height:' || block.superseded_by_hash,
				         256
				     ),
				     updated_at = CURRENT_TIMESTAMP
				 FROM blocks AS block
				 WHERE block.canonical IS FALSE
				   AND block.status = 'reorged'
				   AND block.correction_reason LIKE 'migration_duplicate_canonical_height:%'
				   AND fact.chain_id = block.chain_id
				   AND fact.block_number = block.number
				   AND fact.block_hash = block.hash
				   AND COALESCE(fact.status, '') NOT IN ('reorged', 'superseded')`,
			},
		},
		{
			Name:           "quarantine_duplicate_branch_deposit_inbox",
			Dialect:        "postgres",
			RequiredTables: []string{"chain_facts", "money_event_inboxes"},
			Statements: []string{
				`UPDATE money_event_inboxes AS inbox
				 SET status = 'dead_letter',
				     locked_until = NULL,
				     last_error = 'canonical block migration quarantined an orphan branch chain fact',
				     failure_category = 'migration_orphan_branch',
				     updated_at = CURRENT_TIMESTAMP
				 FROM chain_facts AS fact
				 WHERE fact.status = 'reorged'
				   AND fact.correction_reason LIKE 'migration_orphan_duplicate_canonical_height:%'
				   AND inbox.consumer_name = 'deposit_fact_processor'
				   AND (inbox.event_id = fact.event_id OR inbox.resource_id = fact.id::text)
				   AND inbox.status <> 'dead_letter'`,
			},
		},
		{
			Name:           "backfill_money_event_aggregate_sequence",
			Dialect:        "postgres",
			RequiredTables: []string{"money_event_outboxes"},
			Statements: []string{
				`LOCK TABLE money_event_outboxes IN SHARE ROW EXCLUSIVE MODE`,
				`ALTER TABLE money_event_outboxes
				 ADD COLUMN IF NOT EXISTS sequence BIGINT`,
				`ALTER TABLE money_event_outboxes
				 ADD COLUMN IF NOT EXISTS lease_token UUID`,
				`WITH ranked AS (
					SELECT id,
					       ROW_NUMBER() OVER (
					           PARTITION BY merchant_id, domain_id, aggregate_type, aggregate_id
					           ORDER BY created_at ASC, id ASC
					       )::BIGINT AS aggregate_sequence
					FROM money_event_outboxes
				)
				UPDATE money_event_outboxes AS event
				SET sequence = ranked.aggregate_sequence
				FROM ranked
				WHERE event.id = ranked.id
				  AND event.sequence IS DISTINCT FROM ranked.aggregate_sequence`,
				`ALTER TABLE money_event_outboxes
				 ALTER COLUMN sequence SET DEFAULT 0,
				 ALTER COLUMN sequence SET NOT NULL`,
				`DROP INDEX IF EXISTS idx_money_event_outbox_aggregate_sequence`,
				`CREATE INDEX IF NOT EXISTS idx_money_event_outbox_aggregate_sequence
				 ON money_event_outboxes (merchant_id, domain_id, aggregate_type, aggregate_id, sequence)`,
				`DROP INDEX IF EXISTS ux_money_event_outbox_aggregate_sequence`,
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_money_event_outbox_aggregate_sequence
				 ON money_event_outboxes (merchant_id, domain_id, aggregate_type, aggregate_id, sequence)
				 WHERE sequence > 0`,
			},
		},
		{
			Name:           "snapshot_webhook_delivery_targets",
			Dialect:        "postgres",
			RequiredTables: []string{"webhook_deliveries"},
			Statements: []string{
				`LOCK TABLE webhook_deliveries IN SHARE ROW EXCLUSIVE MODE`,
				`ALTER TABLE webhook_deliveries
				 ADD COLUMN IF NOT EXISTS notification_mode VARCHAR(16),
				 ADD COLUMN IF NOT EXISTS target_subject VARCHAR(255),
				 ADD COLUMN IF NOT EXISTS lease_token UUID`,
				`UPDATE webhook_deliveries
				 SET notification_mode = COALESCE(notification_mode, ''),
				     target_subject = COALESCE(target_subject, '')
				 WHERE notification_mode IS NULL OR target_subject IS NULL`,
				`UPDATE webhook_deliveries
				 SET notification_mode = CASE
				     WHEN LOWER(TRIM(target_url)) LIKE 'nats://%' OR LOWER(TRIM(target_url)) LIKE 'tls://%' THEN 'nats'
				     WHEN LOWER(TRIM(target_url)) LIKE 'http://%' OR LOWER(TRIM(target_url)) LIKE 'https://%' THEN 'webhook'
				     ELSE notification_mode
				 END
				 WHERE notification_mode = ''`,
				`ALTER TABLE webhook_deliveries
				 ALTER COLUMN notification_mode SET DEFAULT '',
				 ALTER COLUMN notification_mode SET NOT NULL,
				 ALTER COLUMN target_subject SET DEFAULT '',
				 ALTER COLUMN target_subject SET NOT NULL`,
			},
		},
		{
			Name:           "backfill_legacy_webhook_delivery_transport",
			Dialect:        "postgres",
			RequiredTables: []string{"domains", "webhook_deliveries"},
			Statements: []string{
				`UPDATE webhook_deliveries AS delivery
				 SET notification_mode = CASE
				         WHEN delivery.notification_mode <> '' THEN delivery.notification_mode
				         WHEN LOWER(TRIM(COALESCE(domain.notification_mode, ''))) = 'nats' THEN 'nats'
				         ELSE 'webhook'
				     END,
				     target_subject = CASE
				         WHEN COALESCE(NULLIF(delivery.notification_mode, ''), LOWER(TRIM(COALESCE(domain.notification_mode, '')))) = 'nats'
				         THEN COALESCE(NULLIF(TRIM(domain.nats_subject), ''), 'gateway.events')
				         ELSE ''
				     END
				 FROM domains AS domain
				 WHERE domain.id = delivery.domain_id
				   AND (delivery.notification_mode = '' OR (delivery.notification_mode = 'nats' AND delivery.target_subject = ''))`,
			},
		},
	}
}
