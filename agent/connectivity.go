package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CheckControlPlaneHealth verifies the endpoint used by an Agent before it
// attempts registration. It carries no credential, so it is safe to run as a
// bootstrap preflight without consuming a one-time registration token.
func CheckControlPlaneHealth(ctx context.Context, controlPlaneURL string, timeout time.Duration) error {
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
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return fmt.Errorf("reach control-plane health endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("control-plane health endpoint returned status %d", resp.StatusCode)
	}
	return nil
}
