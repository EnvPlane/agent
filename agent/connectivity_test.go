package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckControlPlaneHealthUsesHealthEndpointWithoutCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("health check must not send credentials: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := CheckControlPlaneHealth(context.Background(), server.URL, time.Second); err != nil {
		t.Fatalf("health check: %v", err)
	}
}

func TestCheckControlPlaneHealthRejectsFailedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	if err := CheckControlPlaneHealth(context.Background(), server.URL, time.Second); err == nil || !strings.Contains(err.Error(), "endpoint_unhealthy") {
		t.Fatal("expected failed health endpoint to return an error")
	}
}

func TestCheckControlPlaneHealthClassifiesUnreachableEndpoint(t *testing.T) {
	err := CheckControlPlaneHealth(context.Background(), "http://127.0.0.1:1", 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "endpoint_unreachable") {
		t.Fatalf("expected endpoint_unreachable diagnostic, got %v", err)
	}
}
