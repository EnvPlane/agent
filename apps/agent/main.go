package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	clusteragent "github.com/envplane/agent/agent"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	command := "agent"
	if len(os.Args) > 1 {
		command = strings.TrimSpace(os.Args[1])
	}
	switch command {
	case "", "agent":
		runAgent(logger)
	case "agent-install-check":
		runAgentInstallCheck(logger)
	case "agent-connectivity-check":
		runAgentConnectivityCheck(logger)
	default:
		logger.Error("unknown agent command", "command", command, "usage", "agent|agent-install-check|agent-connectivity-check")
		os.Exit(2)
	}
}

func runAgent(logger *slog.Logger) {
	cfg := clusteragent.ConfigFromEnv()
	if len(cfg.EnvDiagnostics) > 0 {
		logger.Warn("deprecated legacy configuration variables are in use", "variables", cfg.EnvDiagnostics)
	}
	if err := cfg.Validate(); err != nil {
		logger.Error("invalid agent configuration", "error", err)
		os.Exit(1)
	}
	source, err := clusteragent.NewKubernetesNamespaceSourceFromConfig(cfg)
	if err != nil {
		logger.Error("failed to initialise kubernetes namespace source", "error", err)
		os.Exit(1)
	}
	reporter := clusteragent.NewHTTPStatusReporterForAgentWithTLS(cfg.ControlPlaneURL, "", cfg.ClusterID, cfg.AgentID, cfg.ReportTimeout, cfg.ControlPlaneCAFile, cfg.ControlPlaneTLSServerName)
	watcher := clusteragent.NewNamespaceWatcher(source, reporter, cfg.ResyncInterval, logger)
	watcher.SetTerminalEventQueueDir(cfg.TerminalEventQueueDir)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	capabilities, err := source.DiscoverCapabilities(ctx)
	if err != nil {
		logger.Error("cluster capability discovery failed", "error", err)
		os.Exit(1)
	}
	bootstrapRegistrationToken := cfg.RegistrationToken
	cfg, err = ensureRuntimeAuth(ctx, cfg, reporter, capabilities, logger)
	if err != nil {
		logger.Error("agent registration failed", "error", err)
		os.Exit(1)
	}
	reporter.SetToken(cfg.AgentAuthToken)
	go runHeartbeat(ctx, cfg, reporter, source, logger, bootstrapRegistrationToken)

	logger.Info("envplane agent started", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID, "control_plane_url", cfg.ControlPlaneURL)
	if err := watcher.Run(ctx); err != nil {
		logger.Error("envplane agent stopped", "error", err)
		os.Exit(1)
	}
}

