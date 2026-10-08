package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDatabaseEscrowKubernetesTransportAndImmutability(t *testing.T) {
	var saved map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-service-account" {
			t.Error("missing authenticated API request")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/external-escrow/secrets":
			if err := json.NewDecoder(r.Body).Decode(&saved); err != nil {
				t.Error("malformed create")
			}
			if saved["immutable"] != true || saved["type"] != "Opaque" {
				t.Error("escrow must be immutable")
			}
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/external-escrow/secrets/db-reviewed":
			if saved == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(saved)
		default:
			t.Error("unexpected or unscoped request")
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()
	source := NewKubernetesNamespaceSource(server.URL, "test-service-account", "", []string{"target"}, server.Client())
	store, err := NewKubernetesDatabaseEscrowStore(source, "external-escrow")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(context.Background(), "db-reviewed"); !errors.Is(err, ErrEscrowNotFound) {
		t.Fatal("404 was not classified")
	}
	if err := store.Create(context.Background(), "db-reviewed", []byte("opaque-encrypted-envelope")); err != nil {
		t.Fatal(err)
	}
	payload, err := store.Read(context.Background(), "db-reviewed")
	if err != nil || string(payload) != "opaque-encrypted-envelope" {
		t.Fatal("escrow round trip failed")
	}
	if saved["data"].(map[string]any)["envelope"] != base64.StdEncoding.EncodeToString(payload) {
		t.Fatal("unexpected payload representation")
	}
	saved["immutable"] = false
	if _, err := store.Read(context.Background(), "db-reviewed"); !errors.Is(err, ErrDatabaseCredentialRecovery) {
		t.Fatal("mutable escrow accepted")
	}
	source.apiURL = "http://unencrypted.invalid"
	if _, err := NewKubernetesDatabaseEscrowStore(source, "external-escrow"); err == nil {
		t.Fatal("unencrypted API accepted")
	}
	source.apiURL = server.URL
	source.token = ""
	if _, err := NewKubernetesDatabaseEscrowStore(source, "external-escrow"); err == nil {
		t.Fatal("unauthenticated API accepted")
	}
}

func TestDatabaseEscrowPVCMarkerIsUIDAndVersionConditional(t *testing.T) {
	for _, mode := range []string{"success", "wrong uid", "wrong volume", "pending", "conflict", "foreign marker", "empty marker", "outside scope"} {
		t.Run(mode, func(t *testing.T) {
			patches := 0
			marker := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/namespaces/target/persistentvolumeclaims/data" {
					t.Error("unscoped PVC access")
				}
				if r.Method == http.MethodPatch {
					patches++
					if r.Header.Get("Content-Type") != "application/json-patch+json" {
						t.Error("not JSON patch")
					}
					var ops []map[string]any
					if err := json.NewDecoder(r.Body).Decode(&ops); err != nil {
						t.Error(err)
					}
					if len(ops) < 3 || ops[0]["op"] != "test" || ops[0]["path"] != "/metadata/uid" || ops[0]["value"] != "pvc-uid" || ops[1]["op"] != "test" || ops[1]["path"] != "/metadata/resourceVersion" || ops[1]["value"] != "123" {
						t.Error("missing exact preconditions")
					}
					if mode == "conflict" {
						w.WriteHeader(http.StatusConflict)
						return
					}
					marker = "db-locator"
					w.WriteHeader(http.StatusOK)
					return
				}
				metadata := map[string]any{"name": "data", "namespace": "target", "uid": "pvc-uid", "resourceVersion": "123"}
				volume, phase := "pv-data", "Bound"
				if mode == "wrong uid" {
					metadata["uid"] = "recreated-uid"
				}
				if mode == "wrong volume" {
					volume = "other-data"
				}
				if mode == "pending" {
					phase = "Pending"
				}
				if mode == "foreign marker" {
					metadata["annotations"] = map[string]string{databaseEscrowPVCMarker: "db-foreign"}
				}
				if mode == "empty marker" {
					metadata["annotations"] = map[string]string{databaseEscrowPVCMarker: ""}
				}
				if marker != "" {
					metadata["annotations"] = map[string]string{databaseEscrowPVCMarker: marker}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"metadata": metadata, "spec": map[string]string{"volumeName": volume}, "status": map[string]string{"phase": phase}})
			}))
			defer server.Close()
			allowed := []string{"target"}
			if mode == "outside scope" {
				allowed = []string{"other"}
			}
			source := NewKubernetesNamespaceSource(server.URL, "test-token", "", allowed, server.Client())
			_, _, _, refs, _ := newEscrowFixture(t, "mysql")
			binding := refs.auth.Binding
			err := source.MarkDatabasePVCInitialization(context.Background(), binding, "db-locator")
			if mode != "success" {
				if !errors.Is(err, ErrDatabaseCredentialRecovery) {
					t.Fatal("unsafe PVC marker accepted")
				}
				if mode != "conflict" && patches != 0 {
					t.Fatal("mismatched PVC patched")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			consumed, err := source.DatabasePVCInitializationConsumed(context.Background(), binding)
			if err != nil || !consumed {
				t.Fatal("marker not durable")
			}
			if err := source.MarkDatabasePVCInitialization(context.Background(), binding, "db-locator"); err != nil || patches != 1 {
				t.Fatal("marker replay must not patch")
			}
		})
	}
}
