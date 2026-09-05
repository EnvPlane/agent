package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestMaterializationWritesRejectNamespacesOutsideAgentScope(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "agent-token", "", []string{"approved"}, server.Client())
	apply := SecretApply{Namespace: "other-tenant", Name: "registry-pull", Type: "Opaque", FieldManager: "envplane-test", IdempotencyKey: "test-key", ExternalStore: "tenant-store", ExternalKey: "registry/credentials"}
	for name, write := range map[string]func(context.Context, SecretApply) error{
		"secret":   source.ApplySecret,
		"external": source.ApplyExternal,
	} {
		t.Run(name, func(t *testing.T) {
			err := write(context.Background(), apply)
			if !errors.Is(err, ErrSecretNotFound) {
				t.Fatalf("outside-scope write error = %v, want ErrSecretNotFound", err)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("outside-scope writes made %d Kubernetes requests, want 0", got)
	}
}

func TestMaterializationWritesAllowApprovedNamespaces(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", request.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "agent-token", "", []string{"approved"}, server.Client())
	apply := SecretApply{Namespace: "approved", Name: "registry-pull", Type: "Opaque", FieldManager: "envplane-test", IdempotencyKey: "test-key", ExternalStore: "tenant-store", ExternalKey: "registry/credentials"}
	for name, write := range map[string]func(context.Context, SecretApply) error{
		"secret":   source.ApplySecret,
		"external": source.ApplyExternal,
	} {
		t.Run(name, func(t *testing.T) {
			if err := write(context.Background(), apply); err != nil {
				t.Fatalf("approved write: %v", err)
			}
		})
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("approved writes made %d Kubernetes requests, want 2", got)
	}
}
