package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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

func TestKubernetesNamespaceSourceSecretMaterializationHTTPHappyPaths(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if got := request.Header.Get("Authorization"); got != "Bearer agent-token" {
			t.Fatalf("authorization = %q", got)
		}
		switch request.URL.Path {
		case "/api/v1/namespaces/approved/secrets/source":
			switch request.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"type":"Opaque","data":{"password":"c2VjcmV0"},"metadata":{"labels":{"managed":"true"},"annotations":{"owner":"test"}}}`))
			case http.MethodDelete:
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("source method = %s", request.Method)
			}
		case "/api/v1/namespaces/approved/secrets/target":
			if request.Method != http.MethodPatch {
				t.Fatalf("Secret apply method = %s", request.Method)
			}
			if request.Header.Get("Content-Type") != "application/apply-patch+yaml" {
				t.Fatalf("Secret apply content type = %q", request.Header.Get("Content-Type"))
			}
			if request.URL.Query().Get("fieldManager") != "envplane-test" || request.URL.Query().Get("force") != "true" {
				t.Fatalf("Secret apply query = %q", request.URL.RawQuery)
			}
			var body struct {
				APIVersion string            `json:"apiVersion"`
				Kind       string            `json:"kind"`
				Data       map[string]string `json:"data"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode Secret apply: %v", err)
			}
			if body.APIVersion != "v1" || body.Kind != "Secret" || body.Data["password"] != "c2VjcmV0" {
				t.Fatalf("Secret apply body = %#v", body)
			}
			w.WriteHeader(http.StatusOK)
		case "/apis/external-secrets.io/v1beta1/namespaces/approved/externalsecrets/target":
			if request.Method != http.MethodPatch {
				t.Fatalf("ExternalSecret apply method = %s", request.Method)
			}
			var body struct {
				APIVersion string `json:"apiVersion"`
				Kind       string `json:"kind"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode ExternalSecret apply: %v", err)
			}
			if body.APIVersion != "external-secrets.io/v1beta1" || body.Kind != "ExternalSecret" {
				t.Fatalf("ExternalSecret apply body = %#v", body)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "agent-token", "", []string{"approved"}, server.Client())
	record, err := source.GetSecret(context.Background(), "approved", "source")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if record.Type != "Opaque" || string(record.Data["password"]) != "secret" || record.Labels["managed"] != "true" || record.Annotations["owner"] != "test" {
		t.Fatalf("GetSecret record = %#v", record)
	}
	if err := source.DeleteSecret(context.Background(), "approved", "source"); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
	apply := SecretApply{Namespace: "approved", Name: "target", Type: "Opaque", Data: map[string][]byte{"password": []byte("secret")}, FieldManager: "envplane-test", Force: true, IdempotencyKey: "test-key", ExternalStore: "tenant-store", ExternalKey: "registry/credentials"}
	if err := source.ApplySecret(context.Background(), apply); err != nil {
		t.Fatalf("ApplySecret: %v", err)
	}
	if err := source.ApplyExternal(context.Background(), apply); err != nil {
		t.Fatalf("ApplyExternal: %v", err)
	}
	if got := requests.Load(); got != 4 {
		t.Fatalf("requests = %d, want 4", got)
	}
}

func TestKubernetesNamespaceSourceRejectsAllOperationsOutsideScope(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "agent-token", "", []string{"approved"}, server.Client())
	apply := SecretApply{Namespace: "other-tenant", Name: "target", Type: "Opaque", FieldManager: "envplane-test", IdempotencyKey: "test-key", ExternalStore: "tenant-store", ExternalKey: "registry/credentials"}
	operations := map[string]func() error{
		"get":            func() error { _, err := source.GetSecret(context.Background(), "other-tenant", "source"); return err },
		"delete":         func() error { return source.DeleteSecret(context.Background(), "other-tenant", "source") },
		"apply-secret":   func() error { return source.ApplySecret(context.Background(), apply) },
		"apply-external": func() error { return source.ApplyExternal(context.Background(), apply) },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, ErrSecretNotFound) {
				t.Fatalf("outside-scope error = %v, want ErrSecretNotFound", err)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("outside-scope operations made %d Kubernetes requests, want 0", got)
	}
}

func TestKubernetesNamespaceSourceEscapesMaterializationPaths(t *testing.T) {
	namespace := "approved space/\u00e5"
	name := "target name/\u00e5"
	secretPath := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/secrets/" + url.PathEscape(name)
	externalPath := "/apis/external-secrets.io/v1beta1/namespaces/" + url.PathEscape(namespace) + "/externalsecrets/" + url.PathEscape(name)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.EscapedPath())
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	source := NewKubernetesNamespaceSource(server.URL, "agent-token", "", []string{namespace}, server.Client())
	apply := SecretApply{Namespace: namespace, Name: name, Type: "Opaque", FieldManager: "envplane-test", IdempotencyKey: "test-key", ExternalStore: "tenant-store", ExternalKey: "registry/credentials"}
	if err := source.ApplySecret(context.Background(), apply); err != nil {
		t.Fatalf("ApplySecret: %v", err)
	}
	if err := source.ApplyExternal(context.Background(), apply); err != nil {
		t.Fatalf("ApplyExternal: %v", err)
	}
	if len(paths) != 2 || paths[0] != secretPath || paths[1] != externalPath {
		t.Fatalf("escaped paths = %#v, want %#v", paths, []string{secretPath, externalPath})
	}
}

func TestKubernetesNamespaceSourceHandlesNonSuccessResponses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		operation func(*KubernetesNamespaceSource) error
		known     error
	}{
		{name: "get unauthorized", status: http.StatusUnauthorized, operation: func(source *KubernetesNamespaceSource) error {
			_, err := source.GetSecret(context.Background(), "approved", "source")
			return err
		}},
		{name: "delete forbidden", status: http.StatusForbidden, operation: func(source *KubernetesNamespaceSource) error {
			return source.DeleteSecret(context.Background(), "approved", "source")
		}},
		{name: "delete not found", status: http.StatusNotFound, operation: func(source *KubernetesNamespaceSource) error {
			return source.DeleteSecret(context.Background(), "approved", "source")
		}, known: ErrSecretNotFound},
		{name: "apply Secret conflict", status: http.StatusConflict, operation: func(source *KubernetesNamespaceSource) error {
			return source.ApplySecret(context.Background(), SecretApply{Namespace: "approved", Name: "target", FieldManager: "envplane-test", IdempotencyKey: "test-key"})
		}, known: ErrMaterializationConflict},
		{name: "apply ExternalSecret unavailable", status: http.StatusInternalServerError, operation: func(source *KubernetesNamespaceSource) error {
			return source.ApplyExternal(context.Background(), SecretApply{Namespace: "approved", Name: "target", FieldManager: "envplane-test", IdempotencyKey: "test-key"})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()

			err := test.operation(NewKubernetesNamespaceSource(server.URL, "agent-token", "", []string{"approved"}, server.Client()))
			if err == nil {
				t.Fatalf("status %d error = nil", test.status)
			}
			if test.known != nil {
				if !errors.Is(err, test.known) {
					t.Fatalf("status %d error = %v, want %v", test.status, err, test.known)
				}
				return
			}
			if !strings.Contains(err.Error(), "status="+http.StatusText(test.status)) && !strings.Contains(err.Error(), "status="+strconv.Itoa(test.status)) {
				t.Fatalf("status %d error = %v", test.status, err)
			}
		})
	}
}
