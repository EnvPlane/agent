package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/envplane/contracts/domain"
)

// MountedDatabaseEscrowReferences reads an existing operator-controlled Secret
// projection. Neither configuration nor a materialization request carries keys.
type MountedDatabaseEscrowReferences struct{ KeyFile, BindingFile, KeyRef, ClusterID string }

func readBoundedEscrowFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrDatabaseCredentialRecovery
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		clearMaterialBytes(data)
		return nil, ErrDatabaseCredentialRecovery
	}
	return data, nil
}

func (r MountedDatabaseEscrowReferences) ResolveDatabaseCredentialKey(_ context.Context, ref string) ([]byte, error) {
	if ref == "" || ref != r.KeyRef {
		return nil, ErrDatabaseCredentialRecovery
	}
	key, err := readBoundedEscrowFile(r.KeyFile, 32)
	if err != nil || len(key) != 32 {
		clearMaterialBytes(key)
		return nil, ErrDatabaseCredentialRecovery
	}
	return key, nil
}

func (r MountedDatabaseEscrowReferences) ResolveDatabaseCredentialBinding(_ context.Context, command MaterializationCommand, item domain.SecretMaterializationItem) (DatabaseCredentialAuthorization, error) {
	payload, err := readBoundedEscrowFile(r.BindingFile, 1<<20)
	if err != nil {
		return DatabaseCredentialAuthorization{}, err
	}
	defer clearMaterialBytes(payload)
	var authorizations []DatabaseCredentialAuthorization
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&authorizations) != nil || len(authorizations) == 0 || len(authorizations) > 128 {
		return DatabaseCredentialAuthorization{}, ErrDatabaseCredentialRecovery
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return DatabaseCredentialAuthorization{}, ErrDatabaseCredentialRecovery
	}
	var result DatabaseCredentialAuthorization
	matches := 0
	for _, auth := range authorizations {
		if _, err := canonicalDatabaseBinding(auth.Binding); err != nil || auth.Binding.ClusterID != r.ClusterID {
			return DatabaseCredentialAuthorization{}, ErrDatabaseCredentialRecovery
		}
		if bindingMatchesCommand(auth.Binding, command, item) {
			result = auth
			matches++
		}
	}
	if matches != 1 {
		return DatabaseCredentialAuthorization{}, ErrDatabaseCredentialRecovery
	}
	return result, nil
}

// ConfigureDatabaseCredentialEscrowFromEnv does not provision namespaces, RBAC,
// keys or bindings. Incorrect opt-in configuration stops runtime initialization.
func ConfigureDatabaseCredentialEscrowFromEnv(m *SecretMaterializer, source *KubernetesNamespaceSource, clusterID string) error {
	raw := strings.TrimSpace(os.Getenv("ENVPLANE_DB_CREDENTIAL_ESCROW_ENABLED"))
	if raw == "" || raw == "false" {
		return nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil || !enabled {
		return ErrDatabaseCredentialRecovery
	}
	r := MountedDatabaseEscrowReferences{KeyFile: os.Getenv("ENVPLANE_DB_CREDENTIAL_ESCROW_KEY_FILE"), BindingFile: os.Getenv("ENVPLANE_DB_CREDENTIAL_ESCROW_BINDINGS_FILE"), KeyRef: os.Getenv("ENVPLANE_DB_CREDENTIAL_ESCROW_KEY_REF"), ClusterID: clusterID}
	namespace := os.Getenv("ENVPLANE_DB_CREDENTIAL_ESCROW_NAMESPACE")
	if r.KeyFile == "" || r.BindingFile == "" || r.KeyRef == "" || r.ClusterID == "" || namespace == "" {
		return ErrDatabaseCredentialRecovery
	}
	store, err := NewKubernetesDatabaseEscrowStore(source, namespace)
	if err != nil {
		return err
	}
	return m.SetDatabaseCredentialEscrow(&DatabaseCredentialEscrow{Store: store, Keys: r, Bindings: r, KeyRef: r.KeyRef})
}
