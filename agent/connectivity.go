package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/envpilot/contracts/domain"
)

const (
	defaultConnectivityMaxAttempts           = 12
	defaultConnectivityInitialBackoffSeconds = 1
	defaultConnectivityMaxBackoffSeconds     = 5
)

type ControlPlaneConnectivityRetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// WithDefaults returns a safe bounded retry policy for preflight checks.
func (p ControlPlaneConnectivityRetryPolicy) WithDefaults() ControlPlaneConnectivityRetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = defaultConnectivityMaxAttempts
	}
	if p.InitialBackoff <= 0 {
		p.InitialBackoff = time.Duration(defaultConnectivityInitialBackoffSeconds) * time.Second
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = time.Duration(defaultConnectivityMaxBackoffSeconds) * time.Second
	}
	if p.MaxBackoff < p.InitialBackoff {
		p.MaxBackoff = p.InitialBackoff
	}
	return p
}

func (p ControlPlaneConnectivityRetryPolicy) delay(attempt int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	delay := p.InitialBackoff << (attempt - 1)
	if delay <= 0 || delay > p.MaxBackoff {
		delay = p.MaxBackoff
	}
	return delay
}

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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("endpoint_unhealthy: control-plane health endpoint returned status %d", resp.StatusCode)
	}
	return nil
}

// CheckControlPlaneHealthWithCAFileAndRetry polls the control-plane health endpoint
// until it is reachable or retry policy expires. It does not use credentials,
// making it safe for bootstrap preflights.
func CheckControlPlaneHealthWithCAFileAndRetry(ctx context.Context, controlPlaneURL string, timeout time.Duration, caFile string, policy ControlPlaneConnectivityRetryPolicy) error {
	policy = policy.WithDefaults()
	if policy.MaxAttempts <= 1 {
		return CheckControlPlaneHealthWithCAFile(ctx, controlPlaneURL, timeout, caFile)
	}

	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lastErr = CheckControlPlaneHealthWithCAFile(ctx, controlPlaneURL, timeout, caFile)
		if lastErr == nil {
			return nil
		}
		if attempt >= policy.MaxAttempts {
			break
		}
		delay := policy.delay(attempt)
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("control-plane preflight retry limit exceeded: %w", lastErr)
}

func NewControlPlaneHTTPClient(timeout time.Duration, caFile string) (*http.Client, error) {
	return NewControlPlaneHTTPClientWithTLS(timeout, caFile, "")
}

func NewControlPlaneHTTPClientWithTLS(timeout time.Duration, caFile, serverName string) (*http.Client, error) {
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
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: strings.TrimSpace(serverName), MinVersion: tls.VersionTLS12}
	} else if strings.TrimSpace(serverName) != "" {
		transport.TLSClientConfig = &tls.Config{ServerName: strings.TrimSpace(serverName), MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// ProbeManagementEndpoint executes the bounded target-Pod probe after Agent
// authentication exists. Its return type is deliberately safe to persist.
func ProbeManagementEndpoint(ctx context.Context, cfg Config, reporter *HTTPStatusReporter, generation int64) *domain.ManagementEndpointPreflight {
	checked := time.Now().UTC()
	report := &domain.ManagementEndpointPreflight{Generation: generation, Code: "dns_failed", CheckedAt: &checked}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.ControlPlaneURL)), "http://") && !cfg.AllowInsecureControlPlane {
		report.Code = "insecure_transport"
		return report
	}
	client, err := NewControlPlaneHTTPClientWithTLS(cfg.ReportTimeout, cfg.ControlPlaneCAFile, cfg.ControlPlaneTLSServerName)
	if err != nil {
		report.Code = "tls_ca_failed"
		return report
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.ControlPlaneURL, "/")+"/api/v1/health", nil)
	if err != nil {
		return report
	}
	response, err := client.Do(request)
	if err != nil {
		report.Code = classifyEndpointProbeError(err)
		return report
	}
	defer func() { _ = response.Body.Close() }()
	report.DNSResolved, report.TCPConnected = true, true
	if strings.HasPrefix(strings.ToLower(cfg.ControlPlaneURL), "https://") {
		report.TLSVerified = true
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		report.Code = "endpoint_unhealthy"
		return report
	}
	report.HealthReachable = true
	if err := reporter.CheckRuntimeAccess(ctx, cfg); err != nil {
		report.Code = "runtime_auth_failed"
		return report
	}
	report.RuntimeAccess = true
	report.Code = "passed"
	return report
}

func classifyEndpointProbeError(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns_failed"
	}
	var unknownCA x509.UnknownAuthorityError
	if errors.As(err, &unknownCA) {
		return "tls_ca_failed"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "certificate") && (strings.Contains(message, "not valid for") || strings.Contains(message, "server name")) {
		return "tls_server_name_mismatch"
	}
	if strings.Contains(message, "certificate") || strings.Contains(message, "tls") {
		return "tls_ca_failed"
	}
	return "tcp_failed"
}
