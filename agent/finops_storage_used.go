package agent

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"time"

	"github.com/envplane/contracts/domain"
)

const FinOpsStorageUsed = domain.FinOpsStorageUsed

func (s *KubernetesNamespaceSource) FinOpsOwnedPVCInventory(ctx context.Context, project string, start time.Time) ([]FinOpsOwnedResource, error) {
	namespaces, err := s.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	result := []FinOpsOwnedResource{}
	for _, ns := range namespaces {
		if ns.Metadata.Labels["envplane.io/project-id"] != project {
			continue
		}
		env := ns.Metadata.Labels[environmentIDLabel]
		if env == "" {
			return nil, errors.New("PVC namespace attribution missing")
		}
		err = s.listPages(ctx, s.apiURL+"/api/v1/namespaces/"+url.PathEscape(ns.Metadata.Name)+"/persistentvolumeclaims", "FinOps PVC ownership", func(raw json.RawMessage) error {
			var pvc finOpsPVC
			if e := json.Unmarshal(raw, &pvc); e != nil {
				return e
			}
			component, _ := FinOpsComponentID(pvc.Metadata.Labels)
			if component == "" || pvc.Metadata.Name == "" || pvc.Metadata.UID == "" || pvc.Metadata.CreatedAt.IsZero() || pvc.Metadata.CreatedAt.After(start) || pvc.Status.Phase != "Bound" || pvc.Spec.VolumeName == "" || (pvc.Metadata.Labels[environmentIDLabel] != "" && pvc.Metadata.Labels[environmentIDLabel] != env) {
				return errors.New("PVC identity/generation unavailable")
			}
			capacity, e := finOpsQuantity(pvc.Status.Capacity["storage"])
			if e != nil || capacity <= 0 || capacity >= float64(math.MaxInt64) || math.Trunc(capacity) != capacity {
				return errors.New("PVC provisioned capacity unavailable")
			}
			result = append(result, FinOpsOwnedResource{Namespace: ns.Metadata.Name, PVCName: pvc.Metadata.Name, ResourceUID: pvc.Metadata.UID, EnvironmentID: env, ComponentID: component, ProvisionedBytes: int64(capacity)})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