func runAgentInstallCheck(logger *slog.Logger) {
	cfg := clusteragent.ConfigFromEnv()
	if err := cfg.Validate(); err != nil {
		logger.Error("invalid agent install check configuration", "error", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	source, err := clusteragent.NewKubernetesNamespaceSourceFromConfig(cfg)
	if err != nil {
		logger.Error("failed to initialise kubernetes namespace source", "error", err)
		os.Exit(1)
	}
	capabilities, err := source.DiscoverCapabilities(ctx)
	if err != nil {
		logger.Error("cluster capability discovery failed", "error", err)
		os.Exit(1)
	}
	reporter := clusteragent.NewHTTPStatusReporterForAgentWithTLS(cfg.ControlPlaneURL, "", cfg.ClusterID, cfg.AgentID, cfg.ReportTimeout, cfg.ControlPlaneCAFile, cfg.ControlPlaneTLSServerName)
	cfg, err = ensureRuntimeAuth(ctx, cfg, reporter, capabilities, logger)
	if err != nil {
		logger.Error("agent registration failed", "error", err)
		os.Exit(1)
	}
	reporter.SetToken(cfg.AgentAuthToken)
	preflight := clusteragent.ProbeManagementEndpoint(ctx, cfg, reporter, cfg.RemoteGeneration)
	status := "online"
	var statusErr error
	if preflight.Code != "passed" {
		status, statusErr = "degraded", fmt.Errorf("management endpoint preflight failed: %s", preflight.Code)
	}
	if err := reporter.ReportHeartbeatWithEndpointPreflight(ctx, cfg, capabilities, status, statusErr, preflight); err != nil {
		logger.Error("agent heartbeat report failed", "error", err)
		os.Exit(1)
	}
	logger.Info("agent install check completed", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID)
}

// runAgentConnectivityCheck deliberately checks only the control-plane health
// endpoint. It is used before Helm installation from the same Agent image that
// will run after installation, without consuming a one-time bootstrap token.
func runAgentConnectivityCheck(logger *slog.Logger) {
	cfg := clusteragent.ConfigFromEnv()
	if err := clusteragent.ValidateControlPlaneEndpointWithPolicy(cfg.ControlPlaneURL, cfg.ControlPlaneEndpointMode, cfg.AllowInsecureControlPlane); err != nil {
		logger.Error("agent control-plane connectivity check failed", "error", err)
		os.Exit(1)
	}
	policy := clusteragent.ControlPlaneConnectivityRetryPolicy{
		MaxAttempts:    getenvInt("ENVPLANE_CONTROL_PLANE_CONNECTIVITY_MAX_ATTEMPTS", 12),
		InitialBackoff: time.Duration(getenvInt("ENVPLANE_CONTROL_PLANE_CONNECTIVITY_INITIAL_BACKOFF_SECONDS", 1)) * time.Second,
		MaxBackoff:     time.Duration(getenvInt("ENVPLANE_CONTROL_PLANE_CONNECTIVITY_MAX_BACKOFF_SECONDS", 5)) * time.Second,
	}
	deadlineSeconds := getenvInt("ENVPLANE_CONTROL_PLANE_CONNECTIVITY_DEADLINE_SECONDS", 120)
	if deadlineSeconds < 5 {
		deadlineSeconds = 5
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(deadlineSeconds)*time.Second)
	defer cancel()
	if err := clusteragent.CheckControlPlaneHealthWithCAFileAndRetry(ctx, cfg.ControlPlaneURL, cfg.ReportTimeout, cfg.ControlPlaneCAFile, policy); err != nil {
		errMsg := err
		if errorsIsTimeout(err) {
			errMsg = fmt.Errorf("control-plane connectivity check timed out before authenticated readiness was reachable: %w", err)
		}
		logger.Error("agent control-plane connectivity check failed", "error", errMsg, "retryable", true, "maxAttempts", policy.MaxAttempts)
		os.Exit(1)
	}
	logger.Info("agent control-plane connectivity check completed", "control_plane_url", cfg.ControlPlaneURL)
}

func errorsIsTimeout(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout") || strings.Contains(strings.ToLower(err.Error()), "context deadline")
}

func getenvInt(name string, fallback int) int {
	value := getenvCompat(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func getenvCompat(legacy string) string {
	if strings.HasPrefix(legacy, "ENVPLANE_") {
		if value := strings.TrimSpace(os.Getenv("ENVPLANE_" + strings.TrimPrefix(legacy, "ENVPLANE_"))); value != "" {
			return value
		}
	}
	return strings.TrimSpace(os.Getenv(legacy))
}

func ensureRuntimeAuth(ctx context.Context, cfg clusteragent.Config, reporter *clusteragent.HTTPStatusReporter, capabilities clusteragent.ClusterCapabilities, logger *slog.Logger) (clusteragent.Config, error) {
	if strings.TrimSpace(cfg.AgentAuthToken) != "" {
		cfg.RegistrationToken = ""
		logger.Info("agent using persisted auth token", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID)
		return cfg, nil
	}
	token, err := reporter.RegisterAgent(ctx, cfg, capabilities)
	if err != nil {
		return cfg, err
	}
	if strings.TrimSpace(token) == "" {
		return cfg, fmt.Errorf("agent registration response did not include agentAuthToken")
	}
	if err := cfg.PersistAgentAuthToken(token); err != nil {
		return cfg, fmt.Errorf("persist agent auth token: %w", err)
	}
	cfg.AgentAuthToken = token
	cfg.RegistrationToken = ""
	return cfg, nil
}

func runHeartbeat(ctx context.Context, cfg clusteragent.Config, reporter *clusteragent.HTTPStatusReporter, source *clusteragent.KubernetesNamespaceSource, logger *slog.Logger, bootstrapRegistrationToken string) {
	ticker := time.NewTicker(cfg.HeartbeatInterval)
	defer ticker.Stop()
	var scanRunning atomic.Bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			capabilities, err := source.DiscoverCapabilities(tickCtx)
			if err != nil {
				cancel()
				logger.Error("cluster capability discovery failed", "error", err)
				continue
			}
			preflight := clusteragent.ProbeManagementEndpoint(tickCtx, cfg, reporter, cfg.RemoteGeneration)
			status := "online"
			var statusErr error
			if preflight.Code != "passed" {
				status, statusErr = "degraded", fmt.Errorf("management endpoint preflight failed: %s", preflight.Code)
			}
			if err := reporter.ReportHeartbeatWithEndpointPreflight(tickCtx, cfg, capabilities, status, statusErr, preflight); err != nil {
				cancel()
				if (isFixtureIdentityReissuedError(err) || isSameClusterIdentityReissuedError(err) || isAgentAuthTokenNotIssuedError(err)) && strings.TrimSpace(bootstrapRegistrationToken) != "" {
					recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 2*cfg.ReportTimeout)
					// The control plane re-opened the explicit E2E fixture's hashed
					// registration claim, or rejected a stale runtime token. Drop only
					// the persisted runtime token and immediately re-register from the
					// mounted Secret; no operator edit or raw-token persistence is needed.
					if clearErr := cfg.ClearPersistedAgentAuthToken(); clearErr != nil {
						logger.Error("clear stale agent auth token", "error", clearErr)
						recoveryCancel()
						continue
					}
					cfg.AgentAuthToken = ""
					cfg.RegistrationToken = bootstrapRegistrationToken
					cfg, err = ensureRuntimeAuth(recoveryCtx, cfg, reporter, capabilities, logger)
					recoveryCancel()
					if err != nil {
						logger.Error("agent fixture identity recovery registration failed", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID, "error", err)
						continue
					}
					reporter.SetToken(cfg.AgentAuthToken)
					logger.Info("agent runtime identity recovered", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID)
					continue
				}
				logger.Error("agent heartbeat failed", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID, "error", err)
			}
			cancel()
			if preflight.Code == "passed" {
				if scanRunning.CompareAndSwap(false, true) {
					go func() {
						defer scanRunning.Store(false)
						scanCtx, scanCancel := context.WithTimeout(ctx, 45*time.Second)
						defer scanCancel()
						if err := runResourceScanTick(scanCtx, cfg, reporter, source, logger); err != nil {
							logger.Error("agent resource scan dispatch failed", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID, "error", err)
						}
					}()
				}
			}
		}
	}
}

func isFixtureIdentityReissuedError(err error) bool {
	var apiErr *clusteragent.APIError
	return err != nil && errors.As(err, &apiErr) && apiErr.Code == "fixture_identity_reissued"
}

func isSameClusterIdentityReissuedError(err error) bool {
	var apiErr *clusteragent.APIError
	return err != nil && errors.As(err, &apiErr) && apiErr.Code == "same_cluster_identity_reissued"
}

func isAgentAuthTokenNotIssuedError(err error) bool {
	var apiErr *clusteragent.APIError
	if err == nil {
		return false
	}
	if errors.As(err, &apiErr) {
		message := strings.ToLower(apiErr.Message)
		if apiErr.Status == 401 && isStaleAgentAuthTokenMessage(message) {
			return true
		}
	}
	// Older control-plane responses use a plain JSON error envelope without a
	// machine-readable code. Preserve recovery for those responses too.
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "status=401") && isStaleAgentAuthTokenMessage(message)
}

