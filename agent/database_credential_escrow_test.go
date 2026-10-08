package agent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/envplane/contracts/domain"
)

type escrowMemoryStore struct {
	payload         []byte
	failure         error
	createCalls     int
	unavailableRead bool
	winner          []byte
	failReadback    bool
}

func (s *escrowMemoryStore) Read(context.Context, string) ([]byte, error) {
	if s.unavailableRead {
		return nil, ErrDatabaseCredentialRecovery
	}
	if s.payload == nil {
		return nil, ErrEscrowNotFound
	}
	return append([]byte(nil), s.payload...), nil
}
func (s *escrowMemoryStore) Create(_ context.Context, _ string, payload []byte) error {
	s.createCalls++
	if s.failure != nil {
		return s.failure
	}
	if s.winner != nil {
		s.payload = append([]byte(nil), s.winner...)
		return ErrMaterializationConflict
	}
	if s.payload != nil {
		return ErrMaterializationConflict
	}
	s.payload = append([]byte(nil), payload...)
	if s.failReadback {
		s.unavailableRead = true
	}
	return nil
}

type escrowTestReferences struct {
	auth DatabaseCredentialAuthorization
	key  []byte
}

func (r *escrowTestReferences) ResolveDatabaseCredentialKey(context.Context, string) ([]byte, error) {
	return append([]byte(nil), r.key...), nil
}
func (r *escrowTestReferences) ResolveDatabaseCredentialBinding(context.Context, MaterializationCommand, domain.SecretMaterializationItem) (DatabaseCredentialAuthorization, error) {
	return r.auth, nil
}

type escrowTestClient struct {
	credentialFake
	verifyError   error
	marker        string
	markerError   error
	markOnlyError error
}

func (c *escrowTestClient) DatabasePVCInitializationConsumed(context.Context, DatabaseCredentialBinding) (bool, error) {
	return c.marker != "", c.markerError
}
func (c *escrowTestClient) MarkDatabasePVCInitialization(_ context.Context, _ DatabaseCredentialBinding, locator string) error {
	if c.markerError != nil {
		return c.markerError
	}
	if c.markOnlyError != nil {
		return c.markOnlyError
	}
	if c.marker != "" && c.marker != locator {
		return ErrDatabaseCredentialRecovery
	}
	c.marker = locator
	return nil
}

func (c *escrowTestClient) VerifyDatabasePVCIdentities(context.Context, DatabaseCredentialBinding) error {
	return c.verifyError
}

func newEscrowFixture(t *testing.T, engine string) (*SecretMaterializer, *escrowTestClient, *escrowMemoryStore, *escrowTestReferences, MaterializationCommand) {
	t.Helper()
	plan := materializerPlan(t, []domain.SecretStrategyConfig{{ID: "db", Strategy: domain.SecretStrategyGenerated, TargetNamespace: "target", TargetName: "db", Generator: engine + "-password-v1:DB_PASSWORD", CredentialRotation: "on_create"}})
	command := MaterializationCommand{TenantID: "tenant", PlanID: plan.PlanID, PlanDigest: plan.Digest, Audience: "runner", Plan: plan}
	references := &escrowTestReferences{key: bytes.Repeat([]byte{7}, 32), auth: DatabaseCredentialAuthorization{InitializeAuthorized: true, Binding: DatabaseCredentialBinding{ClusterID: "cluster", TenantID: "tenant", ProjectID: "project", EnvironmentID: "env", ItemID: "db", Generator: engine + "-password-v1:DB_PASSWORD", Namespace: "target", SecretName: "db", PVCs: []DatabasePVCIdentity{{Namespace: "target", Name: "data", UID: "pvc-uid", VolumeName: "pv-data"}}}}}
	client := &escrowTestClient{credentialFake: credentialFake{materializerFake: materializerFake{secrets: map[string]SecretRecord{}}, pvc: true}}
	store := &escrowMemoryStore{}
	m, _ := NewSecretMaterializer(client, nil, NewGeneratedSecretGenerator())
	if err := m.SetDatabaseCredentialEscrow(&DatabaseCredentialEscrow{Store: store, Keys: references, Bindings: references, KeyRef: "operator-key-v1"}); err != nil {
		t.Fatal(err)
	}
	return m, client, store, references, command
}

