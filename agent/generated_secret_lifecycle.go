package agent

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/envplane/contracts/domain"
)

const credentialIdentityAnnotation = "envplane.io/generated-credential-identity"

// DatabaseCredentialClient deliberately exposes no database password or account
// rotation. Any claim in the feature namespace is conservatively treated as data
// that may depend on the credential. Missing inventory capability fails closed.
type DatabaseCredentialClient interface {
	HasPersistentVolumeClaims(context.Context, string) (bool, error)
	CreateGeneratedSecret(context.Context, SecretApply) error
}

func isDatabaseGenerator(generator string) bool {
	profile, _ := generatedSecretProfile(generator)
	return len(generatedDatabasePasswordKeys(profile)) > 0
}

func credentialIdentity(command MaterializationCommand, item domain.SecretMaterializationItem) string {
	return digestText(strings.Join([]string{command.TenantID, command.Plan.ProjectID, command.Plan.EnvironmentID, item.ID, item.TargetName, item.Generator}, "\x00"))
}

func (m *SecretMaterializer) requireNoDatabasePVCs(ctx context.Context, namespace string) error {
	client, ok := m.client.(DatabaseCredentialClient)
	if !ok {
		return ErrDatabaseCredentialRecovery
	}
	present, err := client.HasPersistentVolumeClaims(ctx, namespace)
	if err != nil {
		return err
	}
	if present {
		return ErrDatabaseCredentialRecovery
	}
	return nil
}

func (m *SecretMaterializer) applyGeneratedSecret(ctx context.Context, command MaterializationCommand, item domain.SecretMaterializationItem, key string) error {
	existing, err := m.client.GetSecret(ctx, item.TargetNamespace, item.TargetName)
	if err == nil {
		defer clearMaterialData(existing.Data)
		identity := existing.Annotations[credentialIdentityAnnotation]
		// Legacy records may only be reused under their exact original plan.
		ownedIdentity := identity == credentialIdentity(command, item) ||
			(identity == "" && existing.Annotations["envplane.io/secret-plan-digest"] == command.PlanDigest)
		if existing.Labels["app.kubernetes.io/managed-by"] != "envplane" ||
			!ownedIdentity {
			return ErrForeignSecret
		}
		if existing.Type != "Opaque" || len(existing.Data) == 0 {
			return ErrDatabaseCredentialRecovery
		}
		profile, applicationKey := generatedSecretProfile(item.Generator)
		if applicationKey != "" {
			if len(existing.Data[applicationKey]) == 0 {
				return ErrDatabaseCredentialRecovery
			}
			for _, engineKey := range generatedDatabasePasswordKeys(profile) {
				if !bytes.Equal(existing.Data[applicationKey], existing.Data[engineKey]) {
					return ErrDatabaseCredentialRecovery
				}
			}
		}
		// No write, randomness, or implicit rotation on restart/recreate/restore.
		return nil
	}
	if !errors.Is(err, ErrSecretNotFound) {
		return err
	}
	if isDatabaseGenerator(item.Generator) {
		if err := m.requireNoDatabasePVCs(ctx, item.TargetNamespace); err != nil {
			return err
		}
	}
	if m.generator == nil {
		return errors.New("secret generator is unavailable")
	}
	data, err := m.generator.Generate(ctx, item)
	if err != nil {
		return err
	}
	defer clearMaterialData(data)
	annotations := materializerAnnotations(command, item)
	annotations[credentialIdentityAnnotation] = credentialIdentity(command, item)
	apply := SecretApply{Namespace: item.TargetNamespace, Name: item.TargetName, Type: "Opaque", Data: cloneSecretData(data), Labels: materializerLabels(command, item), Annotations: annotations, FieldManager: secretMaterializerFieldManager, IdempotencyKey: key}
	if client, ok := m.client.(DatabaseCredentialClient); ok {
		// POST, never SSA: concurrent creators cannot overwrite the winner.
		defer clearMaterialData(apply.Data)
		return client.CreateGeneratedSecret(ctx, apply)
	}
	return m.client.ApplySecret(ctx, apply)
}
