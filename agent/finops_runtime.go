package agent

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/envplane/contracts/domain"
)

// RunFinOpsMetering keeps no fabricated backfill. After a restart collection
// begins with fresh Metrics API windows, so gaps remain visible in the ledger.
func RunFinOpsMetering(ctx context.Context, cfg Config, source *KubernetesNamespaceSource, reporter *HTTPStatusReporter, logger *slog.Logger) {
	RunFinOpsMeteringWithDimensions(ctx, cfg, source, reporter, nil, logger)
}

func RunFinOpsMeteringWithDimensions(ctx context.Context, cfg Config, source *KubernetesNamespaceSource, reporter *HTTPStatusReporter, dimensions FinOpsDimensionSource, logger *slog.Logger) {
	if source == nil || reporter == nil || cfg.BootstrapProjectID == "" || !strings.HasPrefix(cfg.ControlPlaneURL, "https://") {
		return
	}
	// Duplicate immutable windows are idempotent. Poll more frequently than
	// the usual Metrics API refresh; genuinely uncovered intervals stay gaps.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	state := finOpsDeliveryState{}
	for {
		err := state.step(ctx, time.Now().UTC(), func() (domain.FinOpsMeteringBatch, error) {
			return source.CollectFinOps(ctx, cfg.BootstrapProjectID, cfg.ClusterID, cfg.AgentID, time.Now().UTC())
		}, func(batch *domain.FinOpsMeteringBatch) {
			owned, _ := source.FinOpsOwnedPodInventory(ctx, cfg.BootstrapProjectID)
			_ = source.AttachFinOpsDimensions(ctx, batch, dimensions, owned)
		}, func(ctx context.Context, batch domain.FinOpsMeteringBatch) error {
			return SubmitFinOps(ctx, reporter.client, cfg.ControlPlaneURL, reporter.Token(), batch)
		})
		if err != nil && logger != nil {
			var failure *FinOpsDeliveryError
			if errors.As(err, &failure) && failure != nil {
				logger.Warn("FinOps delivery unavailable", "delivery_class", failure.Class(), "http_status", failure.StatusCode)
			} else {
				logger.Warn("FinOps collection or delivery configuration unavailable")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
