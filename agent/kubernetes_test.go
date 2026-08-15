package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestKubernetesRateLimiterHonorsContextCancellation(t *testing.T) {
	limiter := newKubernetesRateLimiter(1, 1)
	if err := limiter.wait(context.Background()); err != nil {
		t.Fatalf("consume initial burst: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := limiter.wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v, want deadline exceeded", err)
	}
}

func TestKubernetesNamespaceSourceListsSelectedNamespaces(t *testing.T) {
	var gotAuth string
	var gotSelector string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotSelector = r.URL.Query().Get("labelSelector")
		_ = json.NewEncoder(w).Encode(namespaceList{
			Items: []Namespace{
				{Metadata: NamespaceMetadata{Name: "envpilot-pr-kan-402"}},
				{Metadata: NamespaceMetadata{Name: "envpilot-pr-kan-999"}},
			},
		})
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "kube-token", "app.kubernetes.io/managed-by=envpilot", []string{"envpilot-pr-kan-402"}, server.Client())
	items, err := source.ListNamespaces(context.Background())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}

	if gotAuth != "Bearer kube-token" {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if gotSelector != "app.kubernetes.io/managed-by=envpilot" {
		t.Fatalf("label selector = %q", gotSelector)
	}
	if len(items) != 1 || items[0].Metadata.Name != "envpilot-pr-kan-402" {
		t.Fatalf("unexpected namespaces: %#v", items)
	}
}

func TestKubernetesNamespaceSourceDiscoversPreExistingNamespacesInAllMode(t *testing.T) {
	var gotSelector string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSelector = r.URL.Query().Get("labelSelector")
		_ = json.NewEncoder(w).Encode(namespaceList{Items: []Namespace{
			{Metadata: NamespaceMetadata{Name: "dev-cms"}},
			{Metadata: NamespaceMetadata{Name: "dev-frontend"}},
			{Metadata: NamespaceMetadata{Name: "kube-system"}},
		}})
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "kube-token", "", nil, server.Client(), "kube-system")
	items, err := source.ListNamespaces(context.Background())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}
	if gotSelector != "" {
		t.Fatalf("all-namespaces mode must not set labelSelector, got %q", gotSelector)
	}
	if got, want := []string{items[0].Metadata.Name, items[1].Metadata.Name}, []string{"dev-cms", "dev-frontend"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pre-existing namespaces = %#v want %#v", got, want)
	}
	_, excluded, err := source.listNamespaces(context.Background())
	if err != nil {
		t.Fatalf("list namespaces with diagnostics: %v", err)
	}
	if !reflect.DeepEqual(excluded, []string{"kube-system"}) {
		t.Fatalf("excluded namespaces = %#v", excluded)
	}
}

