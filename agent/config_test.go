package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnvLoadsPersistedAgentAuthTokenFile(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "agent-auth-token")
	if err := os.WriteFile(tokenPath, []byte("persisted-agent-auth-token\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	t.Setenv("ENVPILOT_CONTROL_PLANE_URL", "https://envpilot.example")
	t.Setenv("ENVPILOT_CLUSTER_ID", "dev-us")
	t.Setenv("ENVPILOT_AGENT_ID", "agent-1")
	t.Setenv("ENVPILOT_AGENT_AUTH_TOKEN_FILE", tokenPath)
	t.Setenv("ENVPILOT_AGENT_REGISTRATION_TOKEN", "")
	t.Setenv("ENVPILOT_AGENT_AUTH_TOKEN", "")
	t.Setenv("ENVPILOT_KUBERNETES_API_URL", "https://kubernetes.example")

	cfg := ConfigFromEnv()
	if cfg.AgentAuthToken != "persisted-agent-auth-token" {
		t.Fatalf("agent auth token = %q", cfg.AgentAuthToken)
	}
	if cfg.AgentAuthTokenFile != tokenPath {
		t.Fatalf("agent auth token file = %q", cfg.AgentAuthTokenFile)
	}
	if strings.Contains(strings.Join(cfg.EnvDiagnostics, " "), "persisted-agent-auth-token") {
		t.Fatal("environment diagnostics must not contain token material")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config with persisted auth token: %v", err)
	}
}

func TestConfigFromEnvCanonicalAliases(t *testing.T) {
	for _, name := range []string{"ENVPILOT_CONTROL_PLANE_URL", "ENVPLANE_CONTROL_PLANE_URL", "ENVPILOT_CLUSTER_ID", "ENVPLANE_CLUSTER_ID", "ENVPILOT_AGENT_ID", "ENVPLANE_AGENT_ID"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	t.Setenv("ENVPLANE_CONTROL_PLANE_URL", "https://canonical.example")
	t.Setenv("ENVPLANE_CLUSTER_ID", "canonical-cluster")
	t.Setenv("ENVPLANE_AGENT_ID", "canonical-agent")
	cfg := ConfigFromEnv()
	if cfg.ControlPlaneURL != "https://canonical.example" || cfg.ClusterID != "canonical-cluster" || cfg.AgentID != "canonical-agent" {
		t.Fatalf("canonical config not loaded: %#v", cfg)
	}
}

func TestConfigFromEnvLegacyFallbackAndCanonicalWins(t *testing.T) {
	for _, name := range []string{"ENVPILOT_CONTROL_PLANE_URL", "ENVPLANE_CONTROL_PLANE_URL", "ENVPILOT_CLUSTER_ID", "ENVPLANE_CLUSTER_ID", "ENVPILOT_AGENT_ID", "ENVPLANE_AGENT_ID"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	t.Setenv("ENVPILOT_CONTROL_PLANE_URL", "https://legacy.example")
	t.Setenv("ENVPILOT_CLUSTER_ID", "legacy-cluster")
	t.Setenv("ENVPILOT_AGENT_ID", "legacy-agent")
	legacy := ConfigFromEnv()
	if legacy.ControlPlaneURL != "https://legacy.example" || len(legacy.EnvDiagnostics) == 0 {
		t.Fatalf("legacy fallback/diagnostic missing: %#v", legacy)
	}
	t.Setenv("ENVPLANE_CONTROL_PLANE_URL", "https://canonical.example")
	if got := ConfigFromEnv().ControlPlaneURL; got != "https://canonical.example" {
		t.Fatalf("canonical did not win mixed configuration: %q", got)
	}
}

func TestConfigFromEnvUsesChartCompatiblePersistedAgentAuthTokenPath(t *testing.T) {
	authDir := filepath.Join(t.TempDir(), "var", "lib", "envpilot-agent", "auth")
	tokenPath := filepath.Join(authDir, "agent-auth-token")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("create chart auth dir: %v", err)
	}
	if err := os.WriteFile(tokenPath, []byte("chart-persisted-agent-auth-token\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	t.Setenv("ENVPILOT_CONTROL_PLANE_URL", "https://envpilot.example")
	t.Setenv("ENVPILOT_CLUSTER_ID", "dev-us")
	t.Setenv("ENVPILOT_AGENT_ID", "agent-1")
	t.Setenv("ENVPILOT_AGENT_AUTH_TOKEN_FILE", tokenPath)
	t.Setenv("ENVPILOT_AGENT_REGISTRATION_TOKEN", "")
	t.Setenv("ENVPILOT_AGENT_AUTH_TOKEN", "")
	t.Setenv("ENVPILOT_KUBERNETES_API_URL", "https://kubernetes.example")

	cfg := ConfigFromEnv()
	if cfg.AgentAuthToken != "chart-persisted-agent-auth-token" {
		t.Fatalf("agent auth token = %q", cfg.AgentAuthToken)
	}
	if cfg.RegistrationToken != "" {
		t.Fatalf("bootstrap token should not be required when chart-compatible auth token file exists")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config with chart-compatible persisted auth token: %v", err)
	}
}

func TestConfigFromEnvTreatsEmptyNamespaceSelectorAsAllNamespaces(t *testing.T) {
	t.Setenv("ENVPILOT_WATCH_NAMESPACE_SELECTOR", "")
	t.Setenv("ENVPILOT_WATCH_EXCLUDED_NAMESPACES", "kube-system,envpilot-system")
	t.Setenv("ENVPILOT_AGENT_NAMESPACE", "envpilot-agent")
	cfg := ConfigFromEnv()
	if cfg.NamespaceSelector != "" {
		t.Fatalf("empty selector must mean all namespaces, got %q", cfg.NamespaceSelector)
	}
	if got, want := cfg.ExcludedNamespaces, []string{"kube-system", "envpilot-system", "envpilot-agent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("excluded namespaces = %#v want %#v", got, want)
	}
}

func TestConfigFromEnvDisablesSecretDiscoveryUnlessExplicitlyEnabled(t *testing.T) {
	t.Setenv("ENVPILOT_DISCOVERY_READ_SECRETS", "")
	if ConfigFromEnv().ReadSecrets {
		t.Fatal("secret discovery must be disabled by default")
	}
	t.Setenv("ENVPILOT_DISCOVERY_READ_SECRETS", "true")
	if !ConfigFromEnv().ReadSecrets {
		t.Fatal("secret discovery must be enabled only by explicit configuration")
	}
}

func TestConfigRejectsHostLocalRemoteControlPlaneEndpoint(t *testing.T) {
	cfg := Config{
		ControlPlaneURL:          "https://host.minikube.internal:18080",
		ControlPlaneEndpointMode: "remote",
		RegistrationToken:        "registration-token",
		ClusterID:                "remote-cluster",
		AgentID:                  "remote-agent",
		KubernetesAPIURL:         "https://kubernetes.example",
		ResyncInterval:           time.Second,
		ReportTimeout:            time.Second,
		HeartbeatInterval:        time.Second,
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "target-pod-reachable") {
		t.Fatalf("remote host-local endpoint error=%v", err)
	}
}

func TestConfigRequiresStableHTTPSForRemoteControlPlaneEndpoint(t *testing.T) {
	cfg := Config{
		ControlPlaneURL:          "http://api.remote.example",
		ControlPlaneEndpointMode: "remote",
		RegistrationToken:        "registration-token",
		ClusterID:                "remote-cluster",
		AgentID:                  "remote-agent",
		KubernetesAPIURL:         "https://kubernetes.example",
		ResyncInterval:           time.Second,
		ReportTimeout:            time.Second,
		HeartbeatInterval:        time.Second,
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "stable HTTPS") {
		t.Fatalf("remote HTTP endpoint error=%v", err)
	}
	cfg.ControlPlaneURL = "https://api.remote.example"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("stable remote HTTPS endpoint must be valid: %v", err)
	}
}

func TestConfigAllowsServiceDNSOnlyForSameCluster(t *testing.T) {
	cfg := Config{
		ControlPlaneURL:          "http://envpilot-control-plane.envpilot.svc:8080",
		ControlPlaneEndpointMode: "sameCluster",
		RegistrationToken:        "registration-token",
		ClusterID:                "same-cluster",
		AgentID:                  "same-agent",
		KubernetesAPIURL:         "https://kubernetes.example",
		ResyncInterval:           time.Second,
		ReportTimeout:            time.Second,
		HeartbeatInterval:        time.Second,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("same-cluster Service DNS must be valid: %v", err)
	}
}

func TestPersistAgentAuthTokenWritesCredentialFile(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "credentials", "agent-auth-token")
	cfg := Config{AgentAuthTokenFile: tokenPath}
	if err := cfg.PersistAgentAuthToken("issued-agent-auth-token"); err != nil {
		t.Fatalf("persist agent auth token: %v", err)
	}
	content, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if string(content) != "issued-agent-auth-token\n" {
		t.Fatalf("token file content = %q", string(content))
	}
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %o", info.Mode().Perm())
	}
}

func TestClearPersistedAgentAuthTokenDropsOnlyRuntimeCredential(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "credentials", "agent-auth-token")
	cfg := Config{AgentAuthTokenFile: tokenPath}
	if err := cfg.PersistAgentAuthToken("issued-agent-auth-token"); err != nil {
		t.Fatalf("persist agent auth token: %v", err)
	}
	if err := cfg.ClearPersistedAgentAuthToken(); err != nil {
		t.Fatalf("clear agent auth token: %v", err)
	}
	if got := readTokenFile(tokenPath); got != "" {
		t.Fatalf("persisted agent auth after clear = %q, want empty", got)
	}
}

