package agent

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/envplane/contracts/domain"
)

var ErrEscrowNotFound = errors.New("database credential escrow not found")

// DatabasePVCIdentity is exact observed data identity, not a name-based guess.
// Cross-cluster/UID-changing restore needs an explicitly reviewed new binding.
type DatabasePVCIdentity struct {
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	VolumeName string `json:"volumeName"`
}

type DatabaseCredentialBinding struct {
	ClusterID     string                `json:"clusterId"`
	TenantID      string                `json:"tenantId"`
	ProjectID     string                `json:"projectId"`
	EnvironmentID string                `json:"environmentId"`
	ItemID        string                `json:"itemId"`
	Generator     string                `json:"generator"`
	Namespace     string                `json:"namespace"`
	SecretName    string                `json:"secretName"`
	PVCs          []DatabasePVCIdentity `json:"pvcs"`
}

// Authorization is a trusted operator/planner decision, never inferred from an
// empty directory, PVC age, or a client-supplied namespace/name alone.
type DatabaseCredentialAuthorization struct {
	Binding              DatabaseCredentialBinding `json:"binding"`
	InitializeAuthorized bool                      `json:"initializeAuthorized"`
	// Only a reviewed operator may attest this existing named credential was
	// authenticated against the exact retained database. No password is sent in
	// the binding; the reference is read inside the approved target namespace.
	ImportAuthorized           bool   `json:"importAuthorized,omitempty"`
	ImportCredentialSecretName string `json:"importCredentialSecretName,omitempty"`
}

type DatabaseCredentialBindingResolver interface {
	ResolveDatabaseCredentialBinding(context.Context, MaterializationCommand, domain.SecretMaterializationItem) (DatabaseCredentialAuthorization, error)
}

type DatabaseCredentialEscrowStore interface {
	Read(context.Context, string) ([]byte, error)
	Create(context.Context, string, []byte) error
}

type DatabaseCredentialKeyProvider interface {
	ResolveDatabaseCredentialKey(context.Context, string) ([]byte, error)
}

type DatabasePVCIdentityVerifier interface {
	VerifyDatabasePVCIdentities(context.Context, DatabaseCredentialBinding) error
}

type DatabasePVCInitializationGuard interface {
	DatabasePVCInitializationConsumed(context.Context, DatabaseCredentialBinding) (bool, error)
	MarkDatabasePVCInitialization(context.Context, DatabaseCredentialBinding, string) error
}

type DatabaseCredentialEscrow struct {
	Store    DatabaseCredentialEscrowStore
	Keys     DatabaseCredentialKeyProvider
	Bindings DatabaseCredentialBindingResolver
	KeyRef   string
}

func (m *SecretMaterializer) SetDatabaseCredentialEscrow(escrow *DatabaseCredentialEscrow) error {
	if escrow == nil || escrow.Store == nil || escrow.Keys == nil || escrow.Bindings == nil || strings.TrimSpace(escrow.KeyRef) == "" {
		return ErrDatabaseCredentialRecovery
	}
	if _, ok := m.client.(DatabasePVCIdentityVerifier); !ok {
		return ErrDatabaseCredentialRecovery
	}
	if _, ok := m.client.(DatabaseCredentialClient); !ok {
		return ErrDatabaseCredentialRecovery
	}
	if _, ok := m.client.(DatabasePVCInitializationGuard); !ok {
		return ErrDatabaseCredentialRecovery
	}
	m.databaseEscrow = escrow
	return nil
}

func canonicalDatabaseBinding(binding DatabaseCredentialBinding) ([]byte, error) {
	for _, value := range []string{binding.ClusterID, binding.TenantID, binding.ProjectID, binding.EnvironmentID, binding.ItemID, binding.Generator, binding.Namespace, binding.SecretName} {
		if strings.TrimSpace(value) == "" {
			return nil, ErrDatabaseCredentialRecovery
		}
	}
	if !isDatabaseGenerator(binding.Generator) || len(binding.PVCs) == 0 || len(binding.PVCs) > 128 {
		return nil, ErrDatabaseCredentialRecovery
	}
	binding.PVCs = append([]DatabasePVCIdentity(nil), binding.PVCs...)
	sort.Slice(binding.PVCs, func(i, j int) bool { return binding.PVCs[i].Name < binding.PVCs[j].Name })
	seen := map[string]bool{}
	for _, pvc := range binding.PVCs {
		if pvc.Namespace != binding.Namespace || pvc.Name == "" || pvc.UID == "" || pvc.VolumeName == "" || seen[pvc.Name] {
			return nil, ErrDatabaseCredentialRecovery
		}
		seen[pvc.Name] = true
	}
	return json.Marshal(binding)
}

