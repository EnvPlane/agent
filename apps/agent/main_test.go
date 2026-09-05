package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	clusteragent "github.com/envplane/agent/agent"
)

type registrationReporterFake struct {
	token    string
	err      error
	calls    int
	setToken string
}

func (r *registrationReporterFake) RegisterAgent(context.Context, clusteragent.Config, clusteragent.ClusterCapabilities) (string, error) {
	r.calls++
	return r.token, r.err
}

func (r *registrationReporterFake) SetToken(token string) {
	r.setToken = token
}

func TestEnsureRuntimeAuthRegistersAndPersistsRuntimeToken(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "auth", "agent-token")
	reporter := &registrationReporterFake{token: "issued-token"}
	cfg, err := ensureRuntimeAuth(context.Background(), clusteragent.Config{AgentAuthTokenFile: tokenFile, RegistrationToken: "bootstrap-token"}, reporter, clusteragent.ClusterCapabilities{}, slog.Default())
	if err != nil {
		t.Fatalf("ensure runtime auth: %v", err)
	}
	if reporter.calls != 1 || cfg.AgentAuthToken != "issued-token" || cfg.RegistrationToken != "" {
		t.Fatalf("runtime auth result = %#v, calls=%d", cfg, reporter.calls)
	}
	content, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("read persisted token: %v", err)
	}
	if string(content) != "issued-token\n" {
		t.Fatalf("persisted token = %q", content)
	}
}

func TestEnsureRuntimeAuthReregistersAfterStaleRuntimeTokenIsCleared(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "auth", "agent-token")
	cfg := clusteragent.Config{AgentAuthTokenFile: tokenFile, AgentAuthToken: "stale-token", RegistrationToken: "bootstrap-token"}
	if err := cfg.PersistAgentAuthToken(cfg.AgentAuthToken); err != nil {
		t.Fatalf("persist stale token: %v", err)
	}
	if err := cfg.ClearPersistedAgentAuthToken(); err != nil {
		t.Fatalf("clear stale token: %v", err)
	}
	cfg.AgentAuthToken = ""
	reporter := &registrationReporterFake{token: "rotated-token"}
	rotated, err := ensureRuntimeAuth(context.Background(), cfg, reporter, clusteragent.ClusterCapabilities{}, slog.Default())
	if err != nil {
		t.Fatalf("recover runtime auth: %v", err)
	}
	if reporter.calls != 1 || rotated.AgentAuthToken != "rotated-token" || rotated.RegistrationToken != "" {
		t.Fatalf("rotated runtime auth = %#v, calls=%d", rotated, reporter.calls)
	}
	content, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("read rotated token: %v", err)
	}
	if string(content) != "rotated-token\n" {
		t.Fatalf("rotated persisted token = %q", content)
	}
}

func TestRecoverRuntimeAuthRotatesSharedReporterTokenForBackgroundLoops(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "auth", "agent-token")
	cfg := clusteragent.Config{AgentAuthTokenFile: tokenFile, AgentAuthToken: "stale-token"}
	if err := cfg.PersistAgentAuthToken(cfg.AgentAuthToken); err != nil {
		t.Fatalf("persist stale token: %v", err)
	}
	reporter := &registrationReporterFake{token: "rotated-token"}
	recovered, err := recoverRuntimeAuth(context.Background(), cfg, reporter, clusteragent.ClusterCapabilities{}, slog.Default(), "bootstrap-token")
	if err != nil {
		t.Fatalf("recover runtime auth: %v", err)
	}
	if recovered.AgentAuthToken != "rotated-token" || reporter.setToken != "rotated-token" {
		t.Fatalf("recovered token=%q reporter token=%q", recovered.AgentAuthToken, reporter.setToken)
	}
	content, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("read recovered token: %v", err)
	}
	if string(content) != "rotated-token\n" {
		t.Fatalf("recovered persisted token = %q", content)
	}
}

func TestAcquireResourceScanAllowsOnlyOneConcurrentTick(t *testing.T) {
	var running atomic.Bool
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if acquireResourceScan(&running) {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := winners.Load(); got != 1 {
		t.Fatalf("concurrent scan admissions = %d, want 1", got)
	}
	running.Store(false)
	if !acquireResourceScan(&running) {
		t.Fatal("scan admission was not released after completion")
	}
}

func TestIsAgentAuthTokenNotIssuedErrorRecognizesPlainResponse(t *testing.T) {
	err := errors.New(`report heartbeat failed: status=401 body={"error":"agent auth token is not issued for project \"envplane\""}`)
	if !isAgentAuthTokenNotIssuedError(err) {
		t.Fatal("expected plain 401 response to trigger runtime auth recovery")
	}
}

func TestIsAgentAuthTokenNotIssuedErrorRecognizesAPIError(t *testing.T) {
	err := &clusteragent.APIError{Status: 401, Code: "agent_auth_invalid", Message: "agent auth token is not issued for project \"envplane\""}
	if !isAgentAuthTokenNotIssuedError(err) {
		t.Fatal("expected API error to trigger runtime auth recovery")
	}
}

func TestIsAgentAuthTokenNotIssuedErrorRecognizesInvalidAPIToken(t *testing.T) {
	err := errors.New(`report heartbeat failed: status=401 body={"error":"invalid api token"}`)
	if !isAgentAuthTokenNotIssuedError(err) {
		t.Fatal("expected invalid API token response to trigger runtime auth recovery")
	}
}

func TestIsSameClusterIdentityReissuedErrorRecognizesRecoveryCode(t *testing.T) {
	err := &clusteragent.APIError{Status: 401, Code: "same_cluster_identity_reissued", Message: "chart-managed agent identity was reissued; retry registration"}
	if !isSameClusterIdentityReissuedError(err) {
		t.Fatal("expected chart-managed identity recovery response to trigger runtime auth recovery")
	}
}
