package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/envplane/contracts/domain"
)

func TestFinOpsDeliverySkipsAcknowledgedCachedMetricsBeforeDecoration(t *testing.T) {
	now := time.Now().UTC()
	id := "window-a"
	state := finOpsDeliveryState{}
	decorations, sends := 0, 0
	collect := func() (domain.FinOpsMeteringBatch, error) {
		return domain.FinOpsMeteringBatch{BatchID: id, PeriodEnd: now}, nil
	}
	decorate := func(b *domain.FinOpsMeteringBatch) {
		decorations++
		b.Dimensions = []domain.FinOpsDimensionReport{{GPUInventory: &domain.FinOpsGPUInventory{ObservedAt: time.Now().UTC()}}}
	}
	submit := func(context.Context, domain.FinOpsMeteringBatch) error { sends++; return nil }
	for i := range 7 {
		if err := state.step(context.Background(), now.Add(time.Duration(i)*10*time.Second), collect, decorate, submit); err != nil {
			t.Fatal(err)
		}
	}
	if sends != 1 || decorations != 1 {
		t.Fatalf("cached 60s metrics redecorated/resubmitted: decorate=%d send=%d", decorations, sends)
	}
	id = "window-b"
	if err := state.step(context.Background(), now.Add(time.Minute), collect, decorate, submit); err != nil {
		t.Fatal(err)
	}
	if sends != 2 || decorations != 2 {
		t.Fatal("new metrics window not delivered")
	}
}

func TestFinOpsDeliveryRestartConflictIsTerminalUntilNewWindow(t *testing.T) {
	for _, status := range []int{400, 401, 403, 409, 422} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			state := finOpsDeliveryState{}
			now := time.Now().UTC()
			id := "persisted-before-restart"
			sends := 0
			collect := func() (domain.FinOpsMeteringBatch, error) {
				return domain.FinOpsMeteringBatch{BatchID: id, PeriodEnd: now}, nil
			}
			submit := func(context.Context, domain.FinOpsMeteringBatch) error {
				sends++
				if id == "new-window" {
					return nil
				}
				return &FinOpsDeliveryError{StatusCode: status}
			}
			if err := state.step(context.Background(), now, collect, func(*domain.FinOpsMeteringBatch) {}, submit); err == nil {
				t.Fatal("terminal failure lost")
			}
			for i := range 20 {
				if err := state.step(context.Background(), now.Add(time.Duration(i)*10*time.Second), collect, func(*domain.FinOpsMeteringBatch) { t.Fatal("cached terminal ID decorated") }, submit); err != nil {
					t.Fatal(err)
				}
			}
			if sends != 1 {
				t.Fatal("terminal cached conflict repeatedly posted")
			}
			id = "new-window"
			if err := state.step(context.Background(), now.Add(time.Minute), collect, func(*domain.FinOpsMeteringBatch) {}, submit); err != nil {
				t.Fatal(err)
			}
			if sends != 2 {
				t.Fatal("next window blocked")
			}
		})
	}
}

func TestFinOpsTransientRetriesKeepImmutablePayload(t *testing.T) {
	for _, status := range []int{0, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			state := finOpsDeliveryState{}
			now := time.Now().UTC()
			collections, decorations, sends := 0, 0, 0
			var first string
			collect := func() (domain.FinOpsMeteringBatch, error) {
				collections++
				return domain.FinOpsMeteringBatch{BatchID: "frozen", PeriodEnd: now}, nil
			}
			decorate := func(b *domain.FinOpsMeteringBatch) {
				decorations++
				b.Dimensions = []domain.FinOpsDimensionReport{{GPUInventory: &domain.FinOpsGPUInventory{ObservedAt: time.Now().UTC()}}}
			}
			submit := func(_ context.Context, b domain.FinOpsMeteringBatch) error {
				sends++
				raw, err := json.Marshal(b)
				if err != nil {
					t.Fatal(err)
				}
				if first == "" {
					first = string(raw)
				} else if first != string(raw) {
					t.Fatal("retry metadata changed")
				}
				if sends < 3 {
					return &FinOpsDeliveryError{StatusCode: status}
				}
				return nil
			}
			for i := range 3 {
				_ = state.step(context.Background(), now.Add(time.Duration(i)*10*time.Second), collect, decorate, submit)
			}
			if collections != 1 || decorations != 1 || sends != 3 || state.pending != nil {
				t.Fatalf("retry state collect=%d decorate=%d send=%d", collections, decorations, sends)
			}
		})
	}
}

func TestSubmitFinOpsReturnsTypedHTTPClassification(t *testing.T) {
	for _, status := range []int{204, 409, 422, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				if status != 204 {
					_, _ = w.Write([]byte("opaque-private-response"))
				}
			}))
			defer h.Close()
			err := SubmitFinOps(context.Background(), h.Client(), h.URL, "test-runtime", domain.FinOpsMeteringBatch{})
			if status == 204 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var typed *FinOpsDeliveryError
			if !errors.As(err, &typed) || typed.StatusCode != status {
				t.Fatalf("classification missing: %v", err)
			}
			if FinOpsDeliveryRetryable(err) != (status == 429 || status >= 500) {
				t.Fatal("wrong retry class")
			}
			if strings.Contains(err.Error(), "opaque-private-response") {
				t.Fatal("response exposed")
			}
		})
	}
}

func TestSubmitFinOpsTransportClassificationDoesNotExposeEndpoint(t *testing.T) {
	h := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := h.Client()
	endpoint := h.URL
	h.Close()
	err := SubmitFinOps(context.Background(), client, endpoint, "test-runtime", domain.FinOpsMeteringBatch{})
	var failure *FinOpsDeliveryError
	if !errors.As(err, &failure) || failure.StatusCode != 0 || !FinOpsDeliveryRetryable(err) || strings.Contains(err.Error(), endpoint) {
		t.Fatalf("unsafe transport classification: %v", err)
	}
}
