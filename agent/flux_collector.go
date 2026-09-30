package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/envplane/contracts/domain"
)

type FluxStatusCollector struct {
	source            FluxSource
	kustomizationName string
	environmentScoped bool
}

func NewFluxStatusCollector(source FluxSource) *FluxStatusCollector {
	return &FluxStatusCollector{source: source}
}

// NewProjectFluxStatusCollector limits Flux observation to a configured
// project Kustomization. It lets the Agent use exact Kubernetes GET access in
// a shared Flux namespace instead of listing other projects' resources.
func NewProjectFluxStatusCollector(source FluxSource, kustomizationName string) *FluxStatusCollector {
	return &FluxStatusCollector{source: source, kustomizationName: strings.TrimSpace(kustomizationName)}
}

// NewEnvironmentFluxStatusCollector reads only the Kustomization belonging to
// the environment currently being reported. The caller grants exact GET access
// to durable environment Kustomization names, so a project Agent never lists a
// shared Flux namespace.
func NewEnvironmentFluxStatusCollector(source FluxSource) *FluxStatusCollector {
	return &FluxStatusCollector{source: source, environmentScoped: true}
}

func (c *FluxStatusCollector) Collect(ctx context.Context, environmentID string, namespace Namespace) (domain.FluxStatus, error) {
	if c.environmentScoped {
		return c.collectEnvironment(ctx, environmentID, namespace)
	}
	kustomizations, err := c.kustomizations(ctx)
	if err != nil {
		return domain.FluxStatus{}, err
	}
	helmReleases, err := c.source.ListHelmReleases(ctx, namespace.Metadata.Name)
	if err != nil {
		return domain.FluxStatus{}, err
	}
	if c.kustomizationName != "" {
		return BuildFluxStatusForProjectKustomization(kustomizations, helmReleases), nil
	}
	return BuildFluxStatus(environmentID, namespace, kustomizations, helmReleases), nil
}

func (c *FluxStatusCollector) collectEnvironment(ctx context.Context, environmentID string, namespace Namespace) (domain.FluxStatus, error) {
	product := strings.TrimSpace(namespace.Metadata.Labels["envplane.io/product"])
	if strings.TrimSpace(environmentID) == "" || product == "" {
		return BuildFluxStatus(environmentID, namespace, nil, nil), nil
	}
	item, err := c.source.GetFluxKustomization(ctx, c.source.FluxNamespace(), environmentID+"."+product)
	if err != nil {
		return domain.FluxStatus{}, err
	}
	helmReleases, err := c.source.ListHelmReleases(ctx, namespace.Metadata.Name)
	if err != nil {
		return domain.FluxStatus{}, err
	}
	kustomizations := []FluxKustomization(nil)
	if strings.TrimSpace(item.Metadata.Name) != "" {
		kustomizations = []FluxKustomization{item}
	}
	return BuildFluxStatus(environmentID, namespace, kustomizations, helmReleases), nil
}

func (c *FluxStatusCollector) kustomizations(ctx context.Context) ([]FluxKustomization, error) {
	if c.kustomizationName == "" {
		return c.source.ListFluxKustomizations(ctx, c.source.FluxNamespace())
	}
	item, err := c.source.GetFluxKustomization(ctx, c.source.FluxNamespace(), c.kustomizationName)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(item.Metadata.Name) == "" {
		return nil, nil
	}
	return []FluxKustomization{item}, nil
}

func BuildFluxStatus(environmentID string, namespace Namespace, kustomizations []FluxKustomization, helmReleases []HelmRelease) domain.FluxStatus {
	kustomizationStatuses := make([]domain.FluxResourceStatus, 0, len(kustomizations))
	helmReleaseStatuses := make([]domain.FluxResourceStatus, 0, len(helmReleases))

	product := strings.TrimSpace(namespace.Metadata.Labels["envplane.io/product"])
	for _, item := range kustomizations {
		if !matchesEnvironmentKustomization(environmentID, product, item) {
			continue
		}
		kustomizationStatuses = append(kustomizationStatuses, fluxResourceStatus("Kustomization", item.Metadata, item.Status))
	}
	for _, item := range helmReleases {
		helmReleaseStatuses = append(helmReleaseStatuses, fluxResourceStatus("HelmRelease", item.Metadata, item.Status))
	}

	sort.Slice(kustomizationStatuses, func(i, j int) bool {
		return kustomizationStatuses[i].Name < kustomizationStatuses[j].Name
	})
	sort.Slice(helmReleaseStatuses, func(i, j int) bool {
		return helmReleaseStatuses[i].Name < helmReleaseStatuses[j].Name
	})

	status := aggregateFluxStatus(kustomizationStatuses, helmReleaseStatuses)
	return domain.FluxStatus{
		Status:         status,
		Message:        fluxStatusMessage(status, kustomizationStatuses, helmReleaseStatuses),
		Kustomizations: kustomizationStatuses,
		HelmReleases:   helmReleaseStatuses,
	}
}

