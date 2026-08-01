package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"envpilot/internal/domain"
)

type StatusReporter interface {
	ReportNamespaceStatus(ctx context.Context, report NamespaceStatusReport) error
	ReportEvents(ctx context.Context, environmentID string, events []domain.KubernetesEvent) error
	ReportFluxStatus(ctx context.Context, environmentID string, status domain.FluxStatus) error
}

type NamespaceStatusReport struct {
	EnvironmentID string
	Namespace     string
	Status        domain.EnvironmentStatus
	Message       string
	EventType     string
	Phase         string
}

type HTTPStatusReporter struct {
	baseURL   string
	token     string
	clusterID string
	agentID   string
	client    *http.Client
}

func NewHTTPStatusReporter(baseURL, token string, timeout time.Duration) *HTTPStatusReporter {
	return NewHTTPStatusReporterForAgent(baseURL, token, "", "", timeout)
}

func NewHTTPStatusReporterForAgent(baseURL, token, clusterID, agentID string, timeout time.Duration) *HTTPStatusReporter {
	return NewHTTPStatusReporterForAgentWithCAFile(baseURL, token, clusterID, agentID, timeout, "")
}

func NewHTTPStatusReporterForAgentWithCAFile(baseURL, token, clusterID, agentID string, timeout time.Duration, caFile string) *HTTPStatusReporter {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client, err := NewControlPlaneHTTPClient(timeout, caFile)
	if err != nil {
		// Preserve the reporter API for callers that report configuration errors
		// through their normal registration/heartbeat path.
		client = &http.Client{Timeout: timeout}
	}
	return &HTTPStatusReporter{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     strings.TrimSpace(token),
		clusterID: strings.TrimSpace(clusterID),
		agentID:   strings.TrimSpace(agentID),
		client:    client,
	}
}

