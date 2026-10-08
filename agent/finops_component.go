package agent

import (
	"errors"
	"strings"
)

// The separately isolated metadata exporter is not an application workload.
// This explicit operator marker also drives its NetworkPolicy separation;
// missing application labels alone never justify excluding arbitrary Pods.
// Marked Pods remain in expected/unallocated CPU coverage; this marker is not
// authority for zero consumption or complete tenant spending.
func finOpsTelemetryPod(labels map[string]string) bool {
	_, marked := labels["envplane.io/pvc-exporter"]
	return marked
}

// FinOpsComponentID accepts explicit chart/runtime labels, never resource names.
// Both label spellings must agree when present; neither has precedence.
func FinOpsComponentID(labels map[string]string) (string, error) {
	platform, chart := labels["envplane.io/component"], labels["app.kubernetes.io/component"]
	if platform != "" && chart != "" && platform != chart {
		return "", errors.New("contradictory component labels")
	}
	component := platform
	if component == "" {
		component = chart
	}
	if component == "" || strings.TrimSpace(component) != component {
		return "", errors.New("explicit component label required")
	}
	return component, nil
}
