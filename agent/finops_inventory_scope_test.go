package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFinOpsInventoryEmptyBootstrapAndExplicitExporter(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct{ invalidOwned, spoofedMarker bool }{{false, false}, {true, false}, {false, true}} {
		invalidOwned := tc.invalidOwned
		pod := map[string]any{"metadata": map[string]any{"name": "workload", "uid": "65f0a182-b4b7-4562-930d-4a3aa9bf4aef", "creationTimestamp": now.Add(-time.Hour), "labels": map[string]string{"app.kubernetes.io/component": "backend"}}, "spec": map[string]any{"containers": []any{map[string]string{"name": "backend"}}}}
		if invalidOwned {
			pod["metadata"].(map[string]any)["labels"] = map[string]string{}
		}
		if tc.spoofedMarker {
			pod["metadata"].(map[string]any)["labels"].(map[string]string)["envplane.io/pvc-exporter"] = "spoofed"
		}
		pvc := map[string]any{"metadata": map[string]any{"name": "data", "uid": "bcf325e3-299c-4e01-9b43-1e4123fb5bdc", "creationTimestamp": now.Add(-time.Hour), "labels": map[string]string{"app.kubernetes.io/component": "backend"}}, "spec": map[string]any{"volumeName": "pv"}, "status": map[string]any{"phase": "Bound", "capacity": map[string]string{"storage": "1Gi"}}}
		h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			items := []any{}
			switch r.URL.Path {
			case "/api/v1/namespaces":
				items = []any{
					map[string]any{"metadata": map[string]any{"name": "bootstrap", "labels": map[string]string{"envplane.io/project-id": "p"}}},
					map[string]any{"metadata": map[string]any{"name": "baseline", "labels": map[string]string{}}},
					map[string]any{"metadata": map[string]any{"name": "feature", "labels": map[string]string{"envplane.io/project-id": "p", environmentIDLabel: "e"}}},
				}
			case "/api/v1/namespaces/feature/pods":
				items = []any{pod, map[string]any{"metadata": map[string]any{"name": "exporter", "uid": "1021a1ee-0080-413c-bafa-b4c5ab7ebb7e", "labels": map[string]string{"envplane.io/pvc-exporter": "private"}}}}
			case "/api/v1/namespaces/feature/persistentvolumeclaims":
				items = []any{pvc}
			case "/apis/metrics.k8s.io/v1beta1/namespaces/feature/pods":
				items = []any{map[string]any{"metadata": map[string]any{"name": "workload"}, "timestamp": now, "window": "30s", "containers": []any{map[string]any{"name": "backend", "usage": map[string]string{"cpu": "100m", "memory": "128Mi"}}}}}
			default:
				if strings.Contains(r.URL.Path, "baseline") {
					t.Error("unregistered baseline was scanned as an Environment")
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		}))
		s := &KubernetesNamespaceSource{apiURL: h.URL, client: h.Client()}
		pods, err := s.FinOpsOwnedPodInventory(context.Background(), "p")
		if invalidOwned && err == nil {
			t.Fatal("invalid owned Pod silently skipped")
		}
		wantOwned := 1
		if tc.spoofedMarker {
			wantOwned = 0
		}
		if !invalidOwned && (err != nil || len(pods) != wantOwned) {
			t.Fatalf("empty bootstrap/exporter poisoned inventory: %+v %v", pods, err)
		}
		pvcs, err := s.FinOpsOwnedPVCInventory(context.Background(), "p", now.Add(-time.Minute))
		if err != nil || len(pvcs) != 1 {
			t.Fatalf("empty bootstrap poisoned PVC inventory: %+v %v", pvcs, err)
		}
		batch, err := s.CollectFinOps(context.Background(), "p", "c", "a", now)
		wantUnallocated := 1
		if invalidOwned || tc.spoofedMarker {
			wantUnallocated = 2
		}
		if err != nil || batch.ExpectedPods != 2 || batch.UnattributedPods != wantUnallocated {
			t.Fatalf("workload coverage wrong: %+v %v", batch, err)
		}
		if tc.spoofedMarker && batch.MeasuredPods != 0 {
			t.Fatal("spoofed marker produced assigned consumption/full coverage")
		}
		h.Close()
	}
}

func TestFinOpsInventoryNonemptyProjectWithoutOwnerFails(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := []any{map[string]any{"metadata": map[string]any{"name": "resource", "uid": "current-uid"}}}
		if r.URL.Path == "/api/v1/namespaces" {
			items = []any{map[string]any{"metadata": map[string]any{"name": "unknown", "labels": map[string]string{"envplane.io/project-id": "p"}}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	defer h.Close()
	s := &KubernetesNamespaceSource{apiURL: h.URL, client: h.Client()}
	if _, err := s.FinOpsOwnedPodInventory(context.Background(), "p"); err == nil {
		t.Fatal("nonempty namespace without owner silently skipped")
	}
	if _, err := s.FinOpsOwnedPVCInventory(context.Background(), "p", time.Now()); err == nil {
		t.Fatal("unbound PVC silently skipped")
	}
}
