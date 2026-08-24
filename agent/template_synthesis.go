package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/envplane/contracts/domain"
)

var synthesisCloneKinds = map[string]bool{
	"ConfigMap": true, "DaemonSet": true, "Deployment": true, "HorizontalPodAutoscaler": true,
	"Ingress": true, "Job": true, "LimitRange": true, "Namespace": true, "NetworkPolicy": true,
	"PodDisruptionBudget": true, "ResourceQuota": true, "Service": true, "ServiceAccount": true,
	"StatefulSet": true, "CronJob": true,
}

// SynthesizeEnvironmentTemplate converts a sanitized multi-namespace scan into
// an immutable review artifact. It never reads Kubernetes and never materializes
// Secret data; callers must resolve every non-clone decision before applying.
func SynthesizeEnvironmentTemplate(input domain.TemplateSynthesisInput, snapshots []domain.ResourceSnapshot, graph domain.ServiceGraph) (domain.EnvironmentTemplateSynthesis, error) {
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.ProjectID) == "" || strings.TrimSpace(input.SourceScanID) == "" {
		return domain.EnvironmentTemplateSynthesis{}, fmt.Errorf("template synthesis scope is required")
	}
	namespaces := append([]string(nil), input.SourceNamespaces...)
	sort.Strings(namespaces)
	allowedNamespaces := map[string]bool{}
	for _, namespace := range namespaces {
		if strings.TrimSpace(namespace) != "" {
			allowedNamespaces[namespace] = true
		}
	}
	decisions := make([]domain.TemplateSynthesisDecision, 0, len(snapshots))
	resources := make([]domain.ResourceTemplate, 0, len(snapshots))
	policies := make([]domain.ResourceDependencyPolicy, 0, len(snapshots))
	for _, snapshot := range snapshots {
		id := resourceID(snapshot)
		strategy, action, reason, autonomous := classifySnapshot(snapshot, allowedNamespaces)
		decisions = append(decisions, domain.TemplateSynthesisDecision{ResourceID: id, Action: action, Strategy: string(strategy), SourceType: "resource_snapshot", SourceID: id, Confidence: confidenceFor(strategy), Reason: reason, AutonomousApply: autonomous})
		policies = append(policies, domain.ResourceDependencyPolicy{ResourceID: id, Kind: snapshot.Kind, Namespace: snapshot.Namespace, Name: snapshot.Name, Strategy: strategy, Defaulted: strategy == domain.ResourcePolicyClone || strategy == domain.ResourcePolicyParameterize, Required: strategy == domain.ResourcePolicyUnsupported, Reason: reason})
		if strategy != domain.ResourcePolicyClone && strategy != domain.ResourcePolicyParameterize {
			continue
		}
		if snapshot.Manifest == nil {
			continue
		}
		resources = append(resources, domain.ResourceTemplate{APIVersion: stringValue(snapshot.Manifest["apiVersion"]), Kind: snapshot.Kind, Namespace: snapshot.Namespace, Name: snapshot.Name, Manifest: cloneMap(snapshot.Manifest), Policy: string(strategy)})
		decisions = append(decisions, parameterDecisions(snapshot)...)
	}
	domain.SortTemplateSynthesisDecisions(decisions)
	issues := make([]domain.DependencyGraphIssue, 0)
	if graph.Validation != nil {
		issues = append(issues, graph.Validation.Errors...)
	}
	for _, decision := range decisions {
		if !decision.AutonomousApply && decision.Action != "ignore" {
			issues = append(issues, domain.DependencyGraphIssue{Code: "review_required", ResourceID: decision.ResourceID, Path: decision.Path, Message: decision.Reason})
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].ResourceID != issues[j].ResourceID {
			return issues[i].ResourceID < issues[j].ResourceID
		}
		return issues[i].Code < issues[j].Code
	})
	revision := domain.EnvironmentTemplateRevision{ContractVersion: domain.EnvironmentTemplateContractVersion, RevisionID: synthesisRevisionID(input.SourceScanID, resources), TemplateID: input.ProjectID + "-synthesized", TenantID: input.TenantID, ProjectID: input.ProjectID, ClusterID: input.ClusterID, SourceScanID: input.SourceScanID, SourceNamespaces: namespaces, Resources: resources, ResourcePolicies: policies, CreatedAt: input.Now.UTC(), PublishedAt: input.Now.UTC()}
	revision.Digest, _ = revision.CanonicalDigest()
	result := domain.EnvironmentTemplateSynthesis{SchemaVersion: domain.TemplateSynthesisSchemaVersion, TenantID: input.TenantID, ProjectID: input.ProjectID, ClusterID: input.ClusterID, SourceScanID: input.SourceScanID, Revision: revision, Graph: graph, Decisions: decisions, Unresolved: issues, AutonomousApply: len(issues) == 0}
	result.Digest, _ = result.CanonicalDigest()
	return result, nil
}

