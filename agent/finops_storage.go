package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/envplane/contracts/domain"
)

func dimensionReport(d domain.FinOpsDimension, start, end time.Time) domain.FinOpsDimensionReport {
	r := domain.FinOpsDimensionReport{Dimension: d, PeriodStart: start, PeriodEnd: end, State: "unavailable", Reason: "source-unconfigured", Samples: []domain.FinOpsDimensionSample{}}
	switch d {
	case domain.FinOpsStorageRequested, domain.FinOpsStorageProvisioned:
		r.Unit = domain.FinOpsGiBHours
		r.MeasurementKind = domain.FinOpsCapacity
		r.Source = "kubernetes-pvc-api"
	case FinOpsStorageUsed:
		r.Unit = domain.FinOpsGiBHours
		r.MeasurementKind = domain.FinOpsMeasured
		r.Source = "prometheus-kubelet-volume-gauge"
	case domain.FinOpsNetworkTransmit, domain.FinOpsNetworkReceive:
		r.Unit = domain.FinOpsGiB
		r.MeasurementKind = domain.FinOpsMeasured
		r.Source = "prometheus-cadvisor-counter"
	case domain.FinOpsGPUUtilization:
		r.Unit = domain.FinOpsBusyGPUHours
		r.MeasurementKind = domain.FinOpsMeasured
		r.Source = "prometheus-dcgm-gauge"
	}
	return r
}

