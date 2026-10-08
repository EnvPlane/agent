package agent

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/envplane/contracts/domain"
)

// FinOpsDeliveryError retains only classification/status, never a URL, bearer,
// provider body or raw transport error. StatusCode zero means transport failure.
type FinOpsDeliveryError struct{ StatusCode int }

func (e *FinOpsDeliveryError) Error() string { return "FinOps delivery " + e.Class() }
func (e *FinOpsDeliveryError) Class() string {
	switch {
	case e.StatusCode == 0:
		return "transport"
	case e.StatusCode == http.StatusTooManyRequests:
		return "rate-limit"
	case e.StatusCode >= 500 && e.StatusCode < 600:
		return "server"
	default:
		return "terminal"
	}
}
func FinOpsDeliveryRetryable(err error) bool {
	var failure *FinOpsDeliveryError
	return errors.As(err, &failure) && failure != nil && failure.Class() != "terminal"
}

type finOpsDeliveryState struct {
	pending  *domain.FinOpsMeteringBatch
	finished map[string]time.Time
}

// step freezes decoration while retrying and skips recently finished metric
// windows before acquiring fresh inventory/metadata. After a restart an old
// conflicting cached window is rejected once and then skipped until a new ID.
func (s *finOpsDeliveryState) step(ctx context.Context, now time.Time, collect func() (domain.FinOpsMeteringBatch, error), decorate func(*domain.FinOpsMeteringBatch), submit func(context.Context, domain.FinOpsMeteringBatch) error) error {
	if s.finished == nil {
		s.finished = map[string]time.Time{}
	}
	for id, at := range s.finished {
		if now.Sub(at) > 10*time.Minute {
			delete(s.finished, id)
		}
	}
	finish := func() {
		s.finished[s.pending.BatchID] = now
		s.pending = nil
		if len(s.finished) > 64 {
			var oldest string
			var timestamp time.Time
			for id, at := range s.finished {
				if oldest == "" || at.Before(timestamp) {
					oldest = id
					timestamp = at
				}
			}
			delete(s.finished, oldest)
		}
	}
	if s.pending == nil {
		batch, err := collect()
		if err != nil {
			return err
		}
		if batch.BatchID == "" {
			return errors.New("FinOps batch identity unavailable")
		}
		if _, done := s.finished[batch.BatchID]; done {
			return nil
		}
		decorate(&batch)
		s.pending = &batch
	}
	if now.Sub(s.pending.PeriodEnd) > 5*time.Minute {
		finish()
		return errors.New("FinOps pending window expired")
	}
	err := submit(ctx, *s.pending)
	if err == nil || !FinOpsDeliveryRetryable(err) {
		finish()
	}
	return err
}