func classifySnapshot(snapshot domain.ResourceSnapshot, namespaces map[string]bool) (domain.ResourcePolicyStrategy, string, string, bool) {
	if snapshot.Kind == "Pod" || snapshot.Kind == "ReplicaSet" || snapshot.Kind == "ControllerRevision" || snapshot.Kind == "Endpoint" || snapshot.Kind == "EndpointSlice" || snapshot.Kind == "Event" || snapshot.Kind == "Lease" {
		return domain.ResourcePolicyIgnore, "ignore", "runtime child is not portable desired state", true
	}
	if !namespaces[snapshot.Namespace] || strings.EqualFold(snapshot.Labels["envplane.io/shared"], "true") || strings.EqualFold(snapshot.Labels["envplane.io/owner"], "platform") {
		return domain.ResourcePolicyReference, "reference", "shared or foreign resource requires explicit operator decision", false
	}
	if snapshot.Kind == "Secret" || snapshot.Kind == "PersistentVolumeClaim" {
		return domain.ResourcePolicyUnsupported, "block", "secret or persistent storage materialization requires an explicit strategy", false
	}
	if !synthesisCloneKinds[snapshot.Kind] {
		return domain.ResourcePolicyUnsupported, "block", "unsupported resource kind requires an explicit strategy", false
	}
	return domain.ResourcePolicyParameterize, "parameterize", "namespace, image and endpoint values are rendered through typed inputs", true
}

func parameterDecisions(snapshot domain.ResourceSnapshot) []domain.TemplateSynthesisDecision {
	result := make([]domain.TemplateSynthesisDecision, 0)
	var walk func(any, string)
	walk = func(value any, path string) {
		switch typed := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				next := path + "." + key
				lower := strings.ToLower(key)
				if lower == "image" || lower == "namespace" || lower == "host" || lower == "hostname" || lower == "url" {
					value := stringValue(typed[key])
					if isExternalEndpoint(value) {
						result = append(result, domain.TemplateSynthesisDecision{ResourceID: resourceID(snapshot), Action: "block", Strategy: string(domain.ResourcePolicyUnsupported), SourceType: "resource_snapshot", SourceID: resourceID(snapshot), Path: next, Confidence: 0.95, Reason: "external endpoint requires an explicit operator decision", AutonomousApply: false})
					} else {
						result = append(result, domain.TemplateSynthesisDecision{ResourceID: resourceID(snapshot), Action: "parameterize", Strategy: string(domain.ResourcePolicyParameterize), SourceType: "resource_snapshot", SourceID: resourceID(snapshot), Path: next, Confidence: 0.9, Reason: "typed render input candidate", AutonomousApply: true})
					}
				}
				walk(typed[key], next)
			}
		case []any:
			for index, item := range typed {
				walk(item, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	walk(snapshot.Manifest, "manifest")
	return result
}

func isExternalEndpoint(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.Contains(value, ".svc") || strings.Contains(value, "localhost") || strings.Contains(value, "127.0.0.1") {
		return false
	}
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.Contains(value, "://")
}

func confidenceFor(strategy domain.ResourcePolicyStrategy) float64 {
	if strategy == domain.ResourcePolicyParameterize {
		return 0.9
	}
	if strategy == domain.ResourcePolicyClone {
		return 1
	}
	return 0
}
func resourceID(snapshot domain.ResourceSnapshot) string {
	return snapshot.Kind + "/" + snapshot.Namespace + "/" + snapshot.Name
}
func stringValue(value any) string { text, _ := value.(string); return text }
func cloneMap(value map[string]any) map[string]any {
	data, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}
func synthesisRevisionID(scanID string, resources []domain.ResourceTemplate) string {
	data, _ := json.Marshal(resources)
	sum := sha256.Sum256(append([]byte(scanID+"|"), data...))
	return "synth-" + hex.EncodeToString(sum[:8])
}