func TestCapabilityConfigFingerprintChangesOnlyForDiscoveryConfiguration(t *testing.T) {
	base := Config{
		NamespaceSelector:  "app.kubernetes.io/managed-by=envpilot",
		Namespaces:         []string{"team-a", "team-b"},
		ExcludedNamespaces: []string{"kube-system", "default"},
		FluxNamespace:      "flux-system",
		AgentAuthToken:     "must-not-affect-fingerprint",
	}
	if got, want := base.CapabilityConfigFingerprint(), base.CapabilityConfigFingerprint(); got != want {
		t.Fatalf("fingerprint must be stable: %q != %q", got, want)
	}
	reordered := base
	reordered.Namespaces = []string{"team-b", "team-a"}
	if got, want := reordered.CapabilityConfigFingerprint(), base.CapabilityConfigFingerprint(); got != want {
		t.Fatalf("fingerprint must ignore list ordering: %q != %q", got, want)
	}
	changed := base
	changed.NamespaceSelector = "team=payments"
	if changed.CapabilityConfigFingerprint() == base.CapabilityConfigFingerprint() {
		t.Fatal("selector change must alter fingerprint")
	}
	changed = base
	changed.ReadSecrets = true
	if changed.CapabilityConfigFingerprint() == base.CapabilityConfigFingerprint() {
		t.Fatal("secret discovery change must alter fingerprint")
	}
}
