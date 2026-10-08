package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCredentialPVCInventoryFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, body       string
		status           int
		present, failure bool
	}{
		{"empty", `{"items":[]}`, 200, false, false},
		{"retained", `{"items":[{}]}`, 200, true, false},
		{"pagination", `{"items":[],"metadata":{"continue":"more"}}`, 200, true, false},
		{"unknown", `{}`, 200, false, true},
		{"null", `{"items":null}`, 200, false, true},
		{"denied", `{}`, 403, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/approved/persistentvolumeclaims" || r.URL.Query().Get("limit") != "1" {
					t.Error("unscoped inventory")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			source := NewKubernetesNamespaceSource(server.URL, "", "", []string{"approved"}, server.Client())
			present, err := source.HasPersistentVolumeClaims(context.Background(), "approved")
			if (err != nil) != tc.failure || present != tc.present {
				t.Fatalf("present=%t failure=%t", present, err != nil)
			}
		})
	}
}

func TestCredentialCreateUsesAtomicPostNotApply(t *testing.T) {
	for _, status := range []int{201, 409, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/namespaces/approved/secrets" || r.URL.RawQuery != "" {
					t.Error("create must not force/patch")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			source := NewKubernetesNamespaceSource(server.URL, "", "", []string{"approved"}, server.Client())
			apply := SecretApply{Namespace: "approved", Name: "db", Type: "Opaque", IdempotencyKey: "key"}
			err := source.CreateGeneratedSecret(context.Background(), apply)
			if (err == nil) != (status == 201) {
				t.Fatalf("unexpected status result: %v", err)
			}
			if status == 409 && !errors.Is(err, ErrMaterializationConflict) {
				t.Fatal("conflict not classified")
			}
			apply.Namespace = "outside"
			if err := source.CreateGeneratedSecret(context.Background(), apply); err == nil {
				t.Fatal("outside scope accepted")
			}
			if requests != 1 {
				t.Fatal("outside scope request")
			}
		})
	}
}
