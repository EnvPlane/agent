package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/envplane/contracts/domain"
)

const secretMaterializerFieldManager = "envplane-secret-materializer"

var (
	ErrMaterializationPlanMismatch = errors.New("materialization plan digest mismatch")
	ErrForeignSecret               = errors.New("existing Secret is not owned by EnvPlane")
	ErrUnsafeSecretType            = errors.New("unsafe Secret type is not materializable")
	ErrMaterializationConflict     = errors.New("materialization conflict")
	ErrSecretNotFound              = errors.New("secret not found")
)

type MaterializationCommand struct {
	TenantID   string                           `json:"tenantId"`
	PlanID     string                           `json:"planId"`
	PlanDigest string                           `json:"planDigest"`
	Audience   string                           `json:"audience"`
	Plan       domain.SecretMaterializationPlan `json:"plan"`
}

// SecretRecord intentionally contains only the fields needed to copy/apply a
// Secret. It is never included in MaterializationCommand or status reports.
type SecretRecord struct {
	Type        string
	Data        map[string][]byte
	Labels      map[string]string
	Annotations map[string]string
}

type SecretApply struct {
	Namespace      string
	Name           string
	Type           string
	Data           map[string][]byte
	Labels         map[string]string
	Annotations    map[string]string
	FieldManager   string
	Force          bool
	IdempotencyKey string
	ExternalStore  string
	ExternalKey    string
}

type SecretMaterializerClient interface {
	GetSecret(context.Context, string, string) (SecretRecord, error)
	ApplySecret(context.Context, SecretApply) error
	ApplyExternal(context.Context, SecretApply) error
	DeleteSecret(context.Context, string, string) error
}

type SecretSensitiveResolver interface {
	ResolveEncryptedReference(context.Context, string) ([]byte, error)
}

type SecretGenerator interface {
	Generate(context.Context, domain.SecretMaterializationItem) (map[string][]byte, error)
}