func (r *HTTPStatusReporter) ReportNamespaceStatus(ctx context.Context, report NamespaceStatusReport) error {
	if strings.TrimSpace(report.EnvironmentID) == "" {
		return fmt.Errorf("environment id is required")
	}
	payload := domain.UpdateEnvironmentStatusRequest{
		Status:    report.Status,
		Message:   report.Message,
		ClusterID: r.clusterID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := r.baseURL + "/api/v1/environments/" + url.PathEscape(report.EnvironmentID) + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("report namespace status failed: environment=%s status=%d body=%s", report.EnvironmentID, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func (r *HTTPStatusReporter) ReportEvents(ctx context.Context, environmentID string, events []domain.KubernetesEvent) error {
	if strings.TrimSpace(environmentID) == "" {
		return fmt.Errorf("environment id is required")
	}
	payload := domain.IngestEnvironmentEventsRequest{ClusterID: r.clusterID, Events: events}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := r.baseURL + "/api/v1/environments/" + url.PathEscape(environmentID) + "/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("report kubernetes events failed: environment=%s status=%d body=%s", environmentID, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func (r *HTTPStatusReporter) ReportFluxStatus(ctx context.Context, environmentID string, status domain.FluxStatus) error {
	if strings.TrimSpace(environmentID) == "" {
		return fmt.Errorf("environment id is required")
	}
	payload := domain.IngestFluxStatusRequest{ClusterID: r.clusterID, FluxStatus: status}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := r.baseURL + "/api/v1/environments/" + url.PathEscape(environmentID) + "/flux-status"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("report flux status failed: environment=%s status=%d body=%s", environmentID, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func (r *HTTPStatusReporter) RegisterAgent(ctx context.Context, cfg Config, capabilities ClusterCapabilities) (string, error) {
	observedAt := time.Now().UTC()
	capabilityReport := capabilityReportForPublish(cfg, capabilities, observedAt)
	payload := domain.AgentRegistrationRequest{
		ProjectID:                cfg.BootstrapProjectID,
		ClusterID:                cfg.ClusterID,
		AgentID:                  cfg.AgentID,
		RegistrationToken:        cfg.RegistrationToken,
		AgentVersion:             cfg.AgentVersion,
		AgentNamespace:           cfg.AgentNamespace,
		KubernetesVersion:        capabilities.KubernetesVersion,
		FluxNamespace:            cfg.FluxNamespace,
		NamespaceSelector:        cfg.NamespaceSelector,
		Capabilities:             capabilities.Capabilities,
		CapabilityReport:         &capabilityReport,
		HeartbeatIntervalSeconds: int(cfg.HeartbeatInterval.Seconds()),
		ObservedAt:               observedAt,
	}
	var response domain.AgentRegistrationResponse
	if err := r.postJSONDecode(ctx, "/api/v1/agents/register", payload, "register agent", &response); err != nil {
		return "", err
	}
	return strings.TrimSpace(response.AgentAuthToken), nil
}

func (r *HTTPStatusReporter) ReportHeartbeat(ctx context.Context, cfg Config, capabilities ClusterCapabilities, status string, statusErr error) error {
	errorMessage := ""
	if statusErr != nil {
		errorMessage = statusErr.Error()
	}
	observedAt := time.Now().UTC()
	capabilityReport := capabilityReportForPublish(cfg, capabilities, observedAt)
	payload := domain.AgentHeartbeatRequest{
		ProjectID:                cfg.BootstrapProjectID,
		ClusterID:                cfg.ClusterID,
		AgentID:                  cfg.AgentID,
		AgentAuthToken:           cfg.AgentAuthToken,
		AgentVersion:             cfg.AgentVersion,
		KubernetesVersion:        capabilities.KubernetesVersion,
		Capabilities:             capabilities.Capabilities,
		CapabilityReport:         &capabilityReport,
		HeartbeatIntervalSeconds: int(cfg.HeartbeatInterval.Seconds()),
		Status:                   status,
		Error:                    errorMessage,
		ObservedAt:               observedAt,
	}
	return r.postJSON(ctx, "/api/v1/agents/heartbeat", payload, "report heartbeat")
}

func capabilityReportForPublish(cfg Config, capabilities ClusterCapabilities, observedAt time.Time) domain.ClusterCapabilityReport {
	report := capabilities.Report
	report.ConfigFingerprint = cfg.CapabilityConfigFingerprint()
	report.ObservedAt = &observedAt
	return report
}

func (r *HTTPStatusReporter) ReportResourceScan(ctx context.Context, cfg Config, task *domain.AgentResourceScanTaskResponse, result ResourceScanResult) error {
	if task == nil || strings.TrimSpace(task.ScanID) == "" {
		return fmt.Errorf("resource scan task scan id is required")
	}
	payload := domain.AgentResourceScanRequest{
		ProjectID:          cfg.BootstrapProjectID,
		ClusterID:          cfg.ClusterID,
		AgentID:            cfg.AgentID,
		ScanID:             task.ScanID,
		Status:             "completed",
		ResourceSnapshots:  result.Snapshots,
		ServiceGraph:       result.ServiceGraph,
		ServiceEnvs:        result.ServiceEnvs,
		PermissionWarnings: result.PermissionWarnings,
		ObservedAt:         time.Now().UTC(),
	}
	return r.postJSONWithBearer(ctx, "/api/v1/agents/resource-scan", payload, cfg.AgentAuthToken, "report resource scan")
}

// ReportResourceScanFailure acknowledges a dispatched scan that could not be
// completed. The diagnostic is intentionally generic: detailed scanner errors
// stay in the agent log and must not be copied into bootstrap session data.
func (r *HTTPStatusReporter) ReportResourceScanFailure(ctx context.Context, cfg Config, task *domain.AgentResourceScanTaskResponse) error {
	if task == nil || strings.TrimSpace(task.ScanID) == "" {
		return fmt.Errorf("resource scan task scan id is required")
	}
	payload := domain.AgentResourceScanRequest{
		ProjectID:  cfg.BootstrapProjectID,
		ClusterID:  cfg.ClusterID,
		AgentID:    cfg.AgentID,
		ScanID:     task.ScanID,
		Status:     "failed",
		ErrorCode:  "resource_scan_failed",
		Error:      "Resource discovery failed. Check agent logs and retry.",
		ObservedAt: time.Now().UTC(),
	}
	return r.postJSONWithBearer(ctx, "/api/v1/agents/resource-scan", payload, cfg.AgentAuthToken, "report resource scan failure")
}

func (r *HTTPStatusReporter) FetchResourceScanTask(ctx context.Context, cfg Config) (*domain.AgentResourceScanTaskResponse, error) {
	query := url.Values{}
	query.Set("projectId", cfg.BootstrapProjectID)
	query.Set("clusterId", cfg.ClusterID)
	query.Set("agentId", cfg.AgentID)
	endpoint := r.baseURL + "/api/v1/agents/resource-scan/next?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(cfg.AgentAuthToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fetch resource scan task failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var task domain.AgentResourceScanTaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *HTTPStatusReporter) postJSON(ctx context.Context, path string, payload any, operation string) error {
	return r.postJSONDecode(ctx, path, payload, operation, nil)
}

func (r *HTTPStatusReporter) postJSONWithBearer(ctx context.Context, path string, payload any, bearerToken string, operation string) error {
	return r.postJSONDecodeWithBearer(ctx, path, payload, operation, nil, bearerToken)
}

func (r *HTTPStatusReporter) postJSONDecode(ctx context.Context, path string, payload any, operation string, output any) error {
	return r.postJSONDecodeWithBearer(ctx, path, payload, operation, output, r.token)
}

func (r *HTTPStatusReporter) postJSONDecodeWithBearer(ctx context.Context, path string, payload any, operation string, output any, bearerToken string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(bearerToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s failed: status=%d body=%s", operation, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("%s response decode failed: %w", operation, err)
		}
	}
	return nil
}
