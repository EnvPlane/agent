package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/envplane/contracts/domain"
)

// RunBaselineMetering is an explicit opt-in caller. It measures PVC capacity,
// not used storage, workload consumption, prices or complete baseline coverage.
// No backfill is synthesized after restarts or failed collection/delivery.
func RunBaselineMetering(ctx context.Context, cfg Config, source *KubernetesNamespaceSource, reporter *HTTPStatusReporter, logger *slog.Logger) {
	if source == nil || reporter == nil || cfg.RemoteGeneration < 1 || cfg.BootstrapProjectID == "" || !strings.HasPrefix(cfg.ControlPlaneURL, "https://") {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	state := baselineDeliveryState{start: time.Now().UTC()}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			err := state.step(ctx, now.UTC(), func(start, end time.Time) (domain.BaselineMeteringBatch, error) {
				bindings, err := FetchBaselineBindings(ctx, reporter.client, cfg.ControlPlaneURL, reporter.Token(), cfg.BootstrapProjectID, cfg.ClusterID, cfg.AgentID, cfg.RemoteGeneration)
				if err != nil {
					return domain.BaselineMeteringBatch{}, err
				}
				// A newly registered pin starts a fresh window, never historical ownership.
				current := []domain.BaseResourceBinding{}
				for _, b := range bindings {
					if !start.Before(b.CreatedAt) {
						current = append(current, b)
					}
				}
				samples, err := source.CollectBaselinePVCCapacity(ctx, current, cfg.BootstrapProjectID, cfg.ClusterID, cfg.RemoteGeneration, start, end)
				if err != nil {
					return domain.BaselineMeteringBatch{}, err
				}
				batch := domain.BaselineMeteringBatch{ProjectID: cfg.BootstrapProjectID, ClusterID: cfg.ClusterID, AgentID: cfg.AgentID, PeriodStart: start, PeriodEnd: end, Samples: samples}
				payload, err := json.Marshal(batch)
				if err != nil {
					return domain.BaselineMeteringBatch{}, err
				}
				id := sha256.Sum256(payload)
				batch.BatchID = hex.EncodeToString(id[:])
				return batch, nil
			}, func(ctx context.Context, b domain.BaselineMeteringBatch) error {
				return SubmitBaselineMetering(ctx, reporter.client, cfg.ControlPlaneURL, reporter.Token(), b)
			})
			if err != nil && logger != nil {
				logger.Warn("baseline capacity collection or delivery unavailable")
			}
		}
	}
}

type baselineDeliveryState struct {
	start   time.Time
	pending *domain.BaselineMeteringBatch
}

func (s *baselineDeliveryState) step(ctx context.Context, now time.Time, collect func(time.Time, time.Time) (domain.BaselineMeteringBatch, error), submit func(context.Context, domain.BaselineMeteringBatch) error) error {
	if s.pending == nil {
		start := s.start
		s.start = now // Missing windows remain gaps, not fabricated history.
		if now.Sub(start) > 5*time.Minute {
			start = now.Add(-time.Minute)
		}
		batch, err := collect(start, now)
		if err != nil {
			return err
		}
		if len(batch.Samples) == 0 {
			return nil
		}
		s.pending = &batch
	}
	if now.Sub(s.pending.PeriodEnd) > 5*time.Minute {
		s.pending = nil
		s.start = now
		return nil
	}
	err := submit(ctx, *s.pending)
	if err == nil || !FinOpsDeliveryRetryable(err) {
		s.pending = nil
		s.start = now
	}
	return err
}