type MaterializationResult struct {
	ItemID    string `json:"itemId"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
}

type SecretMaterializer struct {
	client    SecretMaterializerClient
	resolver  SecretSensitiveResolver
	generator SecretGenerator
}

func NewSecretMaterializer(client SecretMaterializerClient, resolver SecretSensitiveResolver, generator SecretGenerator) (*SecretMaterializer, error) {
	if client == nil {
		return nil, errors.New("secret materializer client is required")
	}
	return &SecretMaterializer{client: client, resolver: resolver, generator: generator}, nil
}

// Execute verifies the immutable plan before every item and applies items in
// order. A resumed command simply rechecks the existing ownership/digest and
// reapplies through Kubernetes server-side apply with the same idempotency key.
func (m *SecretMaterializer) Execute(ctx context.Context, command MaterializationCommand) ([]MaterializationResult, error) {
	if command.TenantID == "" || command.PlanID == "" || command.PlanDigest == "" || command.Audience == "" || command.Plan.PlanID != command.PlanID || command.Plan.Digest != command.PlanDigest || command.Plan.TenantID != command.TenantID {
		return nil, ErrMaterializationPlanMismatch
	}
	if err := command.Plan.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMaterializationPlanMismatch, err)
	}
	results := make([]MaterializationResult, 0, len(command.Plan.Items))
	for _, item := range command.Plan.Items {
		result := MaterializationResult{ItemID: item.ID, Status: "ready"}
		if err := m.executeItem(ctx, command, item); err != nil {
			result.Status, result.ErrorCode = "failed", materializationErrorCode(err)
			results = append(results, result)
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// Cleanup deletes only plan-owned Secrets. Ownership and the plan digest are
// both required, so a forged/stale plan cannot remove a foreign Secret.
func (m *SecretMaterializer) Cleanup(ctx context.Context, command MaterializationCommand) error {
	if command.TenantID == "" || command.PlanID == "" || command.PlanDigest == "" || command.Plan.PlanID != command.PlanID || command.Plan.Digest != command.PlanDigest || command.Plan.TenantID != command.TenantID {
		return ErrMaterializationPlanMismatch
	}
	if err := command.Plan.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrMaterializationPlanMismatch, err)
	}
	owned := map[string]domain.SecretMaterializationItem{}
	for _, item := range command.Plan.Items {
		if item.Strategy == domain.SecretStrategyEncryptedClone || item.Strategy == domain.SecretStrategyManual || item.Strategy == domain.SecretStrategyGenerated {
			owned[item.TargetNamespace+"\x00"+item.TargetName] = item
		}
	}
	for _, record := range command.Plan.Ownership {
		if record.Kind != "Secret" {
			continue
		}
		item, ok := owned[record.Namespace+"\x00"+record.Name]
		if !ok || record.Namespace != command.Plan.TargetNamespace {
			continue
		}
		existing, err := m.client.GetSecret(ctx, record.Namespace, record.Name)
		if errors.Is(err, ErrSecretNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if existing.Labels["app.kubernetes.io/managed-by"] != "envplane" || existing.Annotations["envplane.io/secret-plan-digest"] != command.PlanDigest || item.TargetName != record.Name {
			return ErrForeignSecret
		}
		if err := m.client.DeleteSecret(ctx, record.Namespace, record.Name); err != nil {
			return err
		}
	}
	return nil
}

func (m *SecretMaterializer) executeItem(ctx context.Context, command MaterializationCommand, item domain.SecretMaterializationItem) error {
	key, err := materializationIdempotencyKey(command.Plan, item.ID, domain.SecretOperationMaterialize)
	if err != nil {
		return fmt.Errorf("materialization idempotency binding: %w", err)
	}
	switch item.Strategy {
	case domain.SecretStrategyReference:
		_, err := m.client.GetSecret(ctx, item.SourceNamespace, item.SourceName)
		return err
	case domain.SecretStrategyExternal:
		return m.client.ApplyExternal(ctx, SecretApply{Namespace: item.TargetNamespace, Name: item.TargetName, Type: "Opaque", ExternalStore: item.ExternalSecretStore, ExternalKey: item.ExternalKey, Labels: materializerLabels(command, item), Annotations: materializerAnnotations(command, item), FieldManager: secretMaterializerFieldManager, IdempotencyKey: key})
	case domain.SecretStrategyEncryptedClone:
		source, err := m.client.GetSecret(ctx, item.SourceNamespace, item.SourceName)
		if err != nil {
			return err
		}
		return m.applyCopiedSecret(ctx, command, item, source, key)
	case domain.SecretStrategyManual:
		if m.resolver == nil {
			return errors.New("encrypted sensitive resolver is unavailable")
		}
		value, err := m.resolver.ResolveEncryptedReference(ctx, item.EncryptedPayloadRef)
		if err != nil {
			return err
		}
		defer clearMaterialBytes(value)
		return m.applyCopiedSecret(ctx, command, item, SecretRecord{Type: "Opaque", Data: map[string][]byte{"value": value}}, key)
	case domain.SecretStrategyGenerated:
		if m.generator == nil {
			return errors.New("secret generator is unavailable")
		}
		data, err := m.generator.Generate(ctx, item)
		if err != nil {
			return err
		}
		defer clearMaterialData(data)
		return m.applyCopiedSecret(ctx, command, item, SecretRecord{Type: "Opaque", Data: data}, key)
	default:
		return fmt.Errorf("unsupported secret strategy %q", item.Strategy)
	}
}

func (m *SecretMaterializer) applyCopiedSecret(ctx context.Context, command MaterializationCommand, item domain.SecretMaterializationItem, source SecretRecord, key string) error {
	if source.Type == "kubernetes.io/service-account-token" || strings.Contains(strings.ToLower(source.Type), "service-account-token") {
		return ErrUnsafeSecretType
	}
	if existing, err := m.client.GetSecret(ctx, item.TargetNamespace, item.TargetName); err == nil {
		if existing.Labels["app.kubernetes.io/managed-by"] != "envplane" || existing.Annotations["envplane.io/secret-plan-digest"] != command.PlanDigest {
			return ErrForeignSecret
		}
	} else if !isSecretNotFound(err) {
		return err
	}
	data := cloneSecretData(source.Data)
	return m.client.ApplySecret(ctx, SecretApply{Namespace: item.TargetNamespace, Name: item.TargetName, Type: source.Type, Data: data, Labels: materializerLabels(command, item), Annotations: materializerAnnotations(command, item), FieldManager: secretMaterializerFieldManager, IdempotencyKey: key})
}

func materializerLabels(command MaterializationCommand, item domain.SecretMaterializationItem) map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": "envplane", "envplane.io/secret-plan": materializerLabelID(command.PlanID), "envplane.io/secret-item": materializerLabelID(item.ID)}
}
func materializerAnnotations(command MaterializationCommand, item domain.SecretMaterializationItem) map[string]string {
	return map[string]string{"envplane.io/secret-plan-digest": command.PlanDigest, "envplane.io/secret-item-digest": digestText(item.ID + "\x00" + item.TargetName)}
}
func materializationIdempotencyKey(plan domain.SecretMaterializationPlan, itemID string, operation domain.SecretMaterializationOperation) (string, error) {
	return domain.SecretMaterializationIdempotencyKey(plan.TenantID, plan.ProjectID, plan.EnvironmentID, plan.TemplateDigest, plan.TargetNamespace, itemID, operation)
}
func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
func materializerLabelID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
func cloneSecretData(input map[string][]byte) map[string][]byte {
	output := make(map[string][]byte, len(input))
	for key, value := range input {
		output[key] = append([]byte(nil), value...)
	}
	return output
}
func clearMaterialData(data map[string][]byte) {
	for _, value := range data {
		clearMaterialBytes(value)
	}
}
func clearMaterialBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
func materializationErrorCode(err error) string {
	if errors.Is(err, ErrForeignSecret) {
		return "foreign_secret"
	}
	if errors.Is(err, ErrUnsafeSecretType) {
		return "unsafe_secret_type"
	}
	if errors.Is(err, ErrMaterializationConflict) {
		return "conflict"
	}
	return "materialization_failed"
}
func isSecretNotFound(err error) bool { return errors.Is(err, ErrSecretNotFound) }
