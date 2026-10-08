package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"time"

	"github.com/envplane/contracts/domain"
)

// CollectBaselineMeasured reuses the approved Prometheus transport and exact
// UID selectors. Its private attribution branch never manufactures Environment IDs.
// Missing/incomplete sources return an error, never a zero-consumption receipt.
func (s *KubernetesNamespaceSource) CollectBaselineMeasured(ctx context.Context, prom *FinOpsPrometheusSource, b domain.BaseResourceBinding, project, cluster string, generation int64, start, end time.Time) ([]domain.BaselineMeteringSample, error) {
	if !end.After(start) || end.Sub(start) > 5*time.Minute || start.Before(b.CreatedAt) || s.VerifyBaselineBinding(ctx, b, project, cluster, generation) != nil {
		return nil, errors.New("baseline measured authority unavailable")
	}
	owned := FinOpsOwnedResource{Namespace: b.Namespace, ResourceUID: b.ResourceUID, ComponentID: b.ComponentID, baselineBindingID: b.ID, baselineVersion: b.Version}
	dims := []domain.FinOpsDimension{FinOpsStorageUsed}
	switch b.ResourceKind {
	case "PersistentVolumeClaim":
		owned.PVCName = b.ResourceName
		var pvc finOpsPVC
		if s.baselineGET(ctx, "/api/v1/namespaces/"+url.PathEscape(b.Namespace)+"/persistentvolumeclaims/"+url.PathEscape(b.ResourceName), &pvc) != nil || pvc.Metadata.UID != b.ResourceUID || pvc.Status.Phase != "Bound" {
			return nil, errors.New("baseline storage capacity unavailable")
		}
		bytes, err := finOpsQuantity(pvc.Status.Capacity["storage"])
		if err != nil || bytes <= 0 || bytes >= float64(1<<63) {
			return nil, errors.New("baseline storage capacity unavailable")
		}
		owned.ProvisionedBytes = int64(bytes)
	case "Pod":
		owned.PodName = b.ResourceName
		dims = []domain.FinOpsDimension{domain.FinOpsNetworkReceive, domain.FinOpsNetworkTransmit}
	default:
		return nil, errors.New("unsupported baseline metric resource")
	}
	out := []domain.BaselineMeteringSample{}
	for _, d := range dims {
		report, err := prom.collect(ctx, d, []FinOpsOwnedResource{owned}, start, end, true)
		if err != nil || report.State != "complete" || len(report.Samples) != 1 || report.ObservedResources != 1 {
			return nil, errors.New("baseline measured source gap")
		}
		sample := report.Samples[0]
		if sample.EnvironmentID != "" || sample.ResourceUID != b.ResourceUID || sample.ComponentID != b.ComponentID || sample.Namespace != b.Namespace {
			return nil, errors.New("baseline measured identity mismatch")
		}
		out = append(out, domain.BaselineMeteringSample{BaseResourcePin: b.BaseResourcePin, Attribution: domain.FinOpsResourceAttribution{BaseResourceBindingID: b.ID, BindingVersion: b.Version}, SampleID: sample.SampleID, Metric: string(d), Unit: report.Unit, MeasurementKind: report.MeasurementKind, Source: report.Source, Quantity: sample.Quantity, UsedBytes: sample.UsedBytes})
	}
	if s.VerifyBaselineBinding(ctx, b, project, cluster, generation) != nil {
		return nil, errors.New("baseline changed during measured observation")
	}
	return out, nil
}

// Metrics API averages are integrated only inside their actual observed window,
// after checking exact current Pod UID, creation time and full container coverage.
func (s *KubernetesNamespaceSource) CollectBaselinePodUsage(ctx context.Context, b domain.BaseResourceBinding, project, cluster string, generation int64, start, end time.Time) ([]domain.BaselineMeteringSample, error) {
	if b.ResourceKind != "Pod" || !end.After(start) || end.Sub(start) > 5*time.Minute || start.Before(b.CreatedAt) || s.VerifyBaselineBinding(ctx, b, project, cluster, generation) != nil {
		return nil, errors.New("baseline Pod authority unavailable")
	}
	path := "/api/v1/namespaces/" + url.PathEscape(b.Namespace) + "/pods/" + url.PathEscape(b.ResourceName)
	var pod finOpsPod
	if s.baselineGET(ctx, path, &pod) != nil || pod.Metadata.UID != b.ResourceUID || pod.Metadata.CreatedAt.IsZero() || pod.Metadata.CreatedAt.After(start) {
		return nil, errors.New("baseline Pod identity unavailable")
	}
	var m finOpsMetric
	if s.baselineGET(ctx, "/apis/metrics.k8s.io/v1beta1/namespaces/"+url.PathEscape(b.Namespace)+"/pods/"+url.PathEscape(b.ResourceName), &m) != nil {
		return nil, errors.New("baseline Pod metrics unavailable")
	}
	window, err := time.ParseDuration(m.Window)
	if err != nil || window <= 0 || window > 5*time.Minute || m.Metadata.Name != b.ResourceName || start.Before(m.Timestamp.Add(-window)) || end.After(m.Timestamp) {
		return nil, errors.New("baseline Pod metrics window gap")
	}
	expected := map[string]bool{}
	for _, c := range pod.Spec.Containers {
		if expected[c.Name] || c.Name == "" {
			return nil, errors.New("baseline container identity unavailable")
		}
		expected[c.Name] = true
	}
	if len(expected) == 0 || len(expected) != len(m.Containers) {
		return nil, errors.New("baseline container metrics gap")
	}
	cpu, memory := 0.0, 0.0
	for _, c := range m.Containers {
		if !expected[c.Name] {
			return nil, errors.New("baseline container metrics gap")
		}
		delete(expected, c.Name)
		cores, e := finOpsQuantity(c.Usage["cpu"])
		bytes, e2 := finOpsQuantity(c.Usage["memory"])
		if e != nil || e2 != nil || cores < 0 || bytes < 0 {
			return nil, errors.New("baseline container metrics unavailable")
		}
		cpu += cores
		memory += bytes
	}
	if s.VerifyBaselineBinding(ctx, b, project, cluster, generation) != nil {
		return nil, errors.New("baseline Pod replaced during observation")
	}
	out := []domain.BaselineMeteringSample{}
	for i, metric := range []string{"cpu.usage", "memory.usage"} {
		id := sha256.Sum256([]byte(b.ID + "|" + metric + "|" + start.Format(time.RFC3339Nano) + "|" + end.Format(time.RFC3339Nano)))
		units := []string{"core_hours", domain.FinOpsGiBHours}
		values := []float64{cpu * end.Sub(start).Hours(), memory / (1 << 30) * end.Sub(start).Hours()}
		out = append(out, domain.BaselineMeteringSample{BaseResourcePin: b.BaseResourcePin, Attribution: domain.FinOpsResourceAttribution{BaseResourceBindingID: b.ID, BindingVersion: b.Version}, SampleID: hex.EncodeToString(id[:]), Metric: metric, Unit: units[i], MeasurementKind: domain.FinOpsMeasured, Source: "kubernetes-metrics-api", Quantity: values[i]})
	}
	return out, nil
}
