package agent

import (
	"context"
	"encoding/json"
	"github.com/envplane/contracts/domain"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBaselineMeasuredPinnedPVCAndPod(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Minute)
	for _, kind := range []string{"PersistentVolumeClaim", "Pod"} {
		t.Run(kind, func(t *testing.T) {
			pin := domain.BaseResourceBinding{BaseResourcePin: domain.BaseResourcePin{Namespace: "base", ResourceKind: kind, ResourceName: "data", ResourceUID: "11111111-1111-4111-8111-111111111111", ComponentID: "db"}, ID: "baseline", Version: 1, State: "active", ProjectID: "p", ClusterID: "c", ClusterGeneration: 1, CreatedAt: start.Add(-time.Hour)}
			var wrongMetricUID atomic.Bool
			kube := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/namespaces/base" {
					_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "base"}})
					return
				}
				if r.URL.Path == "/apis/metrics.k8s.io/v1beta1/namespaces/base/pods/data" {
					uid := pin.ResourceUID
					if wrongMetricUID.Load() {
						uid = "22222222-2222-4222-8222-222222222222"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "data", "namespace": "base", "uid": uid}, "timestamp": now, "window": "1m", "containers": []any{map[string]any{"name": "db", "usage": map[string]string{"cpu": "1", "memory": "1Gi"}}}})
					return
				}
				meta := map[string]any{"name": "data", "uid": pin.ResourceUID, "creationTimestamp": start.Add(-time.Hour), "labels": map[string]string{"app.kubernetes.io/component": "db"}}
				if r.URL.Path == "/api/v1/namespaces/base/persistentvolumeclaims/data" {
					_ = json.NewEncoder(w).Encode(map[string]any{"metadata": meta, "status": map[string]any{"phase": "Bound", "capacity": map[string]string{"storage": "1Gi"}}})
					return
				}
				if r.URL.Path == "/api/v1/namespaces/base/pods/data" {
					_ = json.NewEncoder(w).Encode(map[string]any{"metadata": meta, "spec": map[string]any{"containers": []any{map[string]any{"name": "db"}}}})
					return
				}
				t.Error("unexpected source request")
				w.WriteHeader(404)
			}))
			defer kube.Close()
			source := &KubernetesNamespaceSource{apiURL: kube.URL, client: kube.Client()}
			if kind == "Pod" {
				samples, err := source.CollectBaselinePodUsage(context.Background(), pin, "p", "c", 1, start, now)
				if err != nil || len(samples) != 2 {
					t.Fatal("CPU/RAM missing", err)
				}
				for _, s := range samples {
					if s.Quantity != 1.0/60 || s.Attribution.EnvironmentID != "" || s.Attribution.BaseResourceBindingID != "baseline" {
						t.Fatal("wrong measured attribution")
					}
				}
				if _, err := source.CollectBaselinePodUsage(context.Background(), pin, "p", "c", 1, start.Add(-time.Second), now); err == nil {
					t.Fatal("metrics gap invented")
				}
				wrongMetricUID.Store(true)
				if _, err := source.CollectBaselinePodUsage(context.Background(), pin, "p", "c", 1, start, now); err == nil {
					t.Fatal("foreign metric UID accepted")
				}
				return
			}
			for _, missing := range []bool{false, true} {
				promServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					result := []any{}
					if !missing {
						result = append(result, map[string]any{"metric": map[string]string{"__name__": "kubelet_volume_stats_used_bytes", "namespace": "base", "persistentvolumeclaim": "data", "pvc_uid": pin.ResourceUID}, "values": []any{[]any{start.Unix(), "4096"}, []any{now.Unix(), "4096"}}})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": result}})
				}))
				prom, err := NewFinOpsPrometheusSource(promServer.URL, []string{promServer.URL}, promServer.Client())
				if err != nil {
					t.Fatal(err)
				}
				samples, err := source.CollectBaselineMeasured(context.Background(), prom, pin, "p", "c", 1, start, now)
				promServer.Close()
				if missing {
					if err == nil || len(samples) != 0 {
						t.Fatal("missing gauge became zero")
					}
					continue
				}
				if err != nil || len(samples) != 1 || samples[0].UsedBytes == nil || *samples[0].UsedBytes != 4096 || samples[0].Attribution.EnvironmentID != "" {
					t.Fatal("used storage missing", err)
				}
			}
		})
	}
}

func TestBaselineNetworkExplicitUIDAndResetGap(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Minute)
	uid := "11111111-1111-4111-8111-111111111111"
	for _, reset := range []string{"0", "1"} {
		t.Run(reset, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				labels := map[string]string{"__name__": "container_network_transmit_bytes_total", "namespace": "base", "pod": "db", "pod_uid": uid, "interface": "eth0"}
				if r.URL.Path == "/api/v1/query" {
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": labels, "value": []any{now.Unix(), reset}}}}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": labels, "values": []any{[]any{start.Unix(), "0"}, []any{now.Unix(), "1073741824"}}}}}})
			}))
			defer server.Close()
			prom, err := NewFinOpsPrometheusSource(server.URL, []string{server.URL}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			owned := []FinOpsOwnedResource{{Namespace: "base", PodName: "db", ResourceUID: uid, ComponentID: "db", baselineBindingID: "pin", baselineVersion: 1}}
			report, err := prom.collect(context.Background(), domain.FinOpsNetworkTransmit, owned, start, now, true)
			if reset != "0" {
				if err == nil {
					t.Fatal("reset became consumption")
				}
				return
			}
			if err != nil || report.State != "complete" || len(report.Samples) != 1 || report.Samples[0].EnvironmentID != "" || report.Samples[0].Quantity != 1 {
				t.Fatal("exact baseline network missing", err)
			}
			feature, err := prom.Collect(context.Background(), domain.FinOpsNetworkTransmit, owned, start, now)
			if err != nil || len(feature.Samples) != 0 || feature.State == "complete" {
				t.Fatal("baseline pin entered Environment collector")
			}
		})
	}
}
