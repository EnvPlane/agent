package agent

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// FinOpsTelemetryEnvironment is non-secret desired state for the parent Helm
// reconciler, not a manual Pod patch. CA is a mounted public-trust file path.
func FinOpsTelemetryEnvironment(cfg Config) (map[string]string, error) {
	if cfg.FinOpsPrometheusEndpoint != "" {
		if _, err := NewFinOpsPrometheusSource(cfg.FinOpsPrometheusEndpoint, cfg.FinOpsPrometheusAllowedOrigins, http.DefaultClient); err != nil {
			return nil, err
		}
	}
	metric := cfg.FinOpsStorageUsedMetric
	if metric == "" {
		metric = "kubelet_volume_stats_used_bytes"
	}
	if metric != "kubelet_volume_stats_used_bytes" && metric != "envplane_pvc_directory_allocated_bytes" {
		return nil, errors.New("unapproved storage-used metric")
	}
	for _, origin := range cfg.FinOpsPrometheusAllowedOrigins {
		if strings.Contains(origin, ",") {
			return nil, errors.New("invalid metrics origin list")
		}
	}
	return map[string]string{
		"ENVPLANE_FINOPS_NODE_INVENTORY_ENABLED":          strconv.FormatBool(cfg.FinOpsNodeInventoryEnabled),
		"ENVPLANE_FINOPS_STORAGE_USED_METRIC":             metric,
		"ENVPLANE_FINOPS_CADVISOR_CONTAINERD_UID_ENABLED": strconv.FormatBool(cfg.FinOpsCadvisorContainerdUIDEnabled),
		"ENVPLANE_FINOPS_PROMETHEUS_ENDPOINT":             cfg.FinOpsPrometheusEndpoint,
		"ENVPLANE_FINOPS_PROMETHEUS_ALLOWED_ORIGINS":      strings.Join(cfg.FinOpsPrometheusAllowedOrigins, ","),
		"ENVPLANE_FINOPS_PROMETHEUS_CA_FILE":              cfg.FinOpsPrometheusCAFile,
		"ENVPLANE_FINOPS_PROMETHEUS_TLS_SERVER_NAME":      cfg.FinOpsPrometheusTLSServerName,
	}, nil
}
