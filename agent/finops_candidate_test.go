package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/envplane/contracts/domain"
)

func TestContainerdAttributionRequiresExactOwnedUIDAndRejectsRoot(t *testing.T) {
	uid := "aabbccdd-1122-3344-5566-778899aabbcc"
	owner := FinOpsOwnedResource{Namespace: "feature", PodName: "backend", ResourceUID: uid, EnvironmentID: "e", ComponentID: "backend"}
	source := &FinOpsPrometheusSource{containerdUID: true}
	owners := map[string]FinOpsOwnedResource{uid: owner}
	path := "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" + strings.ReplaceAll(uid, "-", "_") + ".slice/cri-containerd-" + strings.Repeat("a", 64) + ".scope"
	labels, err := source.normalizeNetworkIdentity(map[string]string{"id": path}, owners)
	if err != nil || labels["pod_uid"] != uid || labels["namespace"] != "feature" {
		t.Fatalf("containerd identity=%v err=%v", labels, err)
	}
	for _, candidate := range []map[string]string{{"id": "/"}, {"id": "/kubepods.slice"}, {"id": strings.ReplaceAll(path, "aabbccdd", "bbbbbbbb")}, {"id": path, "namespace": "other"}, {"id": path + "/untrusted-child"}} {
		if _, err := source.normalizeNetworkIdentity(candidate, owners); err == nil {
			t.Fatal("unattributable network accepted", candidate)
		}
	}
	source.containerdUID = false
	if _, err := source.normalizeNetworkIdentity(map[string]string{"id": path}, owners); err == nil {
		t.Fatal("cgroup discovery not opt-in")
	}
}

func TestCandidateTLSNetworkCgroupAndStorageUsedGauge(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Minute)
	uid := "aabbccdd-1122-3344-5566-778899aabbcc"
	path := "/kubepods.slice/kubepods-burstable-pod" + strings.ReplaceAll(uid, "-", "_") + ".slice"
	for _, dimension := range []domain.FinOpsDimension{domain.FinOpsNetworkTransmit, FinOpsStorageUsed} {
		t.Run(string(dimension), func(t *testing.T) {
			h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				labels := map[string]string{"__name__": prometheusMetric(dimension), "id": path, "interface": "eth0"}
				if dimension == FinOpsStorageUsed {
					labels = map[string]string{"__name__": "kubelet_volume_stats_used_bytes", "namespace": "feature", "persistentvolumeclaim": "data", "pvc_uid": uid}
				}
				if r.URL.Path == "/api/v1/query" {
					if strings.Contains(r.URL.Query().Get("query"), " or ") && strings.Contains(r.URL.Query().Get("query"), ")[60s]") {
						t.Fatal("invalid PromQL range on binary expression")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": labels, "value": []any{now.Unix(), "0"}}}}})
					return
				}
				first, last := "0", "1073741824"
				if dimension == FinOpsStorageUsed {
					first = "1073741824"
					last = "1073741824"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": labels, "values": []any{[]any{start.Unix(), first}, []any{now.Unix(), last}}}}}})
			}))
			defer h.Close()
			source, err := NewFinOpsPrometheusSource(h.URL, []string{h.URL}, h.Client())
			if err != nil {
				t.Fatal(err)
			}
			source.containerdUID = true
			owned := []FinOpsOwnedResource{{Namespace: "feature", PodName: "backend", PVCName: "data", ResourceUID: uid, EnvironmentID: "e", ComponentID: "backend", ProvisionedBytes: 2 << 30}}
			report, err := source.Collect(context.Background(), dimension, owned, start, now)
			if err != nil || report.State != "complete" || report.MeasurementKind != domain.FinOpsMeasured || len(report.Samples) != 1 {
				t.Fatalf("candidate report=%+v err=%v", report, err)
			}
			want := 1.0
			if dimension == FinOpsStorageUsed {
				want = 1.0 / 60
			}
			if report.Samples[0].Quantity != want {
				t.Fatalf("wrong unit quantity=%v want=%v", report.Samples[0].Quantity, want)
			}
		})
	}
}

func TestStorageUsedDoesNotInferGenerationFromClaimName(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{"__name__": "kubelet_volume_stats_used_bytes", "namespace": "feature", "persistentvolumeclaim": "data"}, "values": []any{[]any{now.Add(-time.Minute).Unix(), "1024"}, []any{now.Unix(), "1024"}}}}}})
	}))
	defer h.Close()
	source, err := NewFinOpsPrometheusSource(h.URL, []string{h.URL}, h.Client())
	if err != nil {
		t.Fatal(err)
	}
	report, err := source.Collect(context.Background(), FinOpsStorageUsed, []FinOpsOwnedResource{{Namespace: "feature", PVCName: "data", ResourceUID: "current-pvc", EnvironmentID: "e", ComponentID: "db"}}, now.Add(-time.Minute), now)
	if err == nil || report.State == "complete" || len(report.Samples) != 0 {
		t.Fatal("namelike volume metric guessed into owned PVC")
	}
}
