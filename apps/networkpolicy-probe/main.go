// Command networkpolicy-probe is an explicitly authorized operator diagnostic.
// It never runs during ordinary Agent discovery and never replaces a CNI.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	clusteragent "github.com/envplane/agent/agent"
	"github.com/envplane/contracts/domain"
)

var errProbe = errors.New("probe operation unavailable")

type kubectlDriver struct{ target, namespace, owner, ip, node string }

func (d *kubectlDriver) command(ctx context.Context, input any, args ...string) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	args = append([]string{"--context", d.target, "--request-timeout=10s"}, args...)
	cmd := exec.CommandContext(bounded, "kubectl", args...)
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return nil, errProbe
		}
		cmd.Stdin = bytes.NewReader(data)
	}
	data, err := cmd.CombinedOutput()
	return data, err
}

func (d *kubectlDriver) ClusterUID(ctx context.Context) (string, error) {
	data, err := d.command(ctx, nil, "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}")
	if err != nil || len(data) == 0 {
		return "", errProbe
	}
	return strings.TrimSpace(string(data)), nil
}

func (d *kubectlDriver) pod(name, role, node string) map[string]any {
	command := []string{"sleep", "300"}
	if role == "server" {
		command = []string{"sh", "-c", "mkdir -p /tmp/www; printf 'probe-ok\\n' > /tmp/www/index.html; exec httpd -f -p 8080 -h /tmp/www"}
	}
	security := map[string]any{"runAsUser": 1000, "runAsNonRoot": true, "allowPrivilegeEscalation": false, "capabilities": map[string]any{"drop": []string{"ALL"}}, "seccompProfile": map[string]string{"type": "RuntimeDefault"}}
	spec := map[string]any{"automountServiceAccountToken": false, "terminationGracePeriodSeconds": 1, "containers": []any{map[string]any{"name": "probe", "image": "busybox:1.37", "command": command, "securityContext": security, "resources": map[string]any{"requests": map[string]string{"cpu": "5m", "memory": "8Mi"}, "limits": map[string]string{"cpu": "50m", "memory": "32Mi"}}}}}
	if node != "" {
		spec["nodeName"] = node
	}
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name, "namespace": d.namespace, "labels": map[string]string{"role": role}}, "spec": spec}
}

func (d *kubectlDriver) Prepare(ctx context.Context) (string, error) {
	uid, err := d.ClusterUID(ctx)
	if err != nil {
		return "", err
	}
	ns := map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": d.namespace, "labels": map[string]string{"envplane.io/probe-id": d.owner}}}
	if _, err = d.command(ctx, ns, "create", "-f", "-"); err != nil {
		return "", errProbe
	}
	if _, err = d.command(ctx, d.pod("server", "server", ""), "create", "-f", "-"); err != nil {
		return "", errProbe
	}
	if _, err = d.command(ctx, nil, "-n", d.namespace, "wait", "--for=condition=Ready", "pod/server", "--timeout=45s"); err != nil {
		return "", errProbe
	}
	data, err := d.command(ctx, nil, "-n", d.namespace, "get", "pod", "server", "-o", "json")
	var pod struct {
		Spec struct {
			NodeName string `json:"nodeName"`
		} `json:"spec"`
		Status struct {
			PodIP string `json:"podIP"`
		} `json:"status"`
	}
	if err != nil || json.Unmarshal(data, &pod) != nil || net.ParseIP(pod.Status.PodIP).To4() == nil || pod.Spec.NodeName == "" {
		return "", errProbe
	}
	d.ip, d.node = pod.Status.PodIP, pod.Spec.NodeName
	for _, client := range []string{"allowed", "denied"} {
		if _, err = d.command(ctx, d.pod(client, client, d.node), "create", "-f", "-"); err != nil {
			return "", errProbe
		}
	}
	if _, err = d.command(ctx, nil, "-n", d.namespace, "wait", "--for=condition=Ready", "pod", "--all", "--timeout=45s"); err != nil {
		return "", errProbe
	}
	return uid, nil
}

func (d *kubectlDriver) SetPolicy(ctx context.Context, stage string) error {
	if _, err := d.command(ctx, nil, "-n", d.namespace, "delete", "networkpolicy", "probe-boundary", "--ignore-not-found", "--wait=true"); err != nil {
		return errProbe
	}
	if stage != "none" {
		selector := map[string]any{"matchLabels": map[string]string{"role": "server"}}
		spec := map[string]any{"podSelector": selector, "policyTypes": []string{"Ingress"}, "ingress": []any{}}
		switch stage {
		case "ingress-deny":
		case "ingress-allow":
			spec["ingress"] = []any{map[string]any{"from": []any{map[string]any{"podSelector": map[string]any{"matchLabels": map[string]string{"role": "allowed"}}}}, "ports": []any{map[string]any{"protocol": "TCP", "port": 8080}}}}
		case "egress-deny":
			spec = map[string]any{"podSelector": map[string]any{"matchLabels": map[string]string{"role": "denied"}}, "policyTypes": []string{"Egress"}, "egress": []any{}}
		default:
			return errProbe
		}
		policy := map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": map[string]string{"name": "probe-boundary", "namespace": d.namespace}, "spec": spec}
		if _, err := d.command(ctx, policy, "apply", "-f", "-"); err != nil {
			return errProbe
		}
	}
	// Policy convergence is asynchronous. Retried fresh connections provide
	// negative evidence only after a bounded settling period and positive controls.
	select {
	case <-ctx.Done():
		return errProbe
	case <-time.After(5 * time.Second):
		return nil
	}
}

