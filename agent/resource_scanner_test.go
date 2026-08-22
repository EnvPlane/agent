package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/envplane/contracts/domain"
)

func TestResourceDiscoveryScannerFluxSourceMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apis/apps/v1/namespaces/dev-base/deployments":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []any{
					map[string]any{
						"metadata": map[string]any{
							"name":      "orders",
							"namespace": "dev-base",
							"labels": map[string]string{
								"helm.toolkit.fluxcd.io/name":      "orders-release",
								"helm.toolkit.fluxcd.io/namespace": "dev-base",
							},
						},
					},
					map[string]any{
						"metadata": map[string]any{
							"name":      "unmapped",
							"namespace": "dev-base",
						},
					},
				},
			})
			return
		case "/apis/helm.toolkit.fluxcd.io/v2/namespaces/dev-base/helmreleases":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []any{
					map[string]any{
						"metadata": map[string]any{
							"name":      "orders-release",
							"namespace": "dev-base",
						},
						"spec": map[string]any{
							"chart": map[string]any{
								"spec": map[string]any{
									"sourceRef": map[string]any{
										"kind":      "GitRepository",
										"name":      "app-config",
										"namespace": "flux-system",
									},
								},
							},
						},
					},
				},
			})
			return
		case "/apis/kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []any{
					map[string]any{
						"metadata": map[string]any{
							"name":      "platform-kustomization",
							"namespace": "flux-system",
						},
						"spec": map[string]any{
							"sourceRef": map[string]any{
								"kind":      "GitRepository",
								"name":      "missing-repo",
								"namespace": "flux-system",
							},
						},
					},
				},
			})
			return
		case "/apis/source.toolkit.fluxcd.io/v1/namespaces/flux-system/gitrepositories":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []any{
					map[string]any{
						"metadata": map[string]any{
							"name":      "app-config",
							"namespace": "flux-system",
						},
					},
				},
			})
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			return
		}
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "token", "", []string{"dev-base"}, server.Client())
	scanner := NewResourceDiscoveryScanner(source)
	result, err := scanner.Scan(context.Background(), []string{"dev-base"})
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	byKindName := make(map[string]domain.ResourceSnapshot)
	for _, snapshot := range result.Snapshots {
		byKindName[snapshot.Kind+"/"+snapshot.Namespace+"/"+snapshot.Name] = snapshot
	}

	helmKey := "HelmRelease/dev-base/orders-release"
	if _, ok := byKindName[helmKey]; !ok {
		t.Fatalf("helm release snapshot missing")
	}
	kustomizationKey := "Kustomization/flux-system/platform-kustomization"
	kustomizationSnapshot, ok := byKindName[kustomizationKey]
	if !ok {
		t.Fatalf("kustomization snapshot missing")
	}
	if kustomizationSnapshot.SourceMapping == nil || kustomizationSnapshot.SourceMapping.Status != "unresolved" {
		t.Fatalf("kustomization source mapping = %#v", kustomizationSnapshot.SourceMapping)
	}

	deploymentKey := "Deployment/dev-base/orders"
	deploymentSnapshot, ok := byKindName[deploymentKey]
	if !ok {
		t.Fatalf("mapped deployment snapshot missing")
	}
	if deploymentSnapshot.SourceMapping == nil || deploymentSnapshot.SourceMapping.Status != "resolved" {
		t.Fatalf("deployment source mapping = %#v", deploymentSnapshot.SourceMapping)
	}
	if deploymentSnapshot.SourceMapping.Kind != "HelmRelease" || deploymentSnapshot.SourceMapping.Name != "orders-release" {
		t.Fatalf("deployment source mapping details = %#v", deploymentSnapshot.SourceMapping)
	}
	if deploymentSnapshot.SourceMapping.GitRepositoryName != "app-config" {
		t.Fatalf("deployment git source mapping = %#v", deploymentSnapshot.SourceMapping)
	}

	unresolvedKey := "Deployment/dev-base/unmapped"
	unresolvedSnapshot, ok := byKindName[unresolvedKey]
	if !ok {
		t.Fatalf("unmapped deployment snapshot missing")
	}
	if unresolvedSnapshot.SourceMapping == nil || unresolvedSnapshot.SourceMapping.Status != "unresolved" {
		t.Fatalf("unmapped deployment source mapping = %#v", unresolvedSnapshot.SourceMapping)
	}
}

