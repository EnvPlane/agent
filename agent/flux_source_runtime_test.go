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

func TestFluxSourceCommandUsesPersistedTokenWhenReporterTokenIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents/flux-sources/commands/next" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer persisted-runtime-token"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	reporter := NewHTTPStatusReporterForAgent(server.URL, "", "cluster", "agent", time.Second)
	cfg := Config{BootstrapProjectID: "project", ClusterID: "cluster", AgentID: "agent", AgentAuthToken: "persisted-runtime-token"}
	if err := runFluxSourceCommandOnce(context.Background(), cfg, reporter, nil); err != nil {
		t.Fatalf("run Flux source command: %v", err)
	}
}

func TestApplyFluxSourceCreatesProjectKustomization(t *testing.T) {
	patched := map[string]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s", r.Method)
		}
		var object map[string]any
		if err := json.NewDecoder(r.Body).Decode(&object); err != nil {
			t.Fatalf("decode apply object: %v", err)
		}
		patched[r.URL.Path] = object
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "agent-token", "", nil, server.Client())
	command := domain.AgentFluxSourceCommand{
		ContractVersion:      domain.FluxSourceCommandContractVersion,
		CommandID:            "source-1",
		TenantID:             "tenant",
		ProjectID:            "checkout",
		ClusterID:            "cluster",
		AgentID:              "agent",
		Namespace:            "flux-system",
		GitRepositoryName:    "checkout-gitops",
		CredentialSecretName: "checkout-gitops-auth",
		KustomizationName:    "checkout-prs",
		KustomizationPath:    "clusters/dev/apps/checkout",
		RepositoryURL:        "https://gitlab.com/envplane/gitops.git",
		Branch:               "main",
		Status:               domain.FluxSourceCommandClaimed,
		CreatedAt:            time.Unix(1, 0),
	}
	if err := source.applyFluxSource(context.Background(), command, fluxSourceCredential{Username: "git", Password: "token"}); err != nil {
		t.Fatalf("apply Flux source: %v", err)
	}
	path := "/apis/kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/checkout-prs"
	kustomization, ok := patched[path]
	if !ok {
		t.Fatalf("missing Kustomization apply; paths: %v", mapsKeys(patched))
	}
	spec, _ := kustomization["spec"].(map[string]any)
	if spec["path"] != "clusters/dev/apps/checkout" || spec["prune"] != true || spec["wait"] != false {
		t.Fatalf("unexpected Kustomization spec: %#v", spec)
	}
	sourceRef, _ := spec["sourceRef"].(map[string]any)
	if sourceRef["name"] != "checkout-gitops" || sourceRef["kind"] != "GitRepository" {
		t.Fatalf("unexpected sourceRef: %#v", sourceRef)
	}
	secret, ok := patched["/api/v1/namespaces/flux-system/secrets/checkout-gitops-auth"]
	if !ok || secret["type"] != "kubernetes.io/basic-auth" {
		t.Fatalf("Git credential Secret must use basic-auth type: %#v", secret)
	}
}

func TestApplyFluxSourceAdoptsOnlySafeLegacyProjectResources(t *testing.T) {
	patched := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			switch r.URL.Path {
			case "/api/v1/namespaces/flux-system/secrets/checkout-gitops-auth":
				_, _ = w.Write([]byte(`{"type":"kubernetes.io/basic-auth","metadata":{"labels":{}}}`))
			case "/apis/source.toolkit.fluxcd.io/v1/namespaces/flux-system/gitrepositories/checkout-gitops":
				_, _ = w.Write([]byte(`{"metadata":{"labels":{"app.kubernetes.io/managed-by":"envplane","envplane.io/project":"checkout"}}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s", r.Method)
		}
		patched[r.URL.Path] = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "agent-token", "", nil, server.Client())
	command := domain.AgentFluxSourceCommand{ProjectID: "checkout", Namespace: "flux-system", GitRepositoryName: "checkout-gitops", CredentialSecretName: "checkout-gitops-auth", KustomizationName: "checkout-prs", KustomizationPath: "clusters/dev/apps/checkout", RepositoryURL: "https://gitlab.com/envplane/gitops.git", Branch: "main"}
	if err := source.applyFluxSource(context.Background(), command, fluxSourceCredential{Username: "git", Password: "token"}); err != nil {
		t.Fatalf("adopt safe legacy source: %v", err)
	}
	if len(patched) != 3 {
		t.Fatalf("patched resources = %#v", patched)
	}
}

func mapsKeys(values map[string]map[string]any) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return strings.Join(keys, ",")
}
