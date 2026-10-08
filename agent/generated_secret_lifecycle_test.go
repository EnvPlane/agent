package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/envplane/contracts/domain"
)

type credentialFake struct {
	materializerFake
	pvc          bool
	inventoryErr error
	conflict     bool
}

func (f *credentialFake) GetSecret(ctx context.Context, ns, name string) (SecretRecord, error) {
	record, err := f.materializerFake.GetSecret(ctx, ns, name)
	record.Data = cloneSecretData(record.Data)
	return record, err
}
func (f *credentialFake) HasPersistentVolumeClaims(context.Context, string) (bool, error) {
	return f.pvc, f.inventoryErr
}
func (f *credentialFake) CreateGeneratedSecret(ctx context.Context, apply SecretApply) error {
	if f.conflict {
		return ErrMaterializationConflict
	}
	apply.Data = cloneSecretData(apply.Data)
	return f.ApplySecret(ctx, apply)
}

func TestDatabaseCredentialLifecycleAcrossEngines(t *testing.T) {
	for _, engine := range []string{"postgresql", "mysql", "mariadb", "mongodb", "redis"} {
		t.Run(engine, func(t *testing.T) {
			plan := materializerPlan(t, []domain.SecretStrategyConfig{{ID: "db", Strategy: domain.SecretStrategyGenerated, TargetNamespace: "target", TargetName: "db", Generator: engine + "-password-v1:DB_PASSWORD", CredentialRotation: "on_create"}})
			command := MaterializationCommand{TenantID: "tenant", PlanID: plan.PlanID, PlanDigest: plan.Digest, Audience: "runner", Plan: plan}
			fake := &credentialFake{materializerFake: materializerFake{secrets: map[string]SecretRecord{}}}
			m, _ := NewSecretMaterializer(fake, nil, NewGeneratedSecretGenerator())
			if _, err := m.Execute(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			original := string(fake.secrets["target/db"].Data["DB_PASSWORD"])
			fake.pvc = true
			for _, phase := range []string{"replay", "recreate", "restored-secret-and-pvc"} {
				restarted, _ := NewSecretMaterializer(fake, nil, NewGeneratedSecretGenerator())
				if _, err := restarted.Execute(context.Background(), command); err != nil {
					t.Fatalf("%s: %v", phase, err)
				}
				if string(fake.secrets["target/db"].Data["DB_PASSWORD"]) != original || len(fake.applies) != 1 {
					t.Fatal("credential rotated")
				}
			}
			if err := m.Cleanup(context.Background(), command); !errors.Is(err, ErrDatabaseCredentialRecovery) {
				t.Fatalf("preserve cleanup: %v", err)
			}
			if len(fake.deletes) != 0 {
				t.Fatal("credential deleted with data present")
			}
			delete(fake.secrets, "target/db")
			if _, err := m.Execute(context.Background(), command); !errors.Is(err, ErrDatabaseCredentialRecovery) {
				t.Fatalf("restore missing credential: %v", err)
			}
			if len(fake.applies) != 1 {
				t.Fatal("password generated for retained data")
			}
			// A fully empty, explicitly cleaned environment may initialize anew.
			fake.pvc = false
			if _, err := m.Execute(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			if err := m.Cleanup(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			if len(fake.deletes) != 1 {
				t.Fatal("empty environment credential cleanup failed")
			}
		})
	}
}

func TestGeneratedCredentialIdentitySurvivesPlanChangeButRejectsForeignEnvironment(t *testing.T) {
	item := domain.SecretMaterializationItem{ID: "db", TargetNamespace: "target", TargetName: "db", Generator: "mysql-password-v1:DB_PASSWORD"}
	command := MaterializationCommand{TenantID: "tenant", PlanDigest: "new", Plan: domain.SecretMaterializationPlan{ProjectID: "project", EnvironmentID: "env"}}
	fake := &credentialFake{materializerFake: materializerFake{secrets: map[string]SecretRecord{"target/db": {Type: "Opaque", Data: map[string][]byte{"DB_PASSWORD": []byte("opaque"), "MYSQL_PASSWORD": []byte("opaque")}, Labels: map[string]string{"app.kubernetes.io/managed-by": "envplane"}, Annotations: map[string]string{credentialIdentityAnnotation: credentialIdentity(command, item), "envplane.io/secret-plan-digest": "old"}}}}, pvc: true}
	m, _ := NewSecretMaterializer(fake, nil, NewGeneratedSecretGenerator())
	if err := m.applyGeneratedSecret(context.Background(), command, item, "key"); err != nil {
		t.Fatal(err)
	}
	command.Plan.EnvironmentID = "other"
	if err := m.applyGeneratedSecret(context.Background(), command, item, "key"); !errors.Is(err, ErrForeignSecret) {
		t.Fatalf("foreign env: %v", err)
	}
}

func TestDatabaseCredentialInventoryAndCreateFailClosed(t *testing.T) {
	item := domain.SecretMaterializationItem{ID: "db", TargetNamespace: "target", TargetName: "db", Generator: "mysql-password-v1:DB_PASSWORD"}
	command := MaterializationCommand{TenantID: "tenant"}
	for _, tc := range []struct {
		name         string
		inventoryErr error
		conflict     bool
		want         error
	}{
		{"inventory denied", ErrForeignSecret, false, ErrForeignSecret},
		{"concurrent create", nil, true, ErrMaterializationConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &credentialFake{materializerFake: materializerFake{secrets: map[string]SecretRecord{}}, inventoryErr: tc.inventoryErr, conflict: tc.conflict}
			m, _ := NewSecretMaterializer(fake, nil, NewGeneratedSecretGenerator())
			if err := m.applyGeneratedSecret(context.Background(), command, item, "key"); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if len(fake.applies) != 0 {
				t.Fatal("unsafe apply")
			}
		})
	}
}

func TestDatabaseCredentialLegacyAndMalformedRecords(t *testing.T) {
	item := domain.SecretMaterializationItem{ID: "db", TargetNamespace: "target", TargetName: "db", Generator: "mysql-password-v1:DB_PASSWORD"}
	command := MaterializationCommand{TenantID: "tenant", PlanDigest: "original"}
	for _, tc := range []struct {
		name, digest, enginePassword string
		want                         error
	}{
		{"legacy exact plan", "original", "opaque", nil},
		{"legacy changed plan", "other", "opaque", ErrForeignSecret},
		{"alias mismatch", "original", "different", ErrDatabaseCredentialRecovery},
		{"alias missing", "original", "", ErrDatabaseCredentialRecovery},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &credentialFake{materializerFake: materializerFake{secrets: map[string]SecretRecord{"target/db": {Type: "Opaque", Data: map[string][]byte{"DB_PASSWORD": []byte("opaque"), "MYSQL_PASSWORD": []byte(tc.enginePassword)}, Labels: map[string]string{"app.kubernetes.io/managed-by": "envplane"}, Annotations: map[string]string{"envplane.io/secret-plan-digest": tc.digest}}}}}
			m, _ := NewSecretMaterializer(fake, nil, NewGeneratedSecretGenerator())
			err := m.applyGeneratedSecret(context.Background(), command, item, "key")
			if !errors.Is(err, tc.want) {
				t.Fatalf("unexpected classification: %v", err)
			}
			if len(fake.applies) != 0 {
				t.Fatal("legacy credential rewritten")
			}
		})
	}
	m, _ := NewSecretMaterializer(&materializerFake{secrets: map[string]SecretRecord{}}, nil, NewGeneratedSecretGenerator())
	if err := m.applyGeneratedSecret(context.Background(), command, item, "key"); !errors.Is(err, ErrDatabaseCredentialRecovery) {
		t.Fatal("missing safety capability allowed generation")
	}
}

func TestDatabaseCredentialRecoveryIsNonRetryableOnWire(t *testing.T) {
	if code := materializationWireErrorCode(ErrDatabaseCredentialRecovery); code != domain.SecretErrorValidationFailed || code.Retryable() {
		t.Fatal("recovery must not be reported as a transient backend failure")
	}
	if code := materializationWireItemErrorCode("database_credential_recovery_required"); code != domain.SecretErrorValidationFailed || code.Retryable() {
		t.Fatal("item recovery must not be retried as a backend failure")
	}
}
