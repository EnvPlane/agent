package agent

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var finOpsKubernetesUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func quoteFinOpsLabel(value string) string { return strconv.Quote(value) }

// Containerd attribution matches an exact API-owned Pod UID, never pod names,
// application labels, node/root cgroups, or a container-name prefix.
func finOpsContainerdPath(owner FinOpsOwnedResource) string {
	if !finOpsKubernetesUID.MatchString(owner.ResourceUID) {
		return ""
	}
	uid := regexp.QuoteMeta(owner.ResourceUID)
	systemdUID := regexp.QuoteMeta(strings.ReplaceAll(owner.ResourceUID, "-", "_"))
	return `(?:.*/pod` + uid + `(?:/[a-f0-9]{64})?|.*/kubepods(?:-[a-z]+)*-pod` + systemdUID + `\.slice(?:/cri-containerd-[a-f0-9]{64}\.scope)?)`
}

func (s *FinOpsPrometheusSource) networkSelector(metric string, owner FinOpsOwnedResource) string {
	base := metric + "{namespace=" + quoteFinOpsLabel(owner.Namespace) + ",pod=" + quoteFinOpsLabel(owner.PodName) + ",pod_uid=" + quoteFinOpsLabel(owner.ResourceUID) + "}"
	pattern := finOpsContainerdPath(owner)
	if !s.containerdUID || pattern == "" {
		return base
	}
	return "(" + base + " or " + metric + "{id=~" + quoteFinOpsLabel(pattern) + "})"
}

func (s *FinOpsPrometheusSource) networkResetQuery(metric string, owner FinOpsOwnedResource, seconds int) string {
	base := metric + "{namespace=" + quoteFinOpsLabel(owner.Namespace) + ",pod=" + quoteFinOpsLabel(owner.PodName) + ",pod_uid=" + quoteFinOpsLabel(owner.ResourceUID) + "}"
	expression := "resets(" + base + "[" + strconv.Itoa(seconds) + "s])"
	if pattern := finOpsContainerdPath(owner); s.containerdUID && pattern != "" {
		expression += " or resets(" + metric + "{id=~" + quoteFinOpsLabel(pattern) + "}[" + strconv.Itoa(seconds) + "s])"
	}
	return expression
}

func (s *FinOpsPrometheusSource) normalizeNetworkIdentity(labels map[string]string, owners map[string]FinOpsOwnedResource) (map[string]string, error) {
	if labels["id"] == "/" || labels["id"] == "/kubepods" || labels["id"] == "/kubepods.slice" {
		return nil, errors.New("node/root traffic has no workload ownership")
	}
	uid := labels["pod_uid"]
	if uid == "" && s.containerdUID {
		for candidate, owner := range owners {
			pattern := finOpsContainerdPath(owner)
			if pattern != "" && regexp.MustCompile("^"+pattern+"$").MatchString(labels["id"]) {
				if uid != "" {
					return nil, errors.New("ambiguous cgroup ownership")
				}
				uid = candidate
			}
		}
	}
	owner, ok := owners[uid]
	if !ok {
		return nil, errors.New("network workload UID unavailable")
	}
	if (labels["namespace"] != "" && labels["namespace"] != owner.Namespace) || (labels["pod"] != "" && labels["pod"] != owner.PodName) {
		return nil, errors.New("conflicting network ownership")
	}
	if labels["pod_uid"] != "" && (labels["namespace"] == "" || labels["pod"] == "") {
		return nil, errors.New("incomplete direct UID ownership")
	}
	result := map[string]string{}
	for key, value := range labels {
		result[key] = value
	}
	result["pod_uid"] = uid
	result["namespace"] = owner.Namespace
	result["pod"] = owner.PodName
	return result, nil
}
