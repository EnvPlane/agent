package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/envplane/contracts/domain"
	"log/slog"
	"net/url"
	"time"
)

// RunBaselineMeasuredMetering samples observed Metrics API windows separately
// from approved Prometheus windows. Absent sources remain gaps, not zeroes.
func RunBaselineMeasuredMetering(ctx context.Context, cfg Config, source *KubernetesNamespaceSource, reporter *HTTPStatusReporter, dimensions FinOpsDimensionSource, logger *slog.Logger) {
	if source == nil || reporter == nil || cfg.RemoteGeneration < 1 || cfg.BootstrapProjectID == "" {
		return
	}
	var prom *FinOpsPrometheusSource
	if wrapper, ok := dimensions.(*FinOpsGPUInventorySource); ok {
		prom, _ = wrapper.Metrics.(*FinOpsPrometheusSource)
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	states := map[string]*baselineDeliveryState{}
	last := time.Now().UTC()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			now = now.UTC()
			bindings, err := FetchBaselineBindings(ctx, reporter.client, cfg.ControlPlaneURL, reporter.Token(), cfg.BootstrapProjectID, cfg.ClusterID, cfg.AgentID, cfg.RemoteGeneration)
			if err != nil || len(bindings) > 64 {
				cancel()
				last = now
				if logger != nil {
					logger.Warn("baseline measured registry unavailable or bound exceeded")
				}
				continue
			}
			active := map[string]bool{}
			for _, b := range bindings {
				groups := []string{"prometheus"}
				if b.ResourceKind == "Pod" {
					groups = append(groups, "metrics-api")
				}
				for _, group := range groups {
					key := b.ID + "|" + group
					active[key] = true
					state := states[key]
					if state == nil {
						state = &baselineDeliveryState{start: last}
						states[key] = state
					}
					err = state.step(ctx, now, func(start, end time.Time) (domain.BaselineMeteringBatch, error) {
						var samples []domain.BaselineMeteringSample
						var e error
						if group == "metrics-api" {
							var m finOpsMetric
							if source.baselineGET(ctx, "/apis/metrics.k8s.io/v1beta1/namespaces/"+url.PathEscape(b.Namespace)+"/pods/"+url.PathEscape(b.ResourceName), &m) != nil {
								return domain.BaselineMeteringBatch{}, errors.New("baseline observed window unavailable")
							}
							window, parseErr := time.ParseDuration(m.Window)
							if parseErr != nil || window <= 0 || window > 5*time.Minute || m.Timestamp.After(now.Add(time.Minute)) || m.Timestamp.Before(now.Add(-5*time.Minute)) {
								return domain.BaselineMeteringBatch{}, errors.New("baseline observed window gap")
							}
							start, end = m.Timestamp.Add(-window), m.Timestamp
							samples, e = source.CollectBaselinePodUsage(ctx, b, cfg.BootstrapProjectID, cfg.ClusterID, cfg.RemoteGeneration, start, end)
						} else {
							samples, e = source.CollectBaselineMeasured(ctx, prom, b, cfg.BootstrapProjectID, cfg.ClusterID, cfg.RemoteGeneration, start, end)
						}
						if e != nil {
							return domain.BaselineMeteringBatch{}, e
						}
						batch := domain.BaselineMeteringBatch{ProjectID: cfg.BootstrapProjectID, ClusterID: cfg.ClusterID, AgentID: cfg.AgentID, PeriodStart: start, PeriodEnd: end, Samples: samples}
						data, e := json.Marshal(batch)
						if e != nil {
							return batch, e
						}
						hash := sha256.Sum256(data)
						batch.BatchID = hex.EncodeToString(hash[:])
						return batch, nil
					}, func(ctx context.Context, batch domain.BaselineMeteringBatch) error {
						return SubmitBaselineMetering(ctx, reporter.client, cfg.ControlPlaneURL, reporter.Token(), batch)
					})
					if err != nil && logger != nil {
						logger.Warn("baseline measured evidence gap", "source_class", group)
					}
				}
			}
			for key := range states {
				if !active[key] {
					delete(states, key)
				}
			}
			last = now
			cancel()
		}
	}
}
