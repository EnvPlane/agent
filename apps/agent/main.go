package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	clusteragent "envpilot/agent"
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
	default:
		logger.Error("unknown agent command", "command", command, "usage", "agent|agent-install-check")
		os.Exit(2)
	}
}

func runAgent(logger *slog.Logger) {
	cfg := clusteragent.ConfigFromEnv()
	if err := cfg.Validate(); err != nil {
		logger.Error("invalid agent configuration", "error", err)
		os.Exit(1)
	}
	source, err := clusteragent.NewKubernetesNamespaceSourceFromConfig(cfg)
	if err != nil {
		logger.Error("failed to initialise kubernetes namespace source", "error", err)
		os.Exit(1)
	}
	reporter := clusteragent.NewHTTPStatusReporterForAgent(cfg.ControlPlaneURL, "", cfg.ClusterID, cfg.AgentID, cfg.ReportTimeout)
	watcher := clusteragent.NewNamespaceWatcher(source, reporter, cfg.ResyncInterval, logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	capabilities, err := source.DiscoverCapabilities(ctx)
	if err != nil {
		logger.Error("cluster capability discovery failed", "error", err)
		os.Exit(1)
	}
	cfg, err = ensureRuntimeAuth(ctx, cfg, reporter, capabilities, logger)
	if err != nil {
		logger.Error("agent registration failed", "error", err)
		os.Exit(1)
	}
	go runHeartbeat(ctx, cfg, reporter, source, logger)

	logger.Info("envpilot agent started", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID, "control_plane_url", cfg.ControlPlaneURL)
	if err := watcher.Run(ctx); err != nil {
		logger.Error("envpilot agent stopped", "error", err)
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
	reporter := clusteragent.NewHTTPStatusReporterForAgent(cfg.ControlPlaneURL, "", cfg.ClusterID, cfg.AgentID, cfg.ReportTimeout)
	cfg, err = ensureRuntimeAuth(ctx, cfg, reporter, capabilities, logger)
	if err != nil {
		logger.Error("agent registration failed", "error", err)
		os.Exit(1)
	}
	if err := reporter.ReportHeartbeat(ctx, cfg, capabilities, "online", nil); err != nil {
		logger.Error("agent heartbeat report failed", "error", err)
		os.Exit(1)
	}
	logger.Info("agent install check completed", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID)
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

func runHeartbeat(ctx context.Context, cfg clusteragent.Config, reporter *clusteragent.HTTPStatusReporter, source *clusteragent.KubernetesNamespaceSource, logger *slog.Logger) {
	ticker := time.NewTicker(cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			capabilities, err := source.DiscoverCapabilities(ctx)
			if err != nil {
				logger.Error("cluster capability discovery failed", "error", err)
				continue
			}
			if err := reporter.ReportHeartbeat(ctx, cfg, capabilities, "online", nil); err != nil {
				logger.Error("agent heartbeat failed", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID, "error", err)
			}
			if err := runResourceScanTick(ctx, cfg, reporter, source, logger); err != nil {
				logger.Error("agent resource scan dispatch failed", "cluster_id", cfg.ClusterID, "agent_id", cfg.AgentID, "error", err)
			}
		}
	}
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