func TestKubernetesNamespaceSourceUsesNamespacedReadsForExplicitAllowlist(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/v1/namespaces" {
			http.Error(w, "cluster namespace list must not be used", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v1/namespaces/dev-base":
			_ = json.NewEncoder(w).Encode(Namespace{Metadata: NamespaceMetadata{Name: "dev-base"}, Status: NamespaceStatus{Phase: "Active"}})
		case "/api/v1/namespaces/shared":
			_ = json.NewEncoder(w).Encode(Namespace{Metadata: NamespaceMetadata{Name: "shared"}, Status: NamespaceStatus{Phase: "Active"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "kube-token", "", []string{"shared", "dev-base"}, server.Client())
	items, err := source.ListNamespaces(context.Background())
	if err != nil {
		t.Fatalf("list explicit namespaces: %v", err)
	}
	if got, want := []string{items[0].Metadata.Name, items[1].Metadata.Name}, []string{"dev-base", "shared"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit namespaces = %#v want %#v", got, want)
	}
	if !reflect.DeepEqual(paths, []string{"/api/v1/namespaces/dev-base", "/api/v1/namespaces/shared"}) {
		t.Fatalf("namespace paths = %#v", paths)
	}
	if err := source.WatchNamespaces(context.Background(), func(NamespaceEvent) error { return nil }); err != nil {
		t.Fatalf("explicit namespace watch should use polling: %v", err)
	}
}

func TestKubernetesNamespaceSourceListsDeploymentsPodsAndIngresses(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/apis/apps/v1/namespaces/envpilot-pr-kan-403/deployments":
			_ = json.NewEncoder(w).Encode(deploymentList{
				Items: []Deployment{{Metadata: DeploymentMetadata{Name: "cms-api"}}},
			})
		case "/api/v1/namespaces/envpilot-pr-kan-403/pods":
			_ = json.NewEncoder(w).Encode(podList{
				Items: []Pod{{Metadata: PodMetadata{Name: "cms-api-abc"}}},
			})
		case "/apis/networking.k8s.io/v1/namespaces/envpilot-pr-kan-403/ingresses":
			_ = json.NewEncoder(w).Encode(ingressList{
				Items: []Ingress{{
					Metadata: IngressMetadata{Name: "preview"},
					Spec:     IngressSpec{Rules: []IngressRule{{Host: "kan-403.preview.local"}}},
					Status:   IngressStatus{LoadBalancer: IngressLoadBalancerStatus{Ingress: []LoadBalancerIngress{{IP: "10.0.0.15"}}}},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "kube-token", "", nil, server.Client())
	deployments, err := source.ListDeployments(context.Background(), "envpilot-pr-kan-403")
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	pods, err := source.ListPods(context.Background(), "envpilot-pr-kan-403")
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	ingresses, err := source.ListIngresses(context.Background(), "envpilot-pr-kan-403")
	if err != nil {
		t.Fatalf("list ingresses: %v", err)
	}

	if len(deployments) != 1 || deployments[0].Metadata.Name != "cms-api" {
		t.Fatalf("unexpected deployments: %#v", deployments)
	}
	if len(pods) != 1 || pods[0].Metadata.Name != "cms-api-abc" {
		t.Fatalf("unexpected pods: %#v", pods)
	}
	if len(ingresses) != 1 || ingresses[0].Metadata.Name != "preview" {
		t.Fatalf("unexpected ingresses: %#v", ingresses)
	}
	if len(paths) != 3 {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestKubernetesNamespaceSourceFollowsResourceContinuationTokens(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Query().Get("limit"); got != "500" {
			t.Fatalf("limit = %q, want 500", got)
		}
		switch requests {
		case 1:
			if got := r.URL.Query().Get("continue"); got != "" {
				t.Fatalf("first continue token = %q", got)
			}
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"first"}}],"metadata":{"continue":"next-page"}}`))
		case 2:
			if got := r.URL.Query().Get("continue"); got != "next-page" {
				t.Fatalf("second continue token = %q", got)
			}
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"second"}}],"metadata":{}}`))
		}
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "token", "", nil, server.Client())
	items, err := source.ListDeployments(context.Background(), "envpilot")
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if requests != 2 || len(items) != 2 || items[1].Metadata.Name != "second" {
		t.Fatalf("requests=%d items=%#v", requests, items)
	}
}

func TestKubernetesNamespaceSourceListsEvents(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(eventList{
			Items: []KubernetesEvent{
				{
					Metadata: EventMetadata{Name: "event-1", Namespace: "envpilot-pr-kan-404"},
					Type:     "Warning",
					Reason:   "FailedScheduling",
					Message:  "0/3 nodes are available",
				},
			},
		})
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "kube-token", "", nil, server.Client())
	events, err := source.ListEvents(context.Background(), "envpilot-pr-kan-404")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if gotPath != "/api/v1/namespaces/envpilot-pr-kan-404/events" {
		t.Fatalf("path = %q", gotPath)
	}
	if len(events) != 1 || events[0].Reason != "FailedScheduling" {
		t.Fatalf("events = %#v", events)
	}
}

