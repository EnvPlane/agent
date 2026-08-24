package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/envplane/contracts/domain"
)

// StatefulMaterializationInput binds a metadata-only decision to one exact
// feature environment. It contains references and policy, never data.
type StatefulMaterializationInput struct {
	TenantID           string
	ProjectID          string
	EnvironmentID      string
	TemplateRevisionID string
	TemplateDigest     string
	TargetNamespace    string
	InputDigest        string
	AllowedNamespaces  []string
}

// PlanStatefulMaterialization compiles explicit Secret, PVC, and database
// strategies into the existing typed execution contract. It performs no
// reads, writes, credential generation, or database access.
func PlanStatefulMaterialization(input StatefulMaterializationInput, policies []domain.StatefulDependencyPolicy, now time.Time) (domain.StatefulExecutionPlan, error) {
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.ProjectID) == "" || strings.TrimSpace(input.EnvironmentID) == "" || strings.TrimSpace(input.TemplateRevisionID) == "" || strings.TrimSpace(input.TemplateDigest) == "" || strings.TrimSpace(input.TargetNamespace) == "" || strings.TrimSpace(input.InputDigest) == "" {
		return domain.StatefulExecutionPlan{}, fmt.Errorf("stateful materialization scope is required")
	}
	allowed := map[string]bool{}
	for _, namespace := range input.AllowedNamespaces {
		allowed[strings.TrimSpace(namespace)] = true
	}
	for _, policy := range policies {
		if policy.SourceNamespace != "" && policy.Strategy != domain.StatefulStrategyExternalIsolated && policy.Strategy != domain.StatefulStrategyGenerate && !allowed[policy.SourceNamespace] {
			return domain.StatefulExecutionPlan{}, fmt.Errorf("source namespace %q is not allowlisted for %s", policy.SourceNamespace, policy.ID)
		}
		if policy.Kind == "Secret" && policy.Strategy == domain.StatefulStrategyDatabaseRestore {
			return domain.StatefulExecutionPlan{}, fmt.Errorf("Secret %s cannot use a database strategy", policy.ID)
		}
	}
	return domain.CompileStatefulExecutionPlan(input.TenantID, input.ProjectID, input.EnvironmentID, input.TemplateRevisionID, input.TemplateDigest, input.TargetNamespace, policies, input.InputDigest, now)
}
