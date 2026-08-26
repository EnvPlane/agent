package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/envplane/contracts/domain"
)

type materializerFake struct {
	secrets  map[string]SecretRecord
	applies  []SecretApply
	external []SecretApply
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