func TestResourceDiscoveryScannerDoesNotCallSecretsAPIWithoutExplicitOptIn(t *testing.T) {
	paths := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "token", "", []string{"template"}, server.Client())
	if _, err := NewResourceDiscoveryScanner(source).Scan(context.Background(), []string{"template"}); err != nil {
		t.Fatalf("default scan: %v", err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "/secrets") {
			t.Fatalf("default scanner must not read Secret resources: %s", path)
		}
	}

	paths = nil
	if _, err := NewResourceDiscoveryScanner(source, true).Scan(context.Background(), []string{"template"}); err != nil {
		t.Fatalf("secret-enabled scan: %v", err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "/secrets") {
			return
		}
	}
	t.Fatal("secret-enabled scanner did not call the Secrets API")
}

func TestResourceDiscoveryScannerHandlesKubernetesLabelSelectorsAndMalformedItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/namespaces/template":
			_, _ = w.Write([]byte(`{"metadata":{"name":"template"}}`))
		case "/apis/apps/v1/namespaces/template/deployments":
			// This is ordinary apps/v1 Deployment JSON, including both LabelSelector
			// forms Kubernetes emits in production.
			_, _ = w.Write([]byte(`{"items":[
				{"metadata":{"name":"heimdall","namespace":"template"},"spec":{"selector":{"matchLabels":{"app":"heimdall"},"matchExpressions":[{"key":"version","operator":"In","values":["v2"]},{"key":"zone","operator":"Exists"},{"key":"tier","operator":"In","values":["frontend","edge"]}]},"template":{"metadata":{"labels":{"app":"heimdall","version":"v2"}}}}},
				{"metadata":{"name":"bad-selector","namespace":"template"},"spec":{"selector":"not-a-label-selector"}},
				{"metadata":{"name":"cms","namespace":"template"},"spec":{"selector":{"matchLabels":{"app":"cms"}},"template":{"metadata":{"labels":{"app":"cms"}}}}},
				{"metadata":{"name":"worker.queue-process-events","namespace":"template"},"spec":{"replicas":1,"selector":{"matchLabels":{"app":"worker.queue-process-events"}}},"status":{"readyReplicas":0,"availableReplicas":0,"conditions":[{"type":"Available","status":"False","reason":"MinimumReplicasUnavailable","message":"containers are not ready"}]}}
			]}`))
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		}
	}))
	defer server.Close()

	scanner := NewResourceDiscoveryScanner(NewKubernetesNamespaceSource(server.URL, "token", "", []string{"template"}, server.Client()))
	result, err := scanner.Scan(context.Background(), []string{"template"})
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	byName := map[string]domain.ResourceSnapshot{}
	for _, snapshot := range result.Snapshots {
		if snapshot.Kind == "Deployment" {
			byName[snapshot.Name] = snapshot
		}
	}
	heimdall, ok := byName["heimdall"]
	if !ok {
		t.Fatalf("normal deployment missing: %#v", result.Snapshots)
	}
	if heimdall.Selector["app"] != "heimdall" || heimdall.Selector["version"] != "v2" {
		t.Fatalf("derived deployment selector = %#v", heimdall.Selector)
	}
	if _, hasZone := heimdall.Selector["zone"]; hasZone {
		t.Fatalf("Exists expression must not become an exact selector: %#v", heimdall.Selector)
	}
	if _, hasTier := heimdall.Selector["tier"]; hasTier {
		t.Fatalf("multi-value In expression must not become an exact selector: %#v", heimdall.Selector)
	}
	if _, ok := byName["cms"]; !ok {
		t.Fatalf("valid resource after malformed item was hidden: %#v", result.Snapshots)
	}
	worker := byName["worker.queue-process-events"]
	if worker.Health == nil || worker.Health.Status != "unhealthy" || !strings.Contains(worker.Health.Message, "MinimumReplicasUnavailable") {
		t.Fatalf("unhealthy worker readiness = %#v", worker.Health)
	}
	warnings := strings.Join(result.PermissionWarnings, "\n")
	if !strings.Contains(warnings, "Deployment/bad-selector: invalid spec.selector") {
		t.Fatalf("malformed selector warning missing: %s", warnings)
	}
}