func isStaleAgentAuthTokenMessage(message string) bool {
	return strings.Contains(message, "auth token is not issued") || strings.Contains(message, "invalid api token")
}

func runResourceScanTick(ctx context.Context, cfg clusteragent.Config, reporter *clusteragent.HTTPStatusReporter, source *clusteragent.KubernetesNamespaceSource, logger *slog.Logger) error {
	if strings.TrimSpace(cfg.BootstrapProjectID) == "" || strings.TrimSpace(cfg.AgentAuthToken) == "" {
		return nil
	}
	task, err := reporter.FetchResourceScanTask(ctx, cfg)
	if err != nil || task == nil || len(task.Namespaces) == 0 {
		return err
	}
	result, err := clusteragent.NewResourceDiscoveryScanner(source, cfg.ReadSecrets).Scan(ctx, task.Namespaces)
	if err != nil {
		if reportErr := reporter.ReportResourceScanFailure(ctx, cfg, task); reportErr != nil {
			return fmt.Errorf("scan resources: %w; report scan failure: %v", err, reportErr)
		}
		return fmt.Errorf("scan resources: %w", err)
	}
	if err := reporter.ReportResourceScan(ctx, cfg, task, result); err != nil {
		return err
	}
	logger.Info("agent resource scan reported", "project_id", task.ProjectID, "resource_count", len(result.Snapshots), "warning_count", len(result.PermissionWarnings))
	return nil
}
