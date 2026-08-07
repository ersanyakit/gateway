package dbmigrations

import (
	"reflect"
	"strings"
	"testing"

	"core/models"
)

func TestMigrationArtifactsUseGORMModels(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("validate migration artifacts: %v", err)
	}
	latest, err := LatestID()
	if err != nil {
		t.Fatalf("latest id: %v", err)
	}
	if latest != "202607180014_canonical_block_money_event_sequence_invariants" {
		t.Fatalf("latest id = %q, want canonical block and money-event sequence migration", latest)
	}

	artifacts := Artifacts()
	modelTypes := map[reflect.Type]bool{}
	for _, model := range artifacts[len(artifacts)-1].Models {
		modelTypes[reflect.TypeOf(model)] = true
	}
	for _, want := range []any{&models.Block{}, &models.MoneyEventOutbox{}, &models.WebhookDelivery{}, &models.NetworkOperationalState{}} {
		if !modelTypes[reflect.TypeOf(want)] {
			t.Fatalf("latest migration artifact missing model %T", want)
		}
	}
}

func TestCanonicalBlockMigrationGatesAffectedNetworksBeforeReconciliation(t *testing.T) {
	steps := canonicalBlockMoneyEventSequencePreflight()
	position := make(map[string]int, len(steps))
	byName := make(map[string]SQLStep, len(steps))
	for index, step := range steps {
		position[step.Name] = index
		byName[step.Name] = step
	}

	ensure, ok := byName["ensure_network_operational_state_gate_schema"]
	if !ok || !strings.Contains(strings.Join(ensure.Statements, "\n"), "CREATE TABLE IF NOT EXISTS network_operational_states") {
		t.Fatal("canonical migration must safely create the durable gate table before AutoMigrate")
	}
	gate, ok := byName["gate_ambiguous_chains_for_authoritative_replay"]
	if !ok {
		t.Fatal("canonical migration is missing the affected-chain maintenance gate")
	}
	if position[gate.Name] >= position["reconcile_duplicate_canonical_heights"] {
		t.Fatal("affected chains must be durably gated before duplicate canonical rows are reconciled")
	}
	if strings.Join(gate.RequiredTables, ",") != "blocks,network_operational_states" {
		t.Fatalf("gate required tables = %v, want blocks and network_operational_states", gate.RequiredTables)
	}
	gateSQL := strings.Join(gate.Statements, "\n")
	for _, token := range []string{
		"LOCK TABLE network_operational_states",
		"HAVING COUNT(*) > 1",
		"'maintenance'",
		canonicalBlockMoneyEventSequenceMigrationID,
		"authoritative scanner replay",
		"money-state reconciliation",
		"operator must acknowledge",
		"'" + canonicalBlockMaintenanceUpdatedBy + "'",
		"ON CONFLICT (chain_id) DO UPDATE",
	} {
		if !strings.Contains(gateSQL, token) {
			t.Fatalf("maintenance gate SQL missing %q", token)
		}
	}
	if strings.Contains(gateSQL, "mode = 'active'") {
		t.Fatal("canonical migration must never automatically reactivate a gated network")
	}
}

func TestMigrationArtifactsValidatePreAutoMigrateSteps(t *testing.T) {
	artifact := Artifacts()[len(Artifacts())-1]
	artifact.PreAutoMigrate = []SQLStep{{
		Name:           "missing_sql",
		Dialect:        "postgres",
		RequiredTables: []string{"blocks"},
	}}
	if err := ValidateArtifacts([]Artifact{artifact}); err == nil {
		t.Fatal("pre-auto-migrate step without SQL should be invalid")
	}

	artifact = Artifacts()[len(Artifacts())-1]
	artifact.PreAutoMigrate[0].RequiredTables = []string{""}
	if err := ValidateArtifacts([]Artifact{artifact}); err == nil {
		t.Fatal("pre-auto-migrate step with blank required table should be invalid")
	}
}

func TestMigrationArtifactsRequireGovernanceMetadata(t *testing.T) {
	artifact := Artifacts()[0]
	artifact.ID = "202606300001_missing_forward_plan"
	artifact.ForwardPlan = ""

	if err := ValidateArtifacts([]Artifact{artifact}); err == nil {
		t.Fatal("migration artifact without forward plan should be invalid")
	}

	artifact = Artifacts()[0]
	artifact.ID = "202606300002_missing_verification_query"
	artifact.VerificationQuery = ""
	if err := ValidateArtifacts([]Artifact{artifact}); err == nil {
		t.Fatal("migration artifact without verification query should be invalid")
	}
}