func bindingMatchesCommand(binding DatabaseCredentialBinding, command MaterializationCommand, item domain.SecretMaterializationItem) bool {
	return binding.TenantID == command.TenantID && binding.ProjectID == command.Plan.ProjectID && binding.EnvironmentID == command.Plan.EnvironmentID && binding.ItemID == item.ID && binding.Generator == item.Generator && binding.Namespace == item.TargetNamespace && binding.SecretName == item.TargetName
}

// DatabaseCredentialEscrowObjectName allows the normal installer/admin profile
// builder to calculate exact named GET permissions without reading any key or
// credential. The digest covers metadata only, not secret material.
func DatabaseCredentialEscrowObjectName(binding DatabaseCredentialBinding) (string, error) {
	aad, err := canonicalDatabaseBinding(binding)
	if err != nil {
		return "", err
	}
	return "db-" + strings.TrimPrefix(digestText(string(aad)), "sha256:"), nil
}

type databaseEscrowEnvelope struct {
	Version    int    `json:"version"`
	KeyRef     string `json:"keyRef"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func (e *DatabaseCredentialEscrow) aead(ctx context.Context) (cipher.AEAD, error) {
	key, err := e.Keys.ResolveDatabaseCredentialKey(ctx, e.KeyRef)
	if err != nil {
		return nil, ErrDatabaseCredentialRecovery
	}
	defer clearMaterialBytes(key)
	if len(key) != 32 {
		return nil, ErrDatabaseCredentialRecovery
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrDatabaseCredentialRecovery
	}
	return cipher.NewGCM(block)
}

func (e *DatabaseCredentialEscrow) seal(ctx context.Context, aad []byte, data map[string][]byte) ([]byte, error) {
	aead, err := e.aead(ctx)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, ErrDatabaseCredentialRecovery
	}
	plaintext, err := json.Marshal(data)
	if err != nil {
		return nil, ErrDatabaseCredentialRecovery
	}
	defer clearMaterialBytes(plaintext)
	return json.Marshal(databaseEscrowEnvelope{Version: 1, KeyRef: e.KeyRef, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plaintext, append([]byte("envplane-db-escrow-v1\x00"+e.KeyRef+"\x00"), aad...))})
}

func (e *DatabaseCredentialEscrow) open(ctx context.Context, aad, payload []byte) (map[string][]byte, error) {
	var envelope databaseEscrowEnvelope
	if json.Unmarshal(payload, &envelope) != nil || envelope.Version != 1 || envelope.KeyRef != e.KeyRef {
		return nil, ErrDatabaseCredentialRecovery
	}
	aead, err := e.aead(ctx)
	if err != nil || len(envelope.Nonce) != aead.NonceSize() {
		return nil, ErrDatabaseCredentialRecovery
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, append([]byte("envplane-db-escrow-v1\x00"+e.KeyRef+"\x00"), aad...))
	if err != nil {
		return nil, ErrDatabaseCredentialRecovery
	}
	defer clearMaterialBytes(plaintext)
	var data map[string][]byte
	if json.Unmarshal(plaintext, &data) != nil || len(data) == 0 {
		return nil, ErrDatabaseCredentialRecovery
	}
	return data, nil
}

func validDatabaseCredentialData(generator string, data map[string][]byte) bool {
	profile, key := generatedSecretProfile(generator)
	if !isDatabaseGenerator(generator) || len(data[key]) == 0 {
		return false
	}
	for _, alias := range generatedDatabasePasswordKeys(profile) {
		if !bytes.Equal(data[key], data[alias]) {
			return false
		}
	}
	return true
}

func (m *SecretMaterializer) escrowDatabaseCredential(ctx context.Context, command MaterializationCommand, item domain.SecretMaterializationItem, existing *SecretRecord, idempotencyKey string) error {
	e := m.databaseEscrow
	authorization, err := e.Bindings.ResolveDatabaseCredentialBinding(ctx, command, item)
	if err != nil || !bindingMatchesCommand(authorization.Binding, command, item) {
		return ErrDatabaseCredentialRecovery
	}
	if store, ok := e.Store.(*KubernetesDatabaseEscrowStore); ok && store.namespace == authorization.Binding.Namespace {
		return ErrDatabaseCredentialRecovery
	}
	aad, err := canonicalDatabaseBinding(authorization.Binding)
	if err != nil {
		return err
	}
	if err := m.client.(DatabasePVCIdentityVerifier).VerifyDatabasePVCIdentities(ctx, authorization.Binding); err != nil {
		return ErrDatabaseCredentialRecovery
	}
	// The locator hashes metadata, never password bytes. AEAD authenticates the
	// entire exact binding even if an untrusted store relocates an envelope.
	locator := "db-" + strings.TrimPrefix(digestText(string(aad)), "sha256:")
	payload, err := e.Store.Read(ctx, locator)
	var data map[string][]byte
	if errors.Is(err, ErrEscrowNotFound) {
		if authorization.ImportAuthorized {
			if authorization.ImportCredentialSecretName == "" {
				return ErrDatabaseCredentialRecovery
			}
			imported, importErr := m.client.GetSecret(ctx, item.TargetNamespace, authorization.ImportCredentialSecretName)
			if importErr != nil || imported.Type != "Opaque" {
				clearMaterialData(imported.Data)
				return ErrDatabaseCredentialRecovery
			}
			data = imported.Data
		} else {
			if existing != nil || !authorization.InitializeAuthorized || m.generator == nil {
				return ErrDatabaseCredentialRecovery
			}
			consumed, guardErr := m.client.(DatabasePVCInitializationGuard).DatabasePVCInitializationConsumed(ctx, authorization.Binding)
			if guardErr != nil || consumed {
				return ErrDatabaseCredentialRecovery
			}
			data, err = m.generator.Generate(ctx, item)
			if err != nil {
				return ErrDatabaseCredentialRecovery
			}
		}
		defer clearMaterialData(data)
		if !validDatabaseCredentialData(item.Generator, data) {
			return ErrDatabaseCredentialRecovery
		}
		payload, err = e.seal(ctx, aad, data)
		if err != nil {
			return err
		}
		if err := e.Store.Create(ctx, locator, payload); err != nil {
			if !errors.Is(err, ErrMaterializationConflict) {
				return ErrDatabaseCredentialRecovery
			}
		}
		// Read back durable state even after success. A concurrent winner, not
		// the caller's uncommitted randomness, determines the credential.
		payload, err = e.Store.Read(ctx, locator)
		if err != nil {
			return ErrDatabaseCredentialRecovery
		}
	} else if err != nil {
		return ErrDatabaseCredentialRecovery
	}
	data, err = e.open(ctx, aad, payload)
	if err != nil {
		return err
	}
	defer clearMaterialData(data)
	if !validDatabaseCredentialData(item.Generator, data) {
		return ErrDatabaseCredentialRecovery
	}
	// Durable encrypted escrow exists before marking data identity or creating
	// a target Secret. Markers prevent a stale initialization authorization from
	// minting a replacement password after both escrow and Secret are lost.
	if err := m.client.(DatabasePVCIdentityVerifier).VerifyDatabasePVCIdentities(ctx, authorization.Binding); err != nil {
		return ErrDatabaseCredentialRecovery
	}
	if err := m.client.(DatabasePVCInitializationGuard).MarkDatabasePVCInitialization(ctx, authorization.Binding, locator); err != nil {
		return ErrDatabaseCredentialRecovery
	}
	if existing != nil {
		if len(existing.Data) != len(data) {
			return ErrDatabaseCredentialRecovery
		}
		for key, value := range data {
			if !bytes.Equal(value, existing.Data[key]) {
				return ErrDatabaseCredentialRecovery
			}
		}
		return nil
	}
	// Persistence/verification must finish before any target Secret write.
	annotations := materializerAnnotations(command, item)
	annotations[credentialIdentityAnnotation] = credentialIdentity(command, item)
	return m.client.(DatabaseCredentialClient).CreateGeneratedSecret(ctx, SecretApply{Namespace: item.TargetNamespace, Name: item.TargetName, Type: "Opaque", Data: data, Labels: materializerLabels(command, item), Annotations: annotations, FieldManager: secretMaterializerFieldManager, IdempotencyKey: idempotencyKey})
}

func (m *SecretMaterializer) verifyEscrowBeforeCredentialCleanup(ctx context.Context, command MaterializationCommand, item domain.SecretMaterializationItem, existing SecretRecord) error {
	e := m.databaseEscrow
	auth, err := e.Bindings.ResolveDatabaseCredentialBinding(ctx, command, item)
	if err != nil || !bindingMatchesCommand(auth.Binding, command, item) {
		return ErrDatabaseCredentialRecovery
	}
	aad, err := canonicalDatabaseBinding(auth.Binding)
	if err != nil {
		return err
	}
	payload, err := e.Store.Read(ctx, "db-"+strings.TrimPrefix(digestText(string(aad)), "sha256:"))
	if err != nil {
		return ErrDatabaseCredentialRecovery
	}
	data, err := e.open(ctx, aad, payload)
	if err != nil {
		return err
	}
	defer clearMaterialData(data)
	if !validDatabaseCredentialData(item.Generator, data) || len(existing.Data) != len(data) {
		return ErrDatabaseCredentialRecovery
	}
	for key, value := range data {
		if !bytes.Equal(value, existing.Data[key]) {
			return ErrDatabaseCredentialRecovery
		}
	}
	return nil
}
