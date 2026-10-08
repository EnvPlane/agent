package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/envplane/contracts/domain"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFinOpsMeasuredMissingAndUnattributed(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, test := range []struct {
		name          string
		metricsStatus int
		component     bool
		wantMeasured  int
		wantAvailable bool
	}{{"measured", 200, true, 1, true}, {"metrics unavailable", 403, true, 0, false}, {"unattributed", 200, false, 0, true}} {
		t.Run(test.name, func(t *testing.T) {
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/namespaces":
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "feature", "labels": map[string]string{"envplane.io/project-id": "p", environmentIDLabel: "e"}}}}})
				case "/api/v1/namespaces/feature/pods":
					labels := map[string]string{}
					if test.component {
						labels["envplane.io/component"] = "backend"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "backend", "uid": "pod-uid", "creationTimestamp": now.Add(-time.Hour), "labels": labels}, "spec": map[string]any{"containers": []any{map[string]string{"name": "backend"}}}}}})
				case "/apis/metrics.k8s.io/v1beta1/namespaces/feature/pods":
					if test.metricsStatus != 200 {
						w.WriteHeader(test.metricsStatus)
						return
					}
					_, _ = fmt.Fprintf(w, `{"items":[{"metadata":{"name":"backend"},"timestamp":%q,"window":"30s","containers":[{"name":"backend","usage":{"cpu":"100m","memory":"128Mi"}}]}]}`, now.Format(time.RFC3339))
				default:
					w.WriteHeader(404)
				}
			}))
			defer h.Close()
			s := &KubernetesNamespaceSource{apiURL: h.URL, client: h.Client()}
			b, err := s.CollectFinOps(context.Background(), "p", "c", "a", now)
			if err != nil {
				t.Fatal(err)
			}
			if b.MeasuredPods != test.wantMeasured || b.MetricsAvailable != test.wantAvailable || b.ExpectedPods != 1 {
				t.Fatalf("batch=%+v", b)
			}
			if b.Samples == nil {
				t.Fatal("metering samples must serialize as an array even when unavailable")
			}
			if len(b.Samples) > 0 {
				if b.Samples[0].TenantID != "" || b.Samples[0].CPUCoreHours <= 0 || b.Samples[0].PeriodEnd.Sub(b.Samples[0].PeriodStart) != 30*time.Second {
					t.Fatal("invented scope/window")
				}
			}
		})
	}
}

func TestFinOpsTransportRejectsPlainHTTP(t *testing.T) {
	if SubmitFinOps(context.Background(), http.DefaultClient, "http://localhost", "test", domain.FinOpsMeteringBatch{}) == nil {
		t.Fatal("insecure transport")
	}
}
