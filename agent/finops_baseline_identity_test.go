package agent

import (
	"context"
	"encoding/json"
	"github.com/envplane/contracts/domain"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBaselineResourceOwnershipAndDeletionFailClosed(t *testing.T) {
	for _, kind := range []string{"Pod", "PersistentVolumeClaim"} {
		for _, problem := range []string{"deleting", "environment-owned", "conflicting-component"} {
			t.Run(kind+problem, func(t *testing.T) {
				pin := domain.BaseResourceBinding{BaseResourcePin: domain.BaseResourcePin{Namespace: "base", ResourceKind: kind, ResourceName: "data", ResourceUID: "11111111-1111-4111-8111-111111111111", ComponentID: "db"}, ID: "pin", State: "active", Version: 1, ProjectID: "p", ClusterID: "c", ClusterGeneration: 1}
				s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v1/namespaces/base" {
						_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "base"}})
						return
					}
					labels := map[string]string{"app.kubernetes.io/component": "db"}
					meta := map[string]any{"name": "data", "uid": pin.ResourceUID, "labels": labels}
					switch problem {
					case "deleting":
						meta["deletionTimestamp"] = time.Now().UTC()
					case "environment-owned":
						labels[environmentIDLabel] = "feature-owner"
					case "conflicting-component":
						labels["envplane.io/component"] = "other"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"metadata": meta})
				}))
				defer s.Close()
				source := &KubernetesNamespaceSource{apiURL: s.URL, client: s.Client()}
				if err := source.VerifyBaselineBinding(context.Background(), pin, "p", "c", 1); err == nil {
					t.Fatal("invalid resource admitted")
				}
			})
		}
	}
}

func TestBaselineMetadataDoesNotFollowRedirects(t *testing.T) {
	hit := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer target.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	source := &KubernetesNamespaceSource{apiURL: origin.URL, client: origin.Client()}
	if err := source.baselineGET(context.Background(), "/api/v1/namespaces/base", new(any)); err == nil || hit {
		t.Fatal("redirect accepted")
	}
}
