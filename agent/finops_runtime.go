package agent

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/envplane/contracts/domain"
)

// RunFinOpsMetering keeps no fabricated backfill. After a restart collection
// begins with fresh Metrics API windows, so gaps remain visible in the ledger.
func RunFinOpsMetering(ctx context.Context, cfg Config, source *KubernetesNamespaceSource, reporter *HTTPStatusReporter, logger *slog.Logger) {
	if source == nil || reporter == nil || cfg.BootstrapProjectID == "" || !strings.HasPrefix(cfg.ControlPlaneURL, "https://") {
		return
	}
	// Duplicate immutable windows are idempotent. Poll more frequently than
	// the usual Metrics API refresh; genuinely uncovered intervals stay gaps.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var pending *domain.FinOpsMeteringBatch
	for {
		if pending == nil {
			batch, err := source.CollectFinOps(ctx, cfg.BootstrapProjectID, cfg.ClusterID, cfg.AgentID, time.Now().UTC())
			if err == nil {
				pending = &batch
			} else if logger != nil {
				logger.Warn("FinOps collection unavailable")
			}
		}
		if pending != nil {
			err := SubmitFinOps(ctx, reporter.client, cfg.ControlPlaneURL, reporter.Token(), *pending)
			if err == nil {
				pending = nil
			} else if logger != nil {
				logger.Warn("FinOps evidence delivery unavailable")
			}
			if pending != nil && time.Since(pending.PeriodEnd) > 5*time.Minute {
				pending = nil
			} // discard rejected/stale batch; leave a visible gap
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
