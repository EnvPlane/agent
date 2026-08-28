package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/envplane/contracts/domain"
)

type materializerFake struct {
	secrets  map[string]SecretRecord
	applies  []SecretApply
	external []SecretApply
	deletes  []string
}

func (f *materializerFake) DeleteSecret(_ context.Context, namespace, name string) error {
	f.deletes = append(f.deletes, namespace+"/"+name)
	delete(f.secrets, namespace+"/"+name)
	return nil
}

func (f *materializerFake) GetSecret(_ context.Context, namespace, name string) (SecretRecord, error) {
	value, ok := f.secrets[namespace+"/"+name]
	if !ok {
		return SecretRecord{}, ErrSecretNotFound
	}
	return value, nil
}
func (f *materializerFake) ApplySecret(_ context.Context, apply SecretApply) error {
	if apply.FieldManager != secretMaterializerFieldManager || apply.IdempotencyKey == "" {
		return ErrMaterializationConflict
	}
	f.applies = append(f.applies, apply)
	f.secrets[apply.Namespace+"/"+apply.Name] = SecretRecord{Type: apply.Type, Data: apply.Data, Labels: apply.Labels, Annotations: apply.Annotations}
	return nil
}
func (f *materializerFake) ApplyExternal(_ context.Context, apply SecretApply) error {
	f.external = append(f.external, apply)
	return nil
}

type materializerResolver struct{}

func (materializerResolver) ResolveEncryptedReference(context.Context, string) ([]byte, error) {
	return []byte("manual-value"), nil
}

type materializerGenerator struct{}

func (materializerGenerator) Generate(context.Context, domain.SecretMaterializationItem) (map[string][]byte, error) {
	return map[string][]byte{"generated": []byte("generated-value")}, nil
}

