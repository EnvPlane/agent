package agent

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultServiceAccountToken = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	defaultServiceAccountCA    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	defaultExcludedNamespaces  = "default,kube-system,kube-public,kube-node-lease,local-path-storage,ingress-nginx,kubernetes-dashboard,envpilot,envpilot-system"
	defaultKubernetesQPS       = 20.0
	defaultKubernetesBurst     = 40
)

type Config struct {
	// EnvDiagnostics contains variable names only; values are never retained.
	EnvDiagnostics            []string
	ControlPlaneURL           string
	ControlPlaneEndpointMode  string
	ControlPlaneCAFile        string
	ControlPlaneTLSServerName string
	RegistrationToken         string
	AgentAuthToken            string
	AgentAuthTokenFile        string
	BootstrapProjectID        string
	ClusterID                 string
	AgentID                   string
	AgentNamespace            string
	AgentVersion              string
	KubernetesAPIURL          string
	KubernetesToken           string
	KubernetesCA              string
	NamespaceSelector         string
	Namespaces                []string
	ExcludedNamespaces        []string
	ReadSecrets               bool
	FluxNamespace             string
	ResyncInterval            time.Duration
	ReportTimeout             time.Duration
	HeartbeatInterval         time.Duration
	KubernetesQPS             float64
	KubernetesBurst           int
	RemoteGeneration          int64
}

// CapabilityConfigFingerprint identifies the configuration that changes
// capability discovery. It deliberately excludes credentials and endpoints so
// it is safe to persist in the control plane and display in the UI.
func (c Config) CapabilityConfigFingerprint() string {
	normalizeList := func(values []string) string {
		seen := map[string]struct{}{}
		items := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			items = append(items, value)
		}
		sort.Strings(items)
		return strings.Join(items, ",")
	}
	payload := strings.Join([]string{
		"v1",
		"selector=" + strings.TrimSpace(c.NamespaceSelector),
		"namespaces=" + normalizeList(c.Namespaces),
		"excludedNamespaces=" + normalizeList(c.ExcludedNamespaces),
		"fluxNamespace=" + strings.TrimSpace(c.FluxNamespace),
		"readSecrets=" + strconv.FormatBool(c.ReadSecrets),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("sha256:%x", sum[:])
}

func ConfigFromEnv() Config {
	agentAuthTokenFile := getenv("ENVPILOT_AGENT_AUTH_TOKEN_FILE", "")
	agentAuthToken := getenv("ENVPILOT_AGENT_AUTH_TOKEN", "")
	if strings.TrimSpace(agentAuthToken) == "" {
		agentAuthToken = readTokenFile(agentAuthTokenFile)
	}
	agentNamespace := getenv("ENVPILOT_AGENT_NAMESPACE", "")
	excludedNamespaces := splitCSV(getenv("ENVPILOT_WATCH_EXCLUDED_NAMESPACES", defaultExcludedNamespaces))
	if strings.TrimSpace(agentNamespace) != "" {
		excludedNamespaces = append(excludedNamespaces, strings.TrimSpace(agentNamespace))
	}
	cfg := Config{
		ControlPlaneURL:           getenv("ENVPILOT_CONTROL_PLANE_URL", ""),
		ControlPlaneEndpointMode:  strings.TrimSpace(getenv("ENVPILOT_CONTROL_PLANE_ENDPOINT_MODE", "sameCluster")),
		ControlPlaneCAFile:        getenv("ENVPILOT_CONTROL_PLANE_CA_FILE", ""),
		ControlPlaneTLSServerName: getenv("ENVPILOT_CONTROL_PLANE_TLS_SERVER_NAME", ""),
		RegistrationToken:         getenv("ENVPILOT_AGENT_REGISTRATION_TOKEN", ""),
		AgentAuthToken:            agentAuthToken,
		AgentAuthTokenFile:        agentAuthTokenFile,
		BootstrapProjectID:        getenv("ENVPILOT_BOOTSTRAP_PROJECT_ID", ""),
		ClusterID:                 getenv("ENVPILOT_CLUSTER_ID", "default"),
		AgentID:                   getenv("ENVPILOT_AGENT_ID", hostname()),
		AgentNamespace:            agentNamespace,
		AgentVersion:              getenv("ENVPILOT_AGENT_VERSION", "dev"),
		KubernetesAPIURL:          getenv("ENVPILOT_KUBERNETES_API_URL", inClusterAPIURL()),
		KubernetesToken:           getenv("ENVPILOT_KUBERNETES_TOKEN_PATH", defaultServiceAccountToken),
		KubernetesCA:              getenv("ENVPILOT_KUBERNETES_CA_PATH", defaultServiceAccountCA),
		// An empty selector intentionally means all namespaces.
		NamespaceSelector:  strings.TrimSpace(getenv("ENVPILOT_WATCH_NAMESPACE_SELECTOR", "")),
		Namespaces:         splitCSV(getenv("ENVPILOT_WATCH_NAMESPACES", "")),
		ExcludedNamespaces: excludedNamespaces,
		ReadSecrets:        getenvBool("ENVPILOT_DISCOVERY_READ_SECRETS", false),
		FluxNamespace:      getenv("ENVPILOT_FLUX_NAMESPACE", "flux-system"),
		ResyncInterval:     time.Duration(getenvInt("ENVPILOT_AGENT_RESYNC_SECONDS", 30)) * time.Second,
		ReportTimeout:      time.Duration(getenvInt("ENVPILOT_AGENT_REPORT_TIMEOUT_SECONDS", 10)) * time.Second,
		HeartbeatInterval:  time.Duration(getenvInt("ENVPILOT_AGENT_HEARTBEAT_SECONDS", 30)) * time.Second,
		KubernetesQPS:      getenvFloat("ENVPILOT_KUBERNETES_QPS", defaultKubernetesQPS),
		KubernetesBurst:    getenvInt("ENVPILOT_KUBERNETES_BURST", defaultKubernetesBurst),
		RemoteGeneration:   int64(getenvInt("ENVPILOT_REMOTE_GENERATION", 0)),
	}
	cfg.EnvDiagnostics = legacyDiagnostics()
	return cfg
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.ControlPlaneURL) == "" {
		return fmt.Errorf("ENVPLANE_CONTROL_PLANE_URL is required")
	}
	if err := ValidateControlPlaneEndpoint(c.ControlPlaneURL, c.ControlPlaneEndpointMode); err != nil {
		return err
	}
	if strings.TrimSpace(c.ControlPlaneCAFile) != "" {
		if _, err := NewControlPlaneHTTPClientWithTLS(c.ReportTimeout, c.ControlPlaneCAFile, c.ControlPlaneTLSServerName); err != nil {
			return fmt.Errorf("invalid ENVPLANE_CONTROL_PLANE_CA_FILE: %w", err)
		}
	}
	if strings.TrimSpace(c.ClusterID) == "" {
		return fmt.Errorf("ENVPLANE_CLUSTER_ID is required")
	}
	if strings.TrimSpace(c.AgentID) == "" {
		return fmt.Errorf("envplane agent id is required")
	}
	if strings.TrimSpace(c.RegistrationToken) == "" && strings.TrimSpace(c.AgentAuthToken) == "" {
		return fmt.Errorf("set ENVPLANE_AGENT_REGISTRATION_TOKEN or ENVPLANE_AGENT_AUTH_TOKEN")
	}
	if strings.TrimSpace(c.KubernetesAPIURL) == "" {
		return fmt.Errorf("kubernetes API URL is required; set ENVPLANE_KUBERNETES_API_URL outside the cluster")
	}
	if c.ResyncInterval <= 0 {
		return fmt.Errorf("resync interval must be positive")
	}
	if c.ReportTimeout <= 0 {
		return fmt.Errorf("report timeout must be positive")
	}
	if c.HeartbeatInterval <= 0 {
		return fmt.Errorf("heartbeat interval must be positive")
	}
	if c.KubernetesQPS < 0 || (c.KubernetesQPS == 0 && c.KubernetesBurst != 0) {
		return fmt.Errorf("kubernetes qps must be positive or zero with zero burst")
	}
	if c.KubernetesQPS > 0 && c.KubernetesBurst <= 0 {
		return fmt.Errorf("kubernetes burst must be positive when qps is enabled")
	}
	return nil
}

