package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/envplane/contracts/domain"
)

func TestBaselineCapacityExactPinWithoutEnvironment(t *testing.T) {
	now := time.Now().UTC()
	b := domain.BaseResourceBinding{BaseResourcePin: domain.BaseResourcePin{Namespace: "base", ResourceKind: "PersistentVolumeClaim", ResourceName: "data", ResourceUID: "11111111-1111-4111-8111-111111111111", ComponentID: "database"}, ID: "binding", ProjectID: "p", ClusterID: "c", ClusterGeneration: 1, Version: 1, State: "active", CreatedAt: now.Add(-time.Hour)}
	for _, wrongUID := range []bool{false, true} {
		t.Run(map[bool]string{false: "capacity", true: "replacement rejected"}[wrongUID], func(t *testing.T) {
			h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/namespaces/base":
					_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "base"}})
				case "/api/v1/namespaces/base/persistentvolumeclaims/data":
					uid := b.ResourceUID
					if wrongUID {
						uid = "22222222-2222-4222-8222-222222222222"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "data", "uid": uid, "labels": map[string]string{"app.kubernetes.io/component": "database"}}, "spec": map[string]any{"volumeName": "pv", "resources": map[string]any{"requests": map[string]string{"storage": "2Gi"}}}, "status": map[string]any{"phase": "Bound", "capacity": map[string]string{"storage": "3Gi"}}})
				default:
					t.Error("unexpected broad resource request")
					w.WriteHeader(404)
				}
			}))
			defer h.Close()
			s := &KubernetesNamespaceSource{apiURL: h.URL, client: h.Client()}
			samples, err := s.CollectBaselinePVCCapacity(context.Background(), []domain.BaseResourceBinding{b}, "p", "c", 1, now.Add(-time.Minute), now)
			if wrongUID {
				if err == nil {
					t.Fatal("replacement accepted")
				}
				return
			}
			if err != nil || len(samples) != 2 {
				t.Fatal("capacity unavailable", err)
			}
			for i, sample := range samples {
				if sample.Attribution.EnvironmentID != "" || sample.Attribution.BaseResourceBindingID != b.ID || sample.MeasurementKind != domain.FinOpsCapacity || sample.Quantity != float64(i+2)/60 || sample.UsedBytes != nil {
					t.Fatal("fabricated attribution or measured usage")
				}
			}
		})
	}
}

func TestBaselineBindingTransportFailsClosed(t *testing.T) {
	for _, body := range []string{`{"bindings":null}`, `{"bindings":[]} {}`, `{"bindings":[],"tenantId":"invented"}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/agents/finops/base-resource-bindings" || r.Header.Get("Authorization") != "Bearer runtime-test" {
					t.Error("wrong authenticated route")
				}
				_, _ = w.Write([]byte(body))
			}))
			defer s.Close()
			if _, err := FetchBaselineBindings(context.Background(), s.Client(), s.URL, "runtime-test", "project", "cluster", "agent", 1); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestBaselineBindingTransportEmptyIsExplicit(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"bindings":[]}`)) }))
	defer s.Close()
	bindings, err := FetchBaselineBindings(context.Background(), s.Client(), s.URL, "runtime-test", "project", "cluster", "agent", 1)
	if err != nil || bindings == nil || len(bindings) != 0 {
		t.Fatal("explicit empty source lost")
	}
}

func TestBaselineRuntimeFrozenRetriesAndTerminalGap(t *testing.T) {
	ctx := context.Background()
	start := time.Now().UTC()
	s := baselineDeliveryState{start: start}
	collections := 0
	collect := func(a, b time.Time) (domain.BaselineMeteringBatch, error) {
		collections++
		return domain.BaselineMeteringBatch{BatchID: "immutable", PeriodStart: a, PeriodEnd: b, Samples: []domain.BaselineMeteringSample{{Attribution: domain.FinOpsResourceAttribution{BaseResourceBindingID: "pin", BindingVersion: 1}}}}, nil
	}
	var first domain.BaselineMeteringBatch
	if err := s.step(ctx, start.Add(time.Minute), collect, func(_ context.Context, b domain.BaselineMeteringBatch) error {
		first = b
		return &FinOpsDeliveryError{StatusCode: 503}
	}); err == nil {
		t.Fatal("delivery failure lost")
	}
	if err := s.step(ctx, start.Add(2*time.Minute), collect, func(_ context.Context, b domain.BaselineMeteringBatch) error {
		if !reflect.DeepEqual(first, b) {
			t.Fatal("retry changed evidence")
		}
		return &FinOpsDeliveryError{StatusCode: 422}
	}); err == nil {
		t.Fatal("terminal failure lost")
	}
	if collections != 1 || s.pending != nil || !s.start.Equal(start.Add(2*time.Minute)) {
		t.Fatal("terminal batch retried or interval backfilled")
	}
}

func TestBaselineRuntimeEmptyDoesNotSubmit(t *testing.T) {
	start := time.Now().UTC()
	s := baselineDeliveryState{start: start}
	err := s.step(context.Background(), start.Add(time.Minute), func(time.Time, time.Time) (domain.BaselineMeteringBatch, error) {
		return domain.BaselineMeteringBatch{Samples: []domain.BaselineMeteringSample{}}, nil
	}, func(context.Context, domain.BaselineMeteringBatch) error {
		t.Fatal("empty source submitted as complete")
		return nil
	})
	if err != nil || s.pending != nil {
		t.Fatal("empty source failed")
	}
}
