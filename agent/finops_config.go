package agent

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/envplane/contracts/domain"
)

// NewConfiguredFinOpsDimensionSource performs no network request. Node reads
// occur only when explicitly enabled; optional metrics errors do not prevent
// the Agent's ordinary reconciliation loops from running.
func NewConfiguredFinOpsDimensionSource(cfg Config, source *KubernetesNamespaceSource) (FinOpsDimensionSource, error) {
	wrapper := &FinOpsGPUInventorySource{}
	if cfg.FinOpsNodeInventoryEnabled && source != nil {
		wrapper.Inventory = func(ctx context.Context) (domain.FinOpsGPUInventory, error) {
			return source.FinOpsGPUNodeInventory(ctx, cfg.ClusterID)
		}
	}
	if strings.TrimSpace(cfg.FinOpsPrometheusEndpoint) != "" {
		client, err := NewControlPlaneHTTPClientWithTLS(15*time.Second, cfg.FinOpsPrometheusCAFile, cfg.FinOpsPrometheusTLSServerName)
		if err != nil {
			return wrapper, errors.New("FinOps metrics CA configuration invalid")
		}
		metrics, err := NewFinOpsPrometheusSource(cfg.FinOpsPrometheusEndpoint, cfg.FinOpsPrometheusAllowedOrigins, client)
		if err != nil {
			return wrapper, errors.New("FinOps metrics HTTPS origin configuration invalid")
		}
		wrapper.Metrics = metrics
	}
	return wrapper, nil
}

// FinOpsGPUNodeInventory reads complete paginated Kubernetes capacity only.
// Errors or unknown GPU quantities invalidate the whole inventory; nothing is
// inferred from an empty list, Pod requests, exporter absence or allocatable.
func (s *KubernetesNamespaceSource) FinOpsGPUNodeInventory(ctx context.Context, clusterID string) (domain.FinOpsGPUInventory, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	unknown := domain.FinOpsGPUInventory{}
	if s == nil || clusterID == "" {
		return unknown, errors.New("GPU node inventory unavailable")
	}
	nodes, devices := 0, 0
	seen := map[string]bool{}
	err := s.listPages(ctx, s.apiURL+"/api/v1/nodes", "FinOps node capacity", func(raw json.RawMessage) error {
		var node struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
			Status struct {
				Capacity map[string]string `json:"capacity"`
			} `json:"status"`
		}
		if json.Unmarshal(raw, &node) != nil || node.Metadata.UID == "" || seen[node.Metadata.UID] || len(node.Status.Capacity) == 0 {
			return errors.New("incomplete GPU node inventory")
		}
		seen[node.Metadata.UID] = true
		nodes++
		if nodes > 10000 {
			return errors.New("node inventory bound exceeded")
		}
		for key, value := range node.Status.Capacity {
			gpu := key == "nvidia.com/gpu" || key == "amd.com/gpu"
			unknownGPU := strings.Contains(strings.ToLower(key), "gpu") || strings.HasPrefix(key, "nvidia.com/mig-") || key == "intel.com/i915" || key == "intel.com/xe"
			if !gpu && !unknownGPU {
				continue
			}
			quantity, e := finOpsQuantity(value)
			if e != nil || quantity < 0 || quantity > 10000 || math.Trunc(quantity) != quantity {
				return errors.New("unknown GPU capacity quantity")
			}
			if !gpu && quantity > 0 {
				return errors.New("unsupported GPU capacity inventory")
			}
			if gpu {
				devices += int(quantity)
			}
		}
		return nil
	})
	if err != nil || nodes == 0 {
		return unknown, errors.New("GPU node inventory unavailable")
	}
	return domain.FinOpsGPUInventory{ClusterID: clusterID, ObservedAt: time.Now().UTC(), Nodes: nodes, Devices: devices, Source: "kubernetes-node-capacity"}, nil
}