// validateControlPlaneEndpoint rejects host-only and cluster-local addresses
// only when the chart declares a remote deployment. Same-cluster Agents use
// Kubernetes Service DNS and are intentionally allowed to do so.
func ValidateControlPlaneEndpoint(rawURL, endpointMode string) error {
	mode := strings.ToLower(strings.TrimSpace(endpointMode))
	if mode == "" {
		mode = "samecluster"
	}
	if mode != "samecluster" && mode != "remote" {
		return fmt.Errorf("ENVPLANE_CONTROL_PLANE_ENDPOINT_MODE must be sameCluster or remote")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return fmt.Errorf("ENVPLANE_CONTROL_PLANE_URL must be an HTTP(S) URL")
	}
	if mode != "remote" {
		return nil
	}
	if parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("remote ENVPLANE_CONTROL_PLANE_URL must be an explicit stable HTTPS URL without credentials, query parameters, or fragments")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "envpilot.local" || host == "host.minikube.internal" || strings.HasSuffix(host, ".svc") || strings.Contains(host, ".svc.") {
		return fmt.Errorf("remote ENVPLANE_CONTROL_PLANE_URL must be target-pod-reachable, not host-local or Kubernetes Service DNS")
	}
	return nil
}

func (c Config) PersistAgentAuthToken(token string) error {
	token = strings.TrimSpace(token)
	path := strings.TrimSpace(c.AgentAuthTokenFile)
	if token == "" || path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token+"\n"), 0o600)
}

// ClearPersistedAgentAuthToken drops only the runtime credential from the
// writable auth volume. The bootstrap registration token remains mounted from
// the chart-managed Secret and is never copied into agent state.
func (c Config) ClearPersistedAgentAuthToken() error {
	path := strings.TrimSpace(c.AgentAuthTokenFile)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0o600)
}

func readTokenFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

func inClusterAPIURL() string {
	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT"))
	if host == "" {
		return ""
	}
	if port == "" {
		port = "443"
	}
	return "https://" + host + ":" + port
}

func getenv(key, fallback string) string {
	if strings.HasPrefix(key, "ENVPILOT_") {
		canonical := "ENVPLANE_" + strings.TrimPrefix(key, "ENVPILOT_")
		if value, set := os.LookupEnv(canonical); set {
			return strings.TrimSpace(value)
		}
	}
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := getenv(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvFloat(key string, fallback float64) float64 {
	value := getenv(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvBool(key string, fallback bool) bool {
	value := getenv(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func legacyDiagnostics() []string {
	seen := map[string]bool{}
	result := []string{}
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(name, "ENVPILOT_") {
			continue
		}
		item := "deprecated:" + name
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func hostname() string {
	value, err := os.Hostname()
	if err != nil {
		return "envpilot-agent"
	}
	return value
}
