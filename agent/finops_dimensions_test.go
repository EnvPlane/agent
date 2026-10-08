package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/envplane/contracts/domain"
)

func TestStorageReportsCapacityNotUsedBytes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "feature", "labels": map[string]string{"envplane.io/project-id": "p", environmentIDLabel: "e"}}}}})
		case "/api/v1/namespaces/feature/persistentvolumeclaims":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "data", "uid": "pvc-uid", "creationTimestamp": now.Add(-time.Hour), "labels": map[string]string{"envplane.io/component": "db"}}, "spec": map[string]any{"volumeName": "owned-pv", "resources": map[string]any{"requests": map[string]string{"storage": "2Gi"}}}, "status": map[string]any{"phase": "Bound", "capacity": map[string]string{"storage": "3Gi"}}}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer h.Close()
	s := &KubernetesNamespaceSource{apiURL: h.URL, client: h.Client()}
	reports, err := s.CollectFinOpsStorage(context.Background(), "p", now.Add(-time.Minute), now)
	if err != nil || len(reports) != 2 {
		t.Fatalf("reports=%+v err=%v", reports, err)
	}
	for i, r := range reports {
		if r.State != "complete" || r.MeasurementKind != domain.FinOpsCapacity || r.Unit != domain.FinOpsGiBHours || r.Samples[0].Quantity != float64(i+2)/60 {
			t.Fatalf("capacity=%+v", r)
		}
	}
}

func TestPrometheusDimensionsAllowedSourceResetGapAndAbsentGPU(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Minute)
	for _, test := range []struct {
		name         string
		dimension    domain.FinOpsDimension
		reset        string
		gap          bool
		foreign      bool
		wantComplete bool
	}{{"network", domain.FinOpsNetworkTransmit, "0", false, false, true}, {"counter reset", domain.FinOpsNetworkTransmit, "1", false, false, false}, {"gap", domain.FinOpsNetworkTransmit, "0", true, false, false}, {"foreign UID", domain.FinOpsNetworkTransmit, "0", false, true, false}, {"GPU sampled", domain.FinOpsGPUUtilization, "0", false, false, true}} {
		t.Run(test.name, func(t *testing.T) {
			h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				uid := "pod-uid"
				if test.foreign {
					uid = "other-uid"
				}
				labels := map[string]string{"__name__": prometheusMetric(test.dimension), "namespace": "feature", "pod": "backend", "pod_uid": uid, "interface": "eth0", "UUID": "gpu-device"}
				if r.URL.Path == "/api/v1/query" {
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": labels, "value": []any{now.Unix(), test.reset}}}}})
					return
				}
				first, last := "0", "1073741824"
				if test.dimension == domain.FinOpsGPUUtilization {
					first = "50"
					last = "50"
				}
				timestamp := start.Unix()
				if test.gap {
					timestamp++
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": labels, "values": []any{[]any{timestamp, first}, []any{now.Unix(), last}}}}}})
			}))
			defer h.Close()
			if _, err := NewFinOpsPrometheusSource(h.URL, nil, h.Client()); err == nil {
				t.Fatal("unapproved source accepted")
			}
			s, err := NewFinOpsPrometheusSource(h.URL, []string{h.URL}, h.Client())
			if err != nil {
				t.Fatal(err)
			}
			owned := []FinOpsOwnedResource{{Namespace: "feature", PodName: "backend", ResourceUID: "pod-uid", EnvironmentID: "e", ComponentID: "backend", ExpectedGPUs: 1}}
			report, err := s.Collect(context.Background(), test.dimension, owned, start, now)
			if (err == nil && report.State == "complete") != test.wantComplete {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			if test.wantComplete {
				if report.Samples[0].TenantID != "" || report.Samples[0].Quantity <= 0 {
					t.Fatal("fabricated identity or quantity")
				}
			}
			owned[0].ExpectedGPUs = 0
			report, err = s.Collect(context.Background(), domain.FinOpsGPUUtilization, owned, start, now)
			if err != nil || report.State != "unavailable" || len(report.Samples) != 0 {
				t.Fatal("absent GPU invented usage")
			}
		})
	}
}
