package dbmigrations

import (
	"reflect"
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
	if latest != "202607130012_wallet_chiliz_spicy_partial_unique" {
		t.Fatalf("latest id = %q, want wallet Chiliz Spicy partial-index migration", latest)
	}

	artifacts := Artifacts()
	modelTypes := map[reflect.Type]bool{}
	for _, model := range artifacts[len(artifacts)-1].Models {
		modelTypes[reflect.TypeOf(model)] = true
	}
	for _, want := range []any{&models.Wallet{}} {
		if !modelTypes[reflect.TypeOf(want)] {
			t.Fatalf("latest migration artifact missing model %T", want)
		}
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
