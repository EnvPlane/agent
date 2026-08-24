package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/envplane/contracts/domain"
)

func TestPlanStatefulMaterializationUsesTypedRefsAndBlocksUnsafeSources(t *testing.T) {
	plan, err := PlanStatefulMaterialization(StatefulMaterializationInput{TenantID: "tenant-a", ProjectID: "project-a", EnvironmentID: "env-a", TemplateRevisionID: "rev-1", TemplateDigest: "sha256:revision", TargetNamespace: "feature-a", InputDigest: "sha256:input", AllowedNamespaces: []string{"base-a"}}, []domain.StatefulDependencyPolicy{
		{ID: "db", Kind: "Database", Strategy: domain.StatefulStrategyDatabaseRestore, TargetName: "db", TargetNamespace: "feature-a", DumpRef: "object://dump", RestoreCredentialRef: "secret://restore", SourceEnvironmentClass: "production", MaskingPolicyRef: "mask-v1", RetentionHours: 48},
		{ID: "secret", Kind: "Secret", Strategy: domain.StatefulStrategyGenerate, TargetName: "db-secret", TargetNamespace: "feature-a", CredentialRotation: "on_create_and_cleanup", RetentionHours: 48},
	}, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if !plan.ApprovalRequired || len(plan.Steps) != 2 {
		t.Fatalf("production materialization must require approval: %+v", plan)
	}
	encoded := strings.ToLower(string(mustJSON(plan)))
	for _, forbidden := range []string{"password", "plaintext", "row data", "secret value"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("materialization plan contains forbidden data %q", forbidden)
		}
	}
}

func TestPlanStatefulMaterializationRejectsForeignNamespace(t *testing.T) {
	_, err := PlanStatefulMaterialization(StatefulMaterializationInput{TenantID: "tenant-a", ProjectID: "project-a", EnvironmentID: "env-a", TemplateRevisionID: "rev-1", TemplateDigest: "sha256:revision", TargetNamespace: "feature-a", InputDigest: "sha256:input", AllowedNamespaces: []string{"base-a"}}, []domain.StatefulDependencyPolicy{{ID: "pvc", Kind: "PersistentVolumeClaim", Strategy: domain.StatefulStrategySnapshotClone, SourceNamespace: "tenant-b", SourceName: "data", TargetName: "data", TargetNamespace: "feature-a", SnapshotClass: "snap", Size: "1Gi"}}, time.Now())
	if err == nil {
		t.Fatal("foreign source namespace must be rejected")
	}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
