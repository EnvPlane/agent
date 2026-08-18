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

	"github.com/envpilot/contracts/domain"
	"github.com/envpilot/contracts/sdk/go/envplanesdk"
)

type StatusReporter interface {
	ReportNamespaceStatus(ctx context.Context, report NamespaceStatusReport) error
	ReportEvents(ctx context.Context, environmentID string, events []domain.KubernetesEvent) error
	ReportFluxStatus(ctx context.Context, environmentID string, status domain.FluxStatus) error
}

// APIError preserves the server error code so recovery logic never depends on
// human-readable response text.
type APIError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"error"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api request failed: status=%d code=%s: %s", e.Status, e.Code, e.Message)
}

type batchStatusItem struct {
	EnvironmentID string                   `json:"environmentId"`
	Status        domain.EnvironmentStatus `json:"status"`
	Message       string                   `json:"message,omitempty"`
	ClusterID     string                   `json:"clusterId,omitempty"`
}

func (r *HTTPStatusReporter) ReportNamespaceStatusBatch(ctx context.Context, reports []NamespaceStatusReport) error {
	if len(reports) == 0 {
		return nil
	}
	items := make([]batchStatusItem, 0, len(reports))
	for _, report := range reports {
		if strings.TrimSpace(report.EnvironmentID) == "" {
			return fmt.Errorf("environment id is required")
		}
		items = append(items, batchStatusItem{EnvironmentID: report.EnvironmentID, Status: report.Status, Message: report.Message, ClusterID: r.clusterID})
	}
	if err := r.sdkClient.DoJSON(ctx, http.MethodPost, "/api/v1/environments/status:batch", map[string]any{"items": items}, nil, ""); err != nil {
		return fmt.Errorf("report namespace status batch failed: %w", err)
	}
	return nil
}

func (r *HTTPStatusReporter) ReportEventsBatch(ctx context.Context, reports []EnvironmentEventsReport) error {
	if len(reports) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(reports))
	for _, report := range reports {
		if strings.TrimSpace(report.EnvironmentID) == "" {
			return fmt.Errorf("environment id is required")
		}
		items = append(items, map[string]any{"environmentId": report.EnvironmentID, "clusterId": r.clusterID, "events": report.Events})
	}
	if err := r.sdkClient.DoJSON(ctx, http.MethodPost, "/api/v1/environments/events:batch", map[string]any{"items": items}, nil, ""); err != nil {
		return fmt.Errorf("report events batch failed: %w", err)
	}
	return nil
}

type NamespaceStatusReport = domain.NamespaceStatusReport

type HTTPStatusReporter struct {
	baseURL   string
	token     string
	clusterID string
	agentID   string
	client    *http.Client
	sdkClient envplanesdk.Client
}

// SetToken updates the runtime credential obtained during agent registration.
func (r *HTTPStatusReporter) SetToken(token string) {
	r.token = strings.TrimSpace(token)
}

func NewHTTPStatusReporterForAgent(baseURL, token, clusterID, agentID string, timeout time.Duration) *HTTPStatusReporter {
	return NewHTTPStatusReporterForAgentWithCAFile(baseURL, token, clusterID, agentID, timeout, "")
}

func NewHTTPStatusReporterForAgentWithCAFile(baseURL, token, clusterID, agentID string, timeout time.Duration, caFile string) *HTTPStatusReporter {
	return NewHTTPStatusReporterForAgentWithTLS(baseURL, token, clusterID, agentID, timeout, caFile, "")
}

func NewHTTPStatusReporterForAgentWithTLS(baseURL, token, clusterID, agentID string, timeout time.Duration, caFile, serverName string) *HTTPStatusReporter {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client, err := NewControlPlaneHTTPClientWithTLS(timeout, caFile, serverName)
	if err != nil {
		// Preserve the reporter API for callers that report configuration errors
		// through their normal registration/heartbeat path.
		client = &http.Client{Timeout: timeout}
	}
	reporter := &HTTPStatusReporter{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     strings.TrimSpace(token),
		clusterID: strings.TrimSpace(clusterID),
		agentID:   strings.TrimSpace(agentID),
		client:    client,
	}
	reporter.sdkClient = envplanesdk.Client{
		BaseURL:    reporter.baseURL,
		HTTPClient: client,
		TokenProvider: func(context.Context) (string, error) {
			return reporter.token, nil
		},
	}
	return reporter
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
	path := "/api/v1/environments/" + url.PathEscape(report.EnvironmentID) + "/status"
	if err := r.sdkClient.DoJSON(ctx, http.MethodPost, path, payload, nil, ""); err != nil {
		return fmt.Errorf("report namespace status failed: environment=%s: %w", report.EnvironmentID, err)
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
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
	return r.ReportHeartbeatWithEndpointPreflight(ctx, cfg, capabilities, status, statusErr, nil)
}

func (r *HTTPStatusReporter) ReportHeartbeatWithEndpointPreflight(ctx context.Context, cfg Config, capabilities ClusterCapabilities, status string, statusErr error, preflight *domain.ManagementEndpointPreflight) error {
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
		EndpointPreflight:        preflight,
		ObservedAt:               observedAt,
	}
	return r.postJSON(ctx, "/api/v1/agents/heartbeat", payload, "report heartbeat")
}

// CheckRuntimeAccess is the authenticated final leg of the target-Pod
// preflight. It consumes neither registration tokens nor response bodies.
func (r *HTTPStatusReporter) CheckRuntimeAccess(ctx context.Context, cfg Config) error {
	query := url.Values{}
	query.Set("projectId", cfg.BootstrapProjectID)
	query.Set("clusterId", cfg.ClusterID)
	query.Set("agentId", cfg.AgentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/v1/agents/runtime-access?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AgentAuthToken)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("runtime access denied")
	}
	return nil
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
		ProjectID:              cfg.BootstrapProjectID,
		ClusterID:              cfg.ClusterID,
		AgentID:                cfg.AgentID,
		ScanID:                 task.ScanID,
		Status:                 "completed",
		ResourceSnapshots:      result.Snapshots,
		ServiceGraph:           result.ServiceGraph,
		ServiceEnvs:            result.ServiceEnvs,
		PermissionWarnings:     result.PermissionWarnings,
		Completeness:           result.Completeness,
		SourceHealthDiagnostic: result.SourceHealthDiagnostic,
		ObservedAt:             time.Now().UTC(),
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var apiError APIError
		if json.Unmarshal(responseBody, &apiError) == nil && strings.TrimSpace(apiError.Code) != "" {
			apiError.Status = resp.StatusCode
			if apiError.Message == "" {
				apiError.Message = operation
			}
			return &apiError
		}
		return fmt.Errorf("%s failed: status=%d body=%s", operation, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("%s response decode failed: %w", operation, err)
		}
	}
	return nil
}
