package agent

import (
	"context"
	"time"
)

// NetworkPolicyProbeReport is operator evidence, not an API-discovery claim.
// It proves only the stated scope; importing it into runtime readiness requires
// authenticated cluster identity/generation binding at the control plane.
type NetworkPolicyProbeReport struct {
	SchemaVersion   int       `json:"schemaVersion"`
	ClusterUID      string    `json:"clusterUID"`
	Generation      int64     `json:"generation"`
	CheckedAt       time.Time `json:"checkedAt"`
	Scope           string    `json:"scope"`
	Ingress         string    `json:"ingress"`
	Egress          string    `json:"egress"`
	State           string    `json:"state"`
	Reason          string    `json:"reason"`
	CleanupComplete bool      `json:"cleanupComplete"`
}

// ProbeDriver owns temporary resources and distinguishes a confirmed network
// timeout from exec/RBAC/image/controller failures. Raw output never reaches a report.
type NetworkPolicyProbeDriver interface {
	Prepare(context.Context) (string, error)
	SetPolicy(context.Context, string) error
	Connect(context.Context, string) (bool, error)
	ClusterUID(context.Context) (string, error)
	Cleanup(context.Context) error
}

func RunNetworkPolicyProbe(ctx context.Context, generation int64, driver NetworkPolicyProbeDriver) (report NetworkPolicyProbeReport) {
	report = NetworkPolicyProbeReport{SchemaVersion: 1, Generation: generation, CheckedAt: time.Now().UTC(), Scope: "single-node-pod-ipv4-tcp", Ingress: "unknown", Egress: "unknown", State: "unknown", Reason: "probe_unavailable"}
	if generation <= 0 || driver == nil {
		return report
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		report.CleanupComplete = driver.Cleanup(cleanupCtx) == nil
		if !report.CleanupComplete {
			report.State, report.Reason = "unknown", "cleanup_failed"
		}
		report.CheckedAt = time.Now().UTC()
	}()
	uid, err := driver.Prepare(ctx)
	if err != nil || uid == "" {
		return report
	}
	report.ClusterUID = uid
	connect := func(client string, want bool) bool {
		got, err := driver.Connect(ctx, client)
		return err == nil && got == want
	}
	if !connect("allowed", true) || !connect("denied", true) {
		report.Reason = "baseline_failed"
		return report
	}
	if driver.SetPolicy(ctx, "ingress-deny") != nil {
		return report
	}
	ingress := "passed"
	for i := 0; i < 3; i++ {
		got, err := driver.Connect(ctx, "allowed")
		if err != nil {
			return report
		}
		if got {
			ingress = "failed"
		}
	}
	if driver.SetPolicy(ctx, "ingress-allow") != nil || !connect("allowed", true) {
		return report
	}
	got, err := driver.Connect(ctx, "denied")
	if err != nil {
		return report
	}
	if got {
		ingress = "failed"
	}
	if driver.SetPolicy(ctx, "egress-deny") != nil || !connect("allowed", true) {
		return report
	}
	egress := "passed"
	for i := 0; i < 3; i++ {
		got, err := driver.Connect(ctx, "denied")
		if err != nil {
			return report
		}
		if got {
			egress = "failed"
		}
	}
	if driver.SetPolicy(ctx, "none") != nil || !connect("denied", true) {
		report.Reason = "recovery_failed"
		return report
	}
	currentUID, err := driver.ClusterUID(ctx)
	if err != nil || currentUID != uid || ctx.Err() != nil {
		report.Reason = "cluster_identity_changed"
		return report
	}
	report.Ingress, report.Egress = ingress, egress
	report.State, report.Reason = "passed", "measured"
	if ingress != "passed" || egress != "passed" {
		report.State, report.Reason = "failed", "policy_not_enforced"
	}
	return report
}
