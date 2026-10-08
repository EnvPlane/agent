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

func TestNodeInventoryOptInPaginationAndUnknownCapacity(t *testing.T) {
	for _, test := range []struct {
		name        string
		capacity    string
		status      int
		wantDevices int
		wantErr     bool
	}{{"no GPU", "absent", 200, 0, false}, {"nvidia and AMD", "2", 200, 3, false}, {"unknown quantity", "invalid", 200, 0, true}, {"fractional quantity", "0.5", 200, 0, true}, {"denied", "absent", 403, 0, true}} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v1/nodes" {
					t.Error("unexpected API path")
					w.WriteHeader(404)
					return
				}
				if test.status != 200 {
					w.WriteHeader(test.status)
					return
				}
				capacity := map[string]string{"cpu": "4", "memory": "8Gi"}
				if test.capacity != "absent" {
					capacity["nvidia.com/gpu"] = test.capacity
				}
				uid, continuation := "first", "next"
				if r.URL.Query().Get("continue") == "next" {
					uid = "second"
					continuation = ""
					capacity = map[string]string{"cpu": "4", "memory": "8Gi"}
					if test.capacity != "absent" {
						capacity["amd.com/gpu"] = "1"
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]string{"continue": continuation}, "items": []any{map[string]any{"metadata": map[string]string{"uid": uid}, "status": map[string]any{"capacity": capacity}}}})
			}))
			defer h.Close()
			source := &KubernetesNamespaceSource{apiURL: h.URL, client: h.Client()}
			configured, err := NewConfiguredFinOpsDimensionSource(Config{ClusterID: "c"}, source)
			if err != nil {
				t.Fatal(err)
			}
			report, err := configured.Collect(context.Background(), domain.FinOpsGPUUtilization, nil, time.Now().Add(-time.Minute), time.Now())
			if err != nil || report.State != "unavailable" || calls != 0 {
				t.Fatal("default node inventory performed reads")
			}
			inventory, err := source.FinOpsGPUNodeInventory(context.Background(), "c")
			if (err != nil) != test.wantErr {
				t.Fatalf("inventory=%+v err=%v", inventory, err)
			}
			if !test.wantErr && (inventory.Devices != test.wantDevices || inventory.Nodes != 2 || calls != 2) {
				t.Fatalf("paginated inventory=%+v calls=%d", inventory, calls)
			}
			if test.wantErr && (inventory.Nodes != 0 || inventory.ClusterID != "") {
				t.Fatal("partial inventory published")
			}
			if !test.wantErr && test.wantDevices == 0 {
				configured, err = NewConfiguredFinOpsDimensionSource(Config{ClusterID: "c", FinOpsNodeInventoryEnabled: true}, source)
				if err != nil {
					t.Fatal(err)
				}
				report, err = configured.Collect(context.Background(), domain.FinOpsGPUUtilization, nil, time.Now().Add(-time.Minute), time.Now())
				if err != nil || report.State != "not_applicable" || report.Reason != "no_devices" || report.GPUInventory == nil || report.GPUInventory.Nodes != 2 {
					t.Fatal("enabled paginated inventory not wired")
				}
			}
		})
	}
}

func TestConfiguredFinOpsSourcesRequireHTTPSExactOrigin(t *testing.T) {
	for _, cfg := range []Config{{FinOpsPrometheusEndpoint: "http://metrics.example"}, {FinOpsPrometheusEndpoint: "https://metrics.example"}, {FinOpsPrometheusEndpoint: "https://metrics.example?query=arbitrary", FinOpsPrometheusAllowedOrigins: []string{"https://metrics.example"}}} {
		if _, err := NewConfiguredFinOpsDimensionSource(cfg, nil); err == nil {
			t.Fatal("unsafe metrics source configured")
		}
	}
	if _, err := NewConfiguredFinOpsDimensionSource(Config{FinOpsPrometheusEndpoint: "https://metrics.example", FinOpsPrometheusAllowedOrigins: []string{"https://metrics.example"}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestFinOpsSourceConfigFromEnvDefaultsAndOptIn(t *testing.T) {
	t.Setenv("ENVPLANE_FINOPS_NODE_INVENTORY_ENABLED", "")
	t.Setenv("ENVPLANE_FINOPS_PROMETHEUS_ENDPOINT", "")
	if ConfigFromEnv().FinOpsNodeInventoryEnabled {
		t.Fatal("inventory default enabled")
	}
	t.Setenv("ENVPLANE_FINOPS_NODE_INVENTORY_ENABLED", "true")
	t.Setenv("ENVPLANE_FINOPS_PROMETHEUS_ENDPOINT", "https://metrics.example")
	t.Setenv("ENVPLANE_FINOPS_PROMETHEUS_ALLOWED_ORIGINS", "https://metrics.example,https://second.example")
	cfg := ConfigFromEnv()
	if !cfg.FinOpsNodeInventoryEnabled || len(cfg.FinOpsPrometheusAllowedOrigins) != 2 {
		t.Fatal("FinOps opt-in configuration not read")
	}
}
