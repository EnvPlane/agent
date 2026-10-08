package agent

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// KubernetesDatabaseEscrowStore uses authenticated Kubernetes API transport and
// only immutable named encrypted objects in an operator-designated namespace.
// It has no update/delete operation and never stores the encryption key.
type KubernetesDatabaseEscrowStore struct {
	source    *KubernetesNamespaceSource
	namespace string
}

func NewKubernetesDatabaseEscrowStore(source *KubernetesNamespaceSource, namespace string) (*KubernetesDatabaseEscrowStore, error) {
	if source == nil || strings.TrimSpace(namespace) == "" || strings.ContainsAny(namespace, "/\\") {
		return nil, ErrDatabaseCredentialRecovery
	}
	endpoint, err := url.Parse(source.apiURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || source.token == "" {
		return nil, ErrDatabaseCredentialRecovery
	}
	return &KubernetesDatabaseEscrowStore{source: NewKubernetesNamespaceSource(source.apiURL, source.token, "", []string{namespace}, source.client), namespace: namespace}, nil
}

func (s *KubernetesDatabaseEscrowStore) Read(ctx context.Context, name string) ([]byte, error) {
	record, err := s.source.GetSecret(ctx, s.namespace, name)
	if errors.Is(err, ErrSecretNotFound) {
		return nil, ErrEscrowNotFound
	}
	if err != nil {
		return nil, ErrDatabaseCredentialRecovery
	}
	defer clearMaterialData(record.Data)
	if !record.Immutable || record.Type != "Opaque" || record.Labels["envplane.io/database-escrow"] != "v1" || len(record.Data) != 1 || len(record.Data["envelope"]) == 0 {
		return nil, ErrDatabaseCredentialRecovery
	}
	return append([]byte(nil), record.Data["envelope"]...), nil
}

func (s *KubernetesDatabaseEscrowStore) Create(ctx context.Context, name string, envelope []byte) error {
	// POST's name uniqueness protects the original envelope from replacement.
	return s.source.CreateGeneratedSecret(ctx, SecretApply{Immutable: true, Namespace: s.namespace, Name: name, Type: "Opaque", Data: map[string][]byte{"envelope": envelope}, Labels: map[string]string{"app.kubernetes.io/managed-by": "envplane", "envplane.io/database-escrow": "v1"}, IdempotencyKey: name})
}

func (s *KubernetesNamespaceSource) VerifyDatabasePVCIdentities(ctx context.Context, binding DatabaseCredentialBinding) error {
	if _, err := canonicalDatabaseBinding(binding); err != nil {
		return err
	}
	if err := s.validateWriteNamespace(binding.Namespace); err != nil {
		return ErrDatabaseCredentialRecovery
	}
	for _, pvc := range binding.PVCs {
		if _, _, err := s.databasePVCRecord(ctx, pvc); err != nil {
			return ErrDatabaseCredentialRecovery
		}
	}
	return nil
}