func TestLabelsFromKubernetesLabelSelectorPreservesServiceStyleMap(t *testing.T) {
	labels, err := labelsFromKubernetesLabelSelector(json.RawMessage(`{"app":"heimdall","component":"api"}`))
	if err != nil {
		t.Fatalf("decode service selector: %v", err)
	}
	if labels["app"] != "heimdall" || labels["component"] != "api" {
		t.Fatalf("service selector labels = %#v", labels)
	}
}

func TestSanitizeResourceManifestDefaultsRequiredAPIVersion(t *testing.T) {
	for kind, expectedAPIVersion := range map[string]string{
		"ConfigMap":     "v1",
		"Deployment":    "apps/v1",
		"Ingress":       "networking.k8s.io/v1",
		"NetworkPolicy": "networking.k8s.io/v1",
	} {
		manifest := sanitizeResourceManifest(kind, map[string]any{
			"metadata": map[string]any{"name": "fixture"},
		}, "template", "fixture")
		if got := manifest["apiVersion"]; got != expectedAPIVersion {
			t.Fatalf("%s apiVersion = %q, want %q", kind, got, expectedAPIVersion)
		}
	}
}

func TestSanitizeConfigMapPreservesDataAndBinaryDataWithoutRuntimeFields(t *testing.T) {
	manifest := sanitizeResourceManifest("ConfigMap", map[string]any{
		"apiVersion": "v1", "metadata": map[string]any{"name": "settings", "uid": "uid", "resourceVersion": "42"},
		"data": map[string]any{"app.yaml": "enabled: true"}, "binaryData": map[string]any{"blob": "YQ=="},
		"immutable": true, "status": map[string]any{"observed": true},
	}, "one", "settings")
	if manifest["data"].(map[string]any)["app.yaml"] != "enabled: true" || manifest["binaryData"].(map[string]any)["blob"] != "YQ==" {
		t.Fatalf("ConfigMap data was lost: %#v", manifest)
	}
	metadata := manifest["metadata"].(map[string]any)
	if _, ok := metadata["uid"]; ok {
		t.Fatal("uid leaked into sanitized metadata")
	}
	if _, ok := manifest["status"]; ok {
		t.Fatal("status leaked into sanitized manifest")
	}
}

func TestRuntimeChildrenAreExcludedFromDesiredState(t *testing.T) {
	cronJob := map[string]any{"metadata": map[string]any{"ownerReferences": []any{map[string]any{"kind": "CronJob"}}}}
	completed := map[string]any{"status": map[string]any{"completionTime": "2026-08-18T00:00:00Z"}}
	if !isExcludedRuntimeJob(cronJob) || !isExcludedRuntimeJob(completed) {
		t.Fatal("runtime Job children must be excluded")
	}
}

func TestCompletenessReportIsNamespacedAndDeterministic(t *testing.T) {
	report := buildCompletenessReport([]string{"one", "two"}, []domain.ResourceSnapshot{{Kind: "Service", Namespace: "one", Name: "api"}, {Kind: "Service", Namespace: "two", Name: "api"}}, []string{"one ConfigMap: unsupported API"})
	if len(report.Namespaces) != 2 || report.Namespaces[0].Namespace != "one" || report.Namespaces[1].Namespace != "two" || report.Complete {
		t.Fatalf("unexpected completeness report: %#v", report)
	}
}

func TestGetNamespaceResourceForbiddenIsNonBlocking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/dev-cms" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "token", "", []string{"dev-cms"}, server.Client())
	snapshot, warning, err := NewResourceDiscoveryScanner(source).getNamespaceResource(context.Background(), "dev-cms")
	if err != nil {
		t.Fatalf("get namespace resource failed: %v", err)
	}
	if snapshot.Name != "" || warning != "" {
		t.Fatalf("forbidden namespace metadata must be non-blocking, snapshot=%#v warning=%q", snapshot, warning)
	}
}