type finOpsPVC struct {
	Metadata struct {
		Name      string            `json:"name"`
		UID       string            `json:"uid"`
		CreatedAt time.Time         `json:"creationTimestamp"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		VolumeName string `json:"volumeName"`
		Resources  struct {
			Requests map[string]string `json:"requests"`
		} `json:"resources"`
	} `json:"spec"`
	Status struct {
		Phase    string            `json:"phase"`
		Capacity map[string]string `json:"capacity"`
	} `json:"status"`
}

// CollectFinOpsStorage reports PVC requests and driver-reported provisioned
// capacity. Neither is used filesystem bytes or authoritative provider billing.
func (s *KubernetesNamespaceSource) CollectFinOpsStorage(ctx context.Context, project string, start, end time.Time) ([]domain.FinOpsDimensionReport, error) {
	reports := []domain.FinOpsDimensionReport{dimensionReport(domain.FinOpsStorageRequested, start, end), dimensionReport(domain.FinOpsStorageProvisioned, start, end)}
	if !end.After(start) {
		return reports, nil
	}
	namespaces, err := s.ListNamespaces(ctx)
	if err != nil {
		return reports, err
	}
	for _, ns := range namespaces {
		if ns.Metadata.Labels["envplane.io/project-id"] != project {
			continue
		}
		var pvcs []finOpsPVC
		err = s.listPages(ctx, s.apiURL+"/api/v1/namespaces/"+url.PathEscape(ns.Metadata.Name)+"/persistentvolumeclaims", "FinOps PVC capacity", func(raw json.RawMessage) error {
			var pvc finOpsPVC
			if e := json.Unmarshal(raw, &pvc); e != nil {
				return e
			}
			pvcs = append(pvcs, pvc)
			return nil
		})
		if err != nil {
			return reports, err
		}
		for _, pvc := range pvcs {
			for i := range reports {
				reports[i].ExpectedResources++
			}
			env := ns.Metadata.Labels[environmentIDLabel]
			component, _ := FinOpsComponentID(pvc.Metadata.Labels)
			if env == "" || component == "" || pvc.Metadata.UID == "" || pvc.Metadata.CreatedAt.IsZero() || pvc.Metadata.CreatedAt.After(start) || (pvc.Metadata.Labels[environmentIDLabel] != "" && pvc.Metadata.Labels[environmentIDLabel] != env) {
				continue
			}
			values := []string{pvc.Spec.Resources.Requests["storage"], pvc.Status.Capacity["storage"]}
			for i, value := range values {
				if i == 1 && (pvc.Status.Phase != "Bound" || pvc.Spec.VolumeName == "") {
					continue
				}
				bytes, e := finOpsQuantity(value)
				if e != nil || bytes <= 0 {
					continue
				}
				id := sha256.Sum256([]byte(string(reports[i].Dimension) + "|" + pvc.Metadata.UID + "|" + start.Format(time.RFC3339Nano) + "|" + end.Format(time.RFC3339Nano)))
				reports[i].Samples = append(reports[i].Samples, domain.FinOpsDimensionSample{SampleID: hex.EncodeToString(id[:]), EnvironmentID: env, ComponentID: component, Namespace: ns.Metadata.Name, ResourceUID: pvc.Metadata.UID, Quantity: bytes / (1 << 30) * end.Sub(start).Hours()})
				reports[i].ObservedResources++
			}
		}
	}
	for i := range reports {
		r := &reports[i]
		r.Reason = "ownership-or-capacity-missing"
		if r.ExpectedResources > 0 && r.ObservedResources == r.ExpectedResources {
			r.State = "complete"
			r.Reason = ""
		} else if r.ObservedResources > 0 {
			r.State = "partial"
		} else {
			r.State = "unavailable"
		}
	}
	return reports, nil
}

type FinOpsOwnedResource struct {
	ProvisionedBytes                                            int64
	HostNetwork                                                 bool
	PVCName                                                     string
	Namespace, PodName, ResourceUID, EnvironmentID, ComponentID string
	ExpectedGPUs                                                int
}
type FinOpsDimensionSource interface {
	Collect(context.Context, domain.FinOpsDimension, []FinOpsOwnedResource, time.Time, time.Time) (domain.FinOpsDimensionReport, error)
}

// Pod inventory comes from the scoped Kubernetes source, never from metric
// labels alone. GPU requests establish expected device count, not actual use.
func (s *KubernetesNamespaceSource) FinOpsOwnedPodInventory(ctx context.Context, project string) ([]FinOpsOwnedResource, error) {
	namespaces, err := s.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	result := []FinOpsOwnedResource{}
	for _, ns := range namespaces {
		env := ns.Metadata.Labels[environmentIDLabel]
		if ns.Metadata.Labels["envplane.io/project-id"] != project {
			continue
		}
		if env == "" {
			return nil, errors.New("project namespace attribution missing")
		}
		err = s.listPages(ctx, s.apiURL+"/api/v1/namespaces/"+url.PathEscape(ns.Metadata.Name)+"/pods", "FinOps ownership", func(raw json.RawMessage) error {
			var p finOpsPod
			if e := json.Unmarshal(raw, &p); e != nil {
				return e
			}
			component, _ := FinOpsComponentID(p.Metadata.Labels)
			if component == "" || p.Metadata.UID == "" || (p.Metadata.Labels[environmentIDLabel] != "" && p.Metadata.Labels[environmentIDLabel] != env) {
				return errors.New("Pod attribution missing")
			}
			gpus := 0
			for _, container := range p.Spec.Containers {
				if value, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
					number, e := finOpsQuantity(value)
					if e != nil || number != float64(int(number)) || number < 0 || number > 1024 {
						return nil
					}
					gpus += int(number)
				}
			}
			result = append(result, FinOpsOwnedResource{Namespace: ns.Metadata.Name, PodName: p.Metadata.Name, ResourceUID: p.Metadata.UID, EnvironmentID: env, ComponentID: component, ExpectedGPUs: gpus, HostNetwork: p.Spec.HostNetwork})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// AttachFinOpsDimensions is the explicit runtime integration point. A nil
// external source produces unavailable network/GPU reports, never zero usage.
func (s *KubernetesNamespaceSource) AttachFinOpsDimensions(ctx context.Context, b *domain.FinOpsMeteringBatch, external FinOpsDimensionSource, owned []FinOpsOwnedResource) error {
	storage, err := s.CollectFinOpsStorage(ctx, b.ProjectID, b.PeriodStart, b.PeriodEnd)
	if err != nil {
		storage = []domain.FinOpsDimensionReport{dimensionReport(domain.FinOpsStorageRequested, b.PeriodStart, b.PeriodEnd), dimensionReport(domain.FinOpsStorageProvisioned, b.PeriodStart, b.PeriodEnd)}
	}
	b.Dimensions = storage
	for _, d := range []domain.FinOpsDimension{domain.FinOpsNetworkTransmit, domain.FinOpsNetworkReceive, domain.FinOpsGPUUtilization, FinOpsStorageUsed} {
		r := dimensionReport(d, b.PeriodStart, b.PeriodEnd)
		if external != nil {
			inventory := owned
			if d == FinOpsStorageUsed {
				inventory, _ = s.FinOpsOwnedPVCInventory(ctx, b.ProjectID, b.PeriodStart)
			}
			observed, e := external.Collect(ctx, d, inventory, b.PeriodStart, b.PeriodEnd)
			if e == nil {
				r = observed
			} else {
				r.Reason = "source-unavailable-or-unattributed"
				r.ExpectedResources = observed.ExpectedResources
			}
		}
		b.Dimensions = append(b.Dimensions, r)
	}
	return err
}
