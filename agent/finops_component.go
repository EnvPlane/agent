package agent

import (
	"errors"
	"strings"
)

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