// BuildFluxStatusForProjectKustomization combines the health of one
// project-owned Flux source with Helm releases in the current environment.
func BuildFluxStatusForProjectKustomization(kustomizations []FluxKustomization, helmReleases []HelmRelease) domain.FluxStatus {
	kustomizationStatuses := make([]domain.FluxResourceStatus, 0, len(kustomizations))
	helmReleaseStatuses := make([]domain.FluxResourceStatus, 0, len(helmReleases))
	for _, item := range kustomizations {
		kustomizationStatuses = append(kustomizationStatuses, fluxResourceStatus("Kustomization", item.Metadata, item.Status))
	}
	for _, item := range helmReleases {
		helmReleaseStatuses = append(helmReleaseStatuses, fluxResourceStatus("HelmRelease", item.Metadata, item.Status))
	}
	sort.Slice(kustomizationStatuses, func(i, j int) bool { return kustomizationStatuses[i].Name < kustomizationStatuses[j].Name })
	sort.Slice(helmReleaseStatuses, func(i, j int) bool { return helmReleaseStatuses[i].Name < helmReleaseStatuses[j].Name })
	status := aggregateFluxStatus(kustomizationStatuses, helmReleaseStatuses)
	return domain.FluxStatus{
		Status:         status,
		Message:        fluxStatusMessage(status, kustomizationStatuses, helmReleaseStatuses),
		Kustomizations: kustomizationStatuses,
		HelmReleases:   helmReleaseStatuses,
	}
}

func matchesEnvironmentKustomization(environmentID string, product string, item FluxKustomization) bool {
	name := strings.TrimSpace(item.Metadata.Name)
	if product != "" && name == environmentID+"."+product {
		return true
	}
	return strings.HasPrefix(name, environmentID+".")
}

func fluxResourceStatus(kind string, metadata FluxMetadata, status FluxStatus) domain.FluxResourceStatus {
	ready := false
	failed := false
	reason := ""
	message := ""
	var transition string
	for _, condition := range status.Conditions {
		if condition.Type == "Ready" {
			ready = condition.Status == "True"
			failed = condition.Status == "False"
			reason = firstNonEmpty(condition.Reason, reason)
			message = firstNonEmpty(condition.Message, message)
			transition = firstNonEmpty(condition.LastTransitionTime, transition)
			continue
		}
		if condition.Status == "True" && failedFluxCondition(condition.Type) {
			failed = true
			reason = firstNonEmpty(condition.Reason, reason)
			message = firstNonEmpty(condition.Message, message)
			transition = firstNonEmpty(condition.LastTransitionTime, transition)
		}
	}
	return domain.FluxResourceStatus{
		Kind:                   kind,
		Name:                   metadata.Name,
		Namespace:              metadata.Namespace,
		Ready:                  ready,
		Failed:                 failed,
		Reason:                 reason,
		Message:                message,
		ObservedGeneration:     status.ObservedGeneration,
		LastAppliedRevision:    status.LastAppliedRevision,
		LastAttemptedRevision:  status.LastAttemptedRevision,
		LastHandledReconcileAt: status.LastHandledReconcileAt,
		LastTransitionTime:     parseKubernetesTime(transition),
	}
}

func failedFluxCondition(conditionType string) bool {
	switch conditionType {
	case "Stalled", "Remediated":
		return true
	default:
		return false
	}
}

func aggregateFluxStatus(kustomizations []domain.FluxResourceStatus, helmReleases []domain.FluxResourceStatus) domain.EnvironmentStatus {
	total := len(kustomizations) + len(helmReleases)
	if total == 0 {
		return domain.StatusCreating
	}
	ready := 0
	for _, item := range append(kustomizations, helmReleases...) {
		if item.Failed {
			return domain.StatusFailed
		}
		if item.Ready {
			ready++
		}
	}
	if ready == total {
		return domain.StatusReady
	}
	return domain.StatusCreating
}

func fluxStatusMessage(status domain.EnvironmentStatus, kustomizations []domain.FluxResourceStatus, helmReleases []domain.FluxResourceStatus) string {
	parts := []string{fmt.Sprintf("flux status=%s", status)}
	for _, item := range kustomizations {
		parts = append(parts, fluxResourceMessage(item))
	}
	for _, item := range helmReleases {
		parts = append(parts, fluxResourceMessage(item))
	}
	return strings.Join(parts, "; ")
}

func fluxResourceMessage(item domain.FluxResourceStatus) string {
	message := fmt.Sprintf("%s/%s ready=%t", item.Kind, item.Name, item.Ready)
	if item.Failed {
		message += " failed=true"
	}
	if item.Reason != "" {
		message += " reason=" + item.Reason
	}
	if item.LastAppliedRevision != "" {
		message += " applied=" + item.LastAppliedRevision
	}
	if item.LastAttemptedRevision != "" {
		message += " attempted=" + item.LastAttemptedRevision
	}
	return message
}