func materializerPlan(t *testing.T, items []domain.SecretStrategyConfig) domain.SecretMaterializationPlan {
	t.Helper()
	plan, err := domain.CompileSecretMaterializationPlan("tenant", "project", "env", "revision", "sha256:template", "target", items, "sha256:input", fixedMaterializerTime)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

var fixedMaterializerTime = mustMaterializerTime()

func mustMaterializerTime() (t time.Time) { return time.Unix(100, 0) }

func TestSecretMaterializerExecutesAllStrategiesWithoutSecretBearingCommand(t *testing.T) {
	plan := materializerPlan(t, []domain.SecretStrategyConfig{
		{ID: "clone", Strategy: domain.SecretStrategyEncryptedClone, SourceNamespace: "shared", SourceName: "source", TargetNamespace: "target", TargetName: "clone", EncryptedPayloadRef: "payload/clone"},
		{ID: "ref", Strategy: domain.SecretStrategyReference, SourceNamespace: "shared", SourceName: "existing", TargetNamespace: "target", TargetName: "ref"},
		{ID: "external", Strategy: domain.SecretStrategyExternal, ExternalSecretStore: "vault", ExternalKey: "db", TargetNamespace: "target", TargetName: "external"},
		{ID: "manual", Strategy: domain.SecretStrategyManual, TargetNamespace: "target", TargetName: "manual", EncryptedPayloadRef: "manual/manual"},
		{ID: "generated", Strategy: domain.SecretStrategyGenerated, TargetNamespace: "target", TargetName: "generated", Generator: "password", CredentialRotation: "on_create"},
	})
	fake := &materializerFake{secrets: map[string]SecretRecord{"shared/source": {Type: "Opaque", Data: map[string][]byte{"a": []byte("one")}}, "shared/existing": {Type: "Opaque", Data: map[string][]byte{"x": []byte("y")}}}}
	materializer, err := NewSecretMaterializer(fake, materializerResolver{}, materializerGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	command := MaterializationCommand{TenantID: "tenant", PlanID: plan.PlanID, PlanDigest: plan.Digest, Audience: "runner", Plan: plan}
	results, err := materializer.Execute(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 || len(fake.applies) != 3 || len(fake.external) != 1 {
		t.Fatalf("unexpected execution: results=%d applies=%d external=%d", len(results), len(fake.applies), len(fake.external))
	}
	if string(fake.secrets["target/clone"].Data["a"]) != "one" || fake.applies[0].FieldManager != secretMaterializerFieldManager {
		t.Fatalf("clone was not applied safely: %#v", fake.applies[0])
	}
}

func TestSecretMaterializerRejectsForeignAndUnsafeSecretsAndPlanSubstitution(t *testing.T) {
	plan := materializerPlan(t, []domain.SecretStrategyConfig{{ID: "clone", Strategy: domain.SecretStrategyEncryptedClone, SourceNamespace: "shared", SourceName: "source", TargetNamespace: "target", TargetName: "clone", EncryptedPayloadRef: "payload/clone"}})
	fake := &materializerFake{secrets: map[string]SecretRecord{"shared/source": {Type: "kubernetes.io/service-account-token", Data: map[string][]byte{"token": []byte("unsafe")}}, "target/clone": {Type: "Opaque", Labels: map[string]string{"app.kubernetes.io/managed-by": "other"}}}}
	materializer, _ := NewSecretMaterializer(fake, nil, nil)
	_, err := materializer.Execute(context.Background(), MaterializationCommand{TenantID: "tenant", PlanID: plan.PlanID, PlanDigest: "wrong", Audience: "runner", Plan: plan})
	if !errors.Is(err, ErrMaterializationPlanMismatch) {
		t.Fatalf("plan substitution error = %v", err)
	}
	_, err = materializer.Execute(context.Background(), MaterializationCommand{TenantID: "tenant", PlanID: plan.PlanID, PlanDigest: plan.Digest, Audience: "runner", Plan: plan})
	if !errors.Is(err, ErrUnsafeSecretType) {
		t.Fatalf("unsafe type error = %v", err)
	}
}

func TestSecretMaterializerUsesKubernetesSafeOwnershipLabels(t *testing.T) {
	plan := materializerPlan(t, []domain.SecretStrategyConfig{{ID: "registry", Strategy: domain.SecretStrategyEncryptedClone, SourceNamespace: "base", SourceName: "source", TargetNamespace: "target", TargetName: "registry", EncryptedPayloadRef: "payload/registry"}})
	fake := &materializerFake{secrets: map[string]SecretRecord{"base/source": {Type: "Opaque", Data: map[string][]byte{"value": []byte("redacted")}}}}
	materializer, err := NewSecretMaterializer(fake, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := materializer.Execute(context.Background(), MaterializationCommand{TenantID: "tenant", PlanID: plan.PlanID, PlanDigest: plan.Digest, Audience: "runner", Plan: plan}); err != nil {
		t.Fatalf("execute materialization: %v", err)
	}
	if len(fake.applies) != 1 {
		t.Fatalf("applies=%d, want one", len(fake.applies))
	}
	labels := fake.applies[0].Labels
	for _, key := range []string{"envplane.io/secret-plan", "envplane.io/secret-item"} {
		value := labels[key]
		if len(value) != 32 || strings.ContainsAny(value, "/:") {
			t.Fatalf("label %s=%q is not a bounded Kubernetes-safe digest", key, value)
		}
	}
	if labels["envplane.io/secret-plan"] == plan.PlanID {
		t.Fatal("canonical plan ID was exposed as a Kubernetes label value")
	}
}

func TestSecretMaterializerCleanupDeletesOnlyOwnedSecrets(t *testing.T) {
	plan := materializerPlan(t, []domain.SecretStrategyConfig{
		{ID: "owned", Strategy: domain.SecretStrategyGenerated, TargetNamespace: "target", TargetName: "owned", Generator: "password", CredentialRotation: "on_create"},
		{ID: "ref", Strategy: domain.SecretStrategyReference, SourceNamespace: "shared", SourceName: "ref", TargetNamespace: "target", TargetName: "ref"},
	})
	fake := &materializerFake{secrets: map[string]SecretRecord{"target/owned": {Labels: map[string]string{"app.kubernetes.io/managed-by": "envplane"}, Annotations: map[string]string{"envplane.io/secret-plan-digest": plan.Digest}}}}
	m, _ := NewSecretMaterializer(fake, nil, nil)
	if err := m.Cleanup(context.Background(), MaterializationCommand{TenantID: "tenant", PlanID: plan.PlanID, PlanDigest: plan.Digest, Audience: "runner", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if len(fake.deletes) != 1 || fake.deletes[0] != "target/owned" {
		t.Fatalf("unexpected deletes: %#v", fake.deletes)
	}
}

// TestSecretMaterializationReleaseGate keeps the executor part of the
// private-registry lifecycle in one regression scenario. Test bytes are
// deliberately opaque and are never logged or embedded in commands/results.
func TestSecretMaterializationReleaseGate(t *testing.T) {
	plan := materializerPlan(t, []domain.SecretStrategyConfig{
		{ID: "registry", Strategy: domain.SecretStrategyEncryptedClone, SourceNamespace: "base", SourceName: "registry", TargetNamespace: "target", TargetName: "registry", EncryptedPayloadRef: "envelopes/registry"},
		{ID: "application", Strategy: domain.SecretStrategyReference, SourceNamespace: "base", SourceName: "application", TargetNamespace: "target", TargetName: "application"},
	})
	sourceData := []byte{0x31, 0x95, 0x02, 0xab}
	fake := &materializerFake{secrets: map[string]SecretRecord{
		"base/registry":    {Type: "kubernetes.io/dockerconfigjson", Data: map[string][]byte{".dockerconfigjson": append([]byte(nil), sourceData...)}},
		"base/application": {Type: "Opaque", Data: map[string][]byte{"config": {0x7f}}},
	}}
	command := MaterializationCommand{TenantID: "tenant", PlanID: plan.PlanID, PlanDigest: plan.Digest, Audience: "runner", Plan: plan}
	encoded, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, sourceData) {
		t.Fatal("materialization command serialized source Secret bytes")
	}

	materializer, err := NewSecretMaterializer(fake, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	results, err := materializer.Execute(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Status != "ready" || results[1].Status != "ready" {
		t.Fatalf("unexpected materialization results: %#v", results)
	}
	target, err := fake.GetSecret(context.Background(), "target", "registry")
	if err != nil {
		t.Fatalf("materialized registry Secret missing: %v", err)
	}
	if !bytes.Equal(target.Data[".dockerconfigjson"], sourceData) {
		t.Fatal("materialized registry Secret differs from approved source")
	}

	// A fresh executor can safely resume the immutable command.
	restarted, err := NewSecretMaterializer(fake, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Execute(context.Background(), command); err != nil {
		t.Fatalf("executor restart could not resume materialization: %v", err)
	}

	// The target may not be replaced after ownership changes.
	fake.secrets["target/registry"] = SecretRecord{Type: "Opaque", Labels: map[string]string{"app.kubernetes.io/managed-by": "foreign"}}
	if _, err := restarted.Execute(context.Background(), command); !errors.Is(err, ErrForeignSecret) {
		t.Fatalf("foreign target replacement error = %v", err)
	}

	// Restore the two-factor ownership guard and verify close/delete cleanup.
	fake.secrets["target/registry"] = target
	if err := restarted.Cleanup(context.Background(), command); err != nil {
		t.Fatalf("cleanup materialized registry Secret: %v", err)
	}
	if _, err := fake.GetSecret(context.Background(), "target", "registry"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("owned registry Secret remained after cleanup: %v", err)
	}
	if _, err := fake.GetSecret(context.Background(), "base", "application"); err != nil {
		t.Fatalf("reference Secret was deleted during cleanup: %v", err)
	}
}