func (d *kubectlDriver) Connect(ctx context.Context, client string) (bool, error) {
	data, err := d.command(ctx, nil, "-n", d.namespace, "exec", client, "--", "wget", "-T", "3", "-qO-", "http://"+d.ip+":8080/")
	if err == nil {
		if strings.TrimSpace(string(data)) != "probe-ok" {
			return false, errProbe
		}
		return true, nil
	}
	// Never interpret generic exec failures (RBAC/image/missing Pod) as denial.
	if strings.Contains(string(data), "wget: download timed out") && ctx.Err() == nil {
		return false, nil
	}
	return false, errProbe
}

func (d *kubectlDriver) Cleanup(ctx context.Context) error {
	data, err := d.command(ctx, nil, "get", "namespace", d.namespace, "--ignore-not-found", "-o", "json")
	if err != nil {
		return errProbe
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var ns struct {
		Metadata struct {
			UID    string            `json:"uid"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if json.Unmarshal(data, &ns) != nil || ns.Metadata.Labels["envplane.io/probe-id"] != d.owner || ns.Metadata.UID == "" {
		return errProbe
	}
	// DeleteOptions UID precondition prevents deleting a replacement namespace.
	options := map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]string{"uid": ns.Metadata.UID}}
	if _, err = d.command(ctx, options, "delete", "--raw", "/api/v1/namespaces/"+d.namespace, "-f", "-"); err != nil {
		return errProbe
	}
	if _, err = d.command(ctx, nil, "wait", "--for=delete", "namespace/"+d.namespace, "--timeout=45s"); err != nil {
		return errProbe
	}
	return nil
}

func main() {
	target := flag.String("context", "", "Explicit kube context")
	authorized := flag.Bool("authorize-test-resources", false, "Authorize isolated temporary namespace, pods, policies and exec")
	generation := flag.Int64("generation", 0, "Observed configuration generation (required)")
	apiURL := flag.String("control-plane-url", "", "HTTPS control plane for authenticated submission")
	tokenFile := flag.String("agent-token-file", "", "Private file containing the runtime Agent token")
	projectID := flag.String("project-id", "", "Bound project identity")
	clusterID := flag.String("cluster-id", "", "Bound remote cluster identity")
	agentID := flag.String("agent-id", "", "Bound Agent identity")
	flag.Parse()
	if !*authorized || *target == "" || (*generation <= 0 && *apiURL == "") {
		_, _ = os.Stderr.WriteString("Explicit context, generation and test-resource authorization are required.\n")
		os.Exit(2)
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		os.Exit(2)
	}
	owner := hex.EncodeToString(random[:])
	driver := &kubectlDriver{target: *target, namespace: "envplane-netpol-probe-" + owner, owner: owner}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var transport *clusteragent.NetworkPolicyProbeTransport
	var challenge domain.NetworkPolicyProbeChallenge
	var expectedUID string
	if *apiURL != "" {
		data, err := os.ReadFile(*tokenFile)
		if err != nil {
			os.Exit(2)
		}
		transport, err = clusteragent.NewNetworkPolicyProbeTransport(*apiURL, string(data), domain.NetworkPolicyProbeIdentity{ProjectID: *projectID, ClusterID: *clusterID, AgentID: *agentID}, nil)
		if err != nil {
			os.Exit(2)
		}
		uid, err := driver.ClusterUID(ctx)
		if err != nil {
			os.Exit(2)
		}
		challenge, err = transport.Challenge(ctx, uid)
		if err != nil {
			os.Exit(2)
		}
		*generation = challenge.Generation
		expectedUID = uid
	}
	report := clusteragent.RunNetworkPolicyProbe(ctx, *generation, driver)
	if transport != nil {
		if report.ClusterUID == "" && report.State == "unknown" {
			report.ClusterUID = expectedUID
		}
		if transport.Submit(ctx, challenge, report) != nil {
			_, _ = os.Stderr.WriteString("Evidence transfer failed; no token or response body was printed.\n")
			os.Exit(2)
		}
	}
	if json.NewEncoder(os.Stdout).Encode(report) != nil {
		os.Exit(2)
	}
	if report.State != "passed" {
		os.Exit(1)
	}
}
