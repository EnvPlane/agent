package agent

import (
	"crypto/sha256"
	"fmt"
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
)

type Config struct {
	ControlPlaneURL    string
	ControlPlaneCAFile string
	RegistrationToken  string
	AgentAuthToken     string
	AgentAuthTokenFile string
	BootstrapProjectID string
	ClusterID          string
	AgentID            string
	AgentNamespace     string
	AgentVersion       string
	KubernetesAPIURL   string
	KubernetesToken    string
	KubernetesCA       string
	NamespaceSelector  string
	Namespaces         []string
	ExcludedNamespaces []string
	ReadSecrets        bool
	FluxNamespace      string
	ResyncInterval     time.Duration
	ReportTimeout      time.Duration
	HeartbeatInterval  time.Duration
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
	return Config{
		ControlPlaneURL:    getenv("ENVPILOT_CONTROL_PLANE_URL", ""),
		ControlPlaneCAFile: getenv("ENVPILOT_CONTROL_PLANE_CA_FILE", ""),
		RegistrationToken:  getenv("ENVPILOT_AGENT_REGISTRATION_TOKEN", ""),
		AgentAuthToken:     agentAuthToken,
		AgentAuthTokenFile: agentAuthTokenFile,
		BootstrapProjectID: getenv("ENVPILOT_BOOTSTRAP_PROJECT_ID", ""),
		ClusterID:          getenv("ENVPILOT_CLUSTER_ID", "default"),
		AgentID:            getenv("ENVPILOT_AGENT_ID", hostname()),
		AgentNamespace:     agentNamespace,
		AgentVersion:       getenv("ENVPILOT_AGENT_VERSION", "dev"),
		KubernetesAPIURL:   getenv("ENVPILOT_KUBERNETES_API_URL", inClusterAPIURL()),
		KubernetesToken:    getenv("ENVPILOT_KUBERNETES_TOKEN_PATH", defaultServiceAccountToken),
		KubernetesCA:       getenv("ENVPILOT_KUBERNETES_CA_PATH", defaultServiceAccountCA),
		// An empty selector intentionally means all namespaces. Do not use
		// getenv here because it treats an explicitly empty environment value as
		// absent and would silently restore the legacy EnvPilot-only selector.
		NamespaceSelector:  strings.TrimSpace(os.Getenv("ENVPILOT_WATCH_NAMESPACE_SELECTOR")),
		Namespaces:         splitCSV(getenv("ENVPILOT_WATCH_NAMESPACES", "")),
		ExcludedNamespaces: excludedNamespaces,
		ReadSecrets:        getenvBool("ENVPILOT_DISCOVERY_READ_SECRETS", false),
		FluxNamespace:      getenv("ENVPILOT_FLUX_NAMESPACE", "flux-system"),
		ResyncInterval:     time.Duration(getenvInt("ENVPILOT_AGENT_RESYNC_SECONDS", 30)) * time.Second,
		ReportTimeout:      time.Duration(getenvInt("ENVPILOT_AGENT_REPORT_TIMEOUT_SECONDS", 10)) * time.Second,
		HeartbeatInterval:  time.Duration(getenvInt("ENVPILOT_AGENT_HEARTBEAT_SECONDS", 30)) * time.Second,
	}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.ControlPlaneURL) == "" {
		return fmt.Errorf("ENVPILOT_CONTROL_PLANE_URL is required")
	}
	if strings.TrimSpace(c.ControlPlaneCAFile) != "" {
		if _, err := NewControlPlaneHTTPClient(c.ReportTimeout, c.ControlPlaneCAFile); err != nil {
			return fmt.Errorf("invalid ENVPILOT_CONTROL_PLANE_CA_FILE: %w", err)
		}
	}
	if strings.TrimSpace(c.ClusterID) == "" {
		return fmt.Errorf("ENVPILOT_CLUSTER_ID is required")
	}
	if strings.TrimSpace(c.AgentID) == "" {
		return fmt.Errorf("ENVPILOT_AGENT_ID is required")
	}
	if strings.TrimSpace(c.RegistrationToken) == "" && strings.TrimSpace(c.AgentAuthToken) == "" {
		return fmt.Errorf("set ENVPILOT_AGENT_REGISTRATION_TOKEN or ENVPILOT_AGENT_AUTH_TOKEN")
	}
	if strings.TrimSpace(c.KubernetesAPIURL) == "" {
		return fmt.Errorf("Kubernetes API URL is required; set ENVPILOT_KUBERNETES_API_URL outside the cluster")
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
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
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
