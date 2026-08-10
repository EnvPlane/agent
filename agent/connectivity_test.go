package agent

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestCheckControlPlaneHealthAcceptsPrivateCAFromMountedFile(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	certificate := server.Certificate()
	if certificate == nil {
		t.Fatal("TLS server certificate is missing")
	}
	caPath := filepath.Join(t.TempDir(), "management-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600); err != nil {
		t.Fatalf("write private CA fixture: %v", err)
	}
	if err := CheckControlPlaneHealthWithCAFile(context.Background(), server.URL, time.Second, caPath); err != nil {
		t.Fatalf("private CA health check: %v", err)
	}
	if err := CheckControlPlaneHealthWithCAFile(context.Background(), server.URL, time.Second, filepath.Join(t.TempDir(), "missing.pem")); err == nil || !strings.Contains(err.Error(), "read control-plane CA file") {
		t.Fatalf("missing CA must fail safely, got %v", err)
	}
}

func TestCheckControlPlaneHealthWithCAFileAndRetrySucceedsAfterTransientFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := CheckControlPlaneHealthWithCAFileAndRetry(ctx, server.URL, time.Second, "", ControlPlaneConnectivityRetryPolicy{
		MaxAttempts:    4,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("retry health check should recover: %v", err)
	}
	if attempts < 2 {
		t.Fatalf("expected retry before success, got attempts=%d", attempts)
	}
}

func TestCheckControlPlaneHealthWithCAFileAndRetryRespectsDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := CheckControlPlaneHealthWithCAFileAndRetry(ctx, server.URL, 100*time.Millisecond, "", ControlPlaneConnectivityRetryPolicy{
		MaxAttempts:    5,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     2 * time.Second,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "context deadline") {
		t.Fatalf("expected deadline error, got %v", err)
	}
}