func TestDatabaseEscrowRecoveryAcrossSupportedEngines(t *testing.T) {
	for _, engine := range []string{"postgresql", "mysql", "mariadb", "mongodb", "redis"} {
		t.Run(engine, func(t *testing.T) {
			m, client, store, references, command := newEscrowFixture(t, engine)
			if _, err := m.Execute(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			original := append([]byte(nil), client.secrets["target/db"].Data["DB_PASSWORD"]...)
			if store.createCalls != 1 || bytes.Contains(store.payload, original) {
				t.Fatal("escrow did not protect plaintext")
			}
			delete(client.secrets, "target/db")
			references.auth.InitializeAuthorized = false
			m.generator = nil // Recovery must not generate a replacement password.
			if _, err := m.Execute(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(client.secrets["target/db"].Data["DB_PASSWORD"], original) || store.createCalls != 1 {
				t.Fatal("recovery rotated data credential")
			}
			if _, err := m.Execute(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			if len(client.applies) != 2 {
				t.Fatal("replay rewrote recovered credential")
			}
		})
	}
}

func TestDatabaseEscrowMustCommitBeforeTargetWrite(t *testing.T) {
	for _, mode := range []string{"unauthorized initialization", "store failure", "readback failure", "marker denied", "marker write denied", "missing escrow", "PVC mismatch", "bad key", "atomic target conflict"} {
		t.Run(mode, func(t *testing.T) {
			m, client, store, refs, command := newEscrowFixture(t, "mysql")
			switch mode {
			case "unauthorized initialization", "missing escrow":
				refs.auth.InitializeAuthorized = false
			case "store failure":
				store.failure = errors.New("opaque backend failure")
			case "readback failure":
				store.failReadback = true
			case "marker denied":
				client.markerError = ErrDatabaseCredentialRecovery
			case "marker write denied":
				client.markOnlyError = ErrDatabaseCredentialRecovery
			case "PVC mismatch":
				client.verifyError = ErrDatabaseCredentialRecovery
			case "bad key":
				refs.key = []byte("invalid")
			case "atomic target conflict":
				client.conflict = true
			}
			if _, err := m.Execute(context.Background(), command); err == nil {
				t.Fatal("unsafe initialization accepted")
			}
			if len(client.applies) != 0 {
				t.Fatal("target credential written before safe escrow")
			}
			if mode == "marker write denied" && store.payload == nil {
				t.Fatal("encrypted escrow must remain available after marker failure")
			}
		})
	}
}

func TestDatabaseEscrowAuthenticatesEveryBindingAndEnvelope(t *testing.T) {
	m, _, store, refs, command := newEscrowFixture(t, "mysql")
	if _, err := m.Execute(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"cluster", "tenant", "project", "environment", "item", "generator", "namespace", "secret", "PVC namespace", "PVC name", "PVC UID", "volume", "key", "ciphertext"} {
		t.Run(field, func(t *testing.T) {
			binding := refs.auth.Binding
			binding.PVCs = append([]DatabasePVCIdentity(nil), binding.PVCs...)
			payload := append([]byte(nil), store.payload...)
			switch field {
			case "cluster":
				binding.ClusterID += "x"
			case "tenant":
				binding.TenantID += "x"
			case "project":
				binding.ProjectID += "x"
			case "environment":
				binding.EnvironmentID += "x"
			case "item":
				binding.ItemID += "x"
			case "generator":
				binding.Generator = "postgresql-password-v1:DB_PASSWORD"
			case "namespace":
				binding.Namespace += "x"
				binding.PVCs[0].Namespace = binding.Namespace
			case "secret":
				binding.SecretName += "x"
			case "PVC namespace":
				binding.PVCs[0].Namespace += "x"
			case "PVC name":
				binding.PVCs[0].Name += "x"
			case "PVC UID":
				binding.PVCs[0].UID += "x"
			case "volume":
				binding.PVCs[0].VolumeName += "x"
			case "key":
				payload = []byte(strings.Replace(string(payload), "operator-key-v1", "operator-key-v2", 1))
			case "ciphertext":
				payload[len(payload)/2] ^= 1
			}
			aad, err := canonicalDatabaseBinding(binding)
			if err == nil {
				_, err = m.databaseEscrow.open(context.Background(), aad, payload)
			}
			if !errors.Is(err, ErrDatabaseCredentialRecovery) {
				t.Fatal("tampered scope accepted")
			}
		})
	}
}

func TestDatabaseEscrowConcurrentWinnerIsRecovered(t *testing.T) {
	m, client, store, refs, command := newEscrowFixture(t, "mysql")
	aad, err := canonicalDatabaseBinding(refs.auth.Binding)
	if err != nil {
		t.Fatal(err)
	}
	winner := map[string][]byte{"DB_PASSWORD": []byte("winner-credential"), "MYSQL_PASSWORD": []byte("winner-credential")}
	store.winner, err = m.databaseEscrow.seal(context.Background(), aad, winner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Execute(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(client.secrets["target/db"].Data["DB_PASSWORD"], winner["DB_PASSWORD"]) {
		t.Fatal("concurrent escrow winner ignored")
	}
}

func TestDatabaseEscrowLossCannotReuseInitializationAuthorization(t *testing.T) {
	m, client, store, _, command := newEscrowFixture(t, "mysql")
	if _, err := m.Execute(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	delete(client.secrets, "target/db")
	store.payload = nil
	if _, err := m.Execute(context.Background(), command); !errors.Is(err, ErrDatabaseCredentialRecovery) {
		t.Fatal("stale authorization permitted replacement password")
	}
	if len(client.applies) != 1 || store.createCalls != 1 {
		t.Fatal("lost escrow credential regenerated")
	}
}

func TestDatabaseEscrowOperatorImportRequiresExplicitScopedAuthorization(t *testing.T) {
	for _, authorized := range []bool{false, true} {
		t.Run(map[bool]string{false: "unapproved", true: "approved"}[authorized], func(t *testing.T) {
			m, client, store, refs, command := newEscrowFixture(t, "mysql")
			refs.auth.InitializeAuthorized = false
			refs.auth.ImportAuthorized = authorized
			refs.auth.ImportCredentialSecretName = "reviewed-recovery-input"
			client.secrets["target/reviewed-recovery-input"] = SecretRecord{Type: "Opaque", Data: map[string][]byte{"DB_PASSWORD": []byte("known-app-credential"), "MYSQL_PASSWORD": []byte("known-app-credential")}}
			m.generator = nil
			_, err := m.Execute(context.Background(), command)
			if !authorized {
				if !errors.Is(err, ErrDatabaseCredentialRecovery) || store.createCalls != 0 || len(client.applies) != 0 {
					t.Fatal("unapproved import executed")
				}
				return
			}
			if err != nil || store.createCalls != 1 || string(client.secrets["target/db"].Data["DB_PASSWORD"]) != "known-app-credential" {
				t.Fatal("approved exact-scope import failed")
			}
			if _, exists := client.secrets["target/reviewed-recovery-input"]; !exists {
				t.Fatal("source credential removed")
			}
		})
	}
}

func TestDatabaseEscrowUnavailableKeyCannotDeleteLastCredential(t *testing.T) {
	m, client, store, refs, command := newEscrowFixture(t, "mysql")
	if _, err := m.Execute(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	client.pvc = false // Explicitly removed PVCs permit normal Secret cleanup.
	refs.key = nil
	if err := m.Cleanup(context.Background(), command); !errors.Is(err, ErrDatabaseCredentialRecovery) {
		t.Fatal("missing key permitted cleanup")
	}
	if len(client.deletes) != 0 || client.secrets["target/db"].Data == nil {
		t.Fatal("last credential lost")
	}
	refs.key = bytes.Repeat([]byte{7}, 32)
	if err := m.Cleanup(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if len(client.deletes) != 1 || store.payload == nil {
		t.Fatal("cleanup must retain external encrypted escrow")
	}
}

func TestDatabaseEscrowRecoveryRejectsReplacedPVCWithoutArtifactChanges(t *testing.T) {
	m, client, store, _, command := newEscrowFixture(t, "mysql")
	if _, err := m.Execute(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	delete(client.secrets, "target/db")
	client.verifyError = ErrDatabaseCredentialRecovery
	if _, err := m.Execute(context.Background(), command); !errors.Is(err, ErrDatabaseCredentialRecovery) {
		t.Fatal("replaced PVC accepted")
	}
	if len(client.applies) != 1 || store.createCalls != 1 {
		t.Fatal("PVC drift mutated artifacts")
	}
}
