package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestResourceScannerExplicitFluxScope(t *testing.T) {
	for _, requiredDenied := range []bool{false, true} {
		t.Run(fmt.Sprint(requiredDenied), func(t *testing.T) {
			fluxRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "toolkit.fluxcd.io") {
					fluxRequests++
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if requiredDenied && strings.HasSuffix(r.URL.Path, "/deployments") {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if r.URL.Path == "/api/v1/namespaces/base" {
					_, _ = fmt.Fprint(w, `{"metadata":{"name":"base"}}`)
					return
				}
				_, _ = fmt.Fprint(w, `{"items":[]}`)
			}))
			defer server.Close()
			scanner := NewResourceDiscoveryScanner(NewKubernetesNamespaceSource(server.URL, "token", "", []string{"base"}, server.Client()))
			scanner.SetReadFlux(false)
			result, err := scanner.Scan(context.Background(), []string{"base"})
			if err != nil {
				t.Fatal(err)
			}
			if fluxRequests != 0 || result.Completeness.Complete == requiredDenied || len(result.Completeness.Namespaces[0].Excluded) != 3 {
				t.Fatalf("unexpected scope coverage: requests=%d completeness=%+v", fluxRequests, result.Completeness)
			}
		})
	}
}

func TestConfigReadFluxExplicitOptOut(t *testing.T) {
	t.Setenv("ENVPLANE_DISCOVERY_READ_FLUX", "")
	if !ConfigFromEnv().ReadFlux {
		t.Fatal("legacy default must retain Flux discovery")
	}
	t.Setenv("ENVPLANE_DISCOVERY_READ_FLUX", "false")
	if ConfigFromEnv().ReadFlux {
		t.Fatal("explicit Flux opt-out ignored")
	}
}

// Opt-in local live check; the proxy must impersonate the project Agent.
func TestResourceScannerLiveExplicitFluxScope(t *testing.T) {
	endpoint := os.Getenv("ENVPLANE_TEST_SCAN_PROXY_URL")
	if endpoint == "" {
		t.Skip("local impersonating Kubernetes proxy not configured")
	}
	namespaces := strings.Split(os.Getenv("ENVPLANE_TEST_SCAN_NAMESPACES"), ",")
	if len(namespaces) == 0 || namespaces[0] == "" {
		t.Fatal("explicit live scan namespace allowlist is required")
	}
	scanner := NewResourceDiscoveryScanner(NewKubernetesNamespaceSource(endpoint, "", "", namespaces, http.DefaultClient))
	scanner.SetReadFlux(false)
	result, err := scanner.Scan(context.Background(), namespaces)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completeness.Complete || len(result.Snapshots) == 0 {
		t.Fatalf("live scope incomplete: %+v", result.Completeness)
	}
	t.Logf("live inventory: %d resources; completeness=%+v", len(result.Snapshots), result.Completeness)
}