func TestKubernetesNamespaceSourceListsFluxResources(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/apis/kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations":
			_ = json.NewEncoder(w).Encode(fluxKustomizationList{
				Items: []FluxKustomization{{Metadata: FluxMetadata{Name: "kan-405.bethunder", Namespace: "flux-system"}}},
			})
		case "/apis/helm.toolkit.fluxcd.io/v2/namespaces/envpilot-pr-kan-405/helmreleases":
			_ = json.NewEncoder(w).Encode(helmReleaseList{
				Items: []HelmRelease{{Metadata: FluxMetadata{Name: "nginx", Namespace: "envpilot-pr-kan-405"}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "kube-token", "", nil, server.Client())
	kustomizations, err := source.ListFluxKustomizations(context.Background(), "flux-system")
	if err != nil {
		t.Fatalf("list flux kustomizations: %v", err)
	}
	helmReleases, err := source.ListHelmReleases(context.Background(), "envpilot-pr-kan-405")
	if err != nil {
		t.Fatalf("list helm releases: %v", err)
	}

	if len(kustomizations) != 1 || kustomizations[0].Metadata.Name != "kan-405.bethunder" {
		t.Fatalf("kustomizations = %#v", kustomizations)
	}
	if len(helmReleases) != 1 || helmReleases[0].Metadata.Name != "nginx" {
		t.Fatalf("helm releases = %#v", helmReleases)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestKubernetesNamespaceSourceDiscoversCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			_ = json.NewEncoder(w).Encode(map[string]string{"gitVersion": "v1.30.1"})
		case "/api/v1/namespaces":
			_ = json.NewEncoder(w).Encode(namespaceList{Items: []Namespace{
				{Metadata: NamespaceMetadata{Name: "dev-cms"}},
				{Metadata: NamespaceMetadata{Name: "kube-system"}},
			}})
		case "/apis/networking.k8s.io/v1/ingressclasses":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]string{"name": "nginx"}, "spec": map[string]string{"controller": "k8s.io/ingress-nginx"}}}})
		case "/apis/apiextensions.k8s.io/v1/customresourcedefinitions":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]string{"name": "kustomizations.kustomize.toolkit.fluxcd.io"}}}})
		case "/apis/storage.k8s.io/v1/storageclasses":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]string{"name": "standard"}}}})
		case "/api/v1", "/apis/apps/v1", "/apis/kustomize.toolkit.fluxcd.io/v1", "/apis/helm.toolkit.fluxcd.io/v2":
			_ = json.NewEncoder(w).Encode(map[string]string{"kind": "APIResourceList"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "kube-token", "", nil, server.Client(), "kube-system")
	capabilities, err := source.DiscoverCapabilities(context.Background())
	if err != nil {
		t.Fatalf("discover capabilities: %v", err)
	}

	expected := []string{"apps-v1", "core-v1", "flux-helm-v2", "flux-kustomize-v1"}
	if capabilities.KubernetesVersion != "v1.30.1" {
		t.Fatalf("version = %q", capabilities.KubernetesVersion)
	}
	if !reflect.DeepEqual(capabilities.Capabilities, expected) {
		t.Fatalf("capabilities = %#v", capabilities.Capabilities)
	}
	if got, want := capabilities.Report.Namespaces, []string{"dev-cms"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("discovered namespaces = %#v want %#v", got, want)
	}
	if capabilities.Report.NamespaceMode != "all" || !reflect.DeepEqual(capabilities.Report.ExcludedNamespaces, []string{"kube-system"}) {
		t.Fatalf("namespace diagnostics = %#v", capabilities.Report)
	}
	if got, want := capabilities.Report.IngressControllers, []string{"nginx"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ingress classes = %#v want %#v", got, want)
	}
	if got, want := capabilities.Report.StorageClasses, []string{"standard"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("storage classes = %#v want %#v", got, want)
	}
}

func TestKubernetesNamespaceSourceReportsCapabilityRBACForbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/networking.k8s.io/v1/ingressclasses", "/apis/apiextensions.k8s.io/v1/customresourcedefinitions", "/apis/storage.k8s.io/v1/storageclasses":
			http.Error(w, "forbidden by RBAC", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	capabilities, err := NewKubernetesNamespaceSource(server.URL, "kube-token", "", nil, server.Client()).DiscoverCapabilities(context.Background())
	if err != nil {
		t.Fatalf("discover capabilities: %v", err)
	}
	warnings := strings.Join(capabilities.Report.PermissionWarnings, "\n")
	for _, resource := range []string{"ingressclasses.networking.k8s.io", "customresourcedefinitions.apiextensions.k8s.io", "storageclasses.storage.k8s.io"} {
		if !strings.Contains(warnings, "RBAC forbidden: cannot list "+resource) {
			t.Fatalf("expected RBAC warning for %s, got %s", resource, warnings)
		}
	}
}
