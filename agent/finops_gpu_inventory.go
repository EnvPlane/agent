package agent

import (
	"context"
	"time"

	"github.com/envplane/contracts/domain"
)

// FinOpsGPUInventorySource wraps an optional metrics collector with a trusted
// cluster-node inventory reader. It does not request exec/host access, or infer
// absence of hardware from missing DCGM series or absent Pod GPU requests.
type FinOpsGPUInventorySource struct {
	Metrics   FinOpsDimensionSource
	Inventory func(context.Context) (domain.FinOpsGPUInventory, error)
}

func (s *FinOpsGPUInventorySource) Collect(ctx context.Context, d domain.FinOpsDimension, owned []FinOpsOwnedResource, start, end time.Time) (domain.FinOpsDimensionReport, error) {
	r := dimensionReport(d, start, end)
	if d == domain.FinOpsGPUUtilization && s.Inventory != nil {
		inventory, err := s.Inventory(ctx)
		if err == nil && inventory.ClusterID != "" && inventory.Source == "kubernetes-node-capacity" && inventory.Nodes > 0 && inventory.Devices == 0 && !inventory.ObservedAt.IsZero() && !inventory.ObservedAt.Before(end.Add(-5*time.Minute)) && !inventory.ObservedAt.After(end.Add(time.Minute)) {
			r.State = "not_applicable"
			r.Reason = "no_devices"
			r.Source = "kubernetes-node-capacity"
			r.GPUInventory = &inventory
			return r, nil
		}
	}
	if s.Metrics != nil {
		return s.Metrics.Collect(ctx, d, owned, start, end)
	}
	return r, nil
}
