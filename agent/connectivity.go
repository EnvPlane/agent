package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// CheckControlPlaneHealth verifies the endpoint used by an Agent before it
// attempts registration. It carries no credential, so it is safe to run as a
// bootstrap preflight without consuming a one-time registration token.
func CheckControlPlaneHealth(ctx context.Context, controlPlaneURL string, timeout time.Duration) error {
	return CheckControlPlaneHealthWithCAFile(ctx, controlPlaneURL, timeout, "")
}

// CheckControlPlaneHealthWithCAFile is the pod-context preflight used by
// remote installations. The CA file is mounted from an operator-provided
// Secret; no certificate material is ever accepted through bootstrap APIs.
func CheckControlPlaneHealthWithCAFile(ctx context.Context, controlPlaneURL string, timeout time.Duration, caFile string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(controlPlaneURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid control-plane URL %q", controlPlaneURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("control-plane URL must use http or https")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/health", nil)
	if err != nil {
		return fmt.Errorf("create control-plane health request: %w", err)
	}
	client, err := NewControlPlaneHTTPClient(timeout, caFile)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("endpoint_unreachable: reach control-plane health endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("endpoint_unhealthy: control-plane health endpoint returned status %d", resp.StatusCode)
	}
	return nil
}

func NewControlPlaneHTTPClient(timeout time.Duration, caFile string) (*http.Client, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(caFile) != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read control-plane CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("control-plane CA file contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}
