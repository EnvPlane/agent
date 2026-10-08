package agent

import (
	"context"
	"errors"
	"testing"
)

type policyProbeFake struct {
	policy          string
	enforce         bool
	baselineFail    bool
	execFail        bool
	cleanupFail     bool
	identityChanged bool
	cleaned         bool
}

func (f *policyProbeFake) Prepare(context.Context) (string, error)     { return "cluster-a", nil }
func (f *policyProbeFake) SetPolicy(_ context.Context, p string) error { f.policy = p; return nil }
func (f *policyProbeFake) Connect(_ context.Context, client string) (bool, error) {
	if f.execFail {
		return false, errors.New("exec permission denied")
	}
	if f.baselineFail {
		return false, nil
	}
	if !f.enforce {
		return true, nil
	}
	if f.policy == "ingress-deny" || (f.policy == "ingress-allow" && client == "denied") || (f.policy == "egress-deny" && client == "denied") {
		return false, nil
	}
	return true, nil
}
func (f *policyProbeFake) ClusterUID(context.Context) (string, error) {
	if f.identityChanged {
		return "cluster-b", nil
	}
	return "cluster-a", nil
}
func (f *policyProbeFake) Cleanup(context.Context) error {
	f.cleaned = true
	if f.cleanupFail {
		return errors.New("cleanup failed")
	}
	return nil
}
func TestNetworkPolicyProbeCannotInferEnforcementFromAPI(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fake  policyProbeFake
		state string
	}{
		{"enforcing", policyProbeFake{enforce: true}, "passed"},
		{"ignored", policyProbeFake{}, "failed"},
		{"baseline failure", policyProbeFake{baselineFail: true}, "unknown"},
		{"exec denial", policyProbeFake{execFail: true}, "unknown"},
		{"cleanup failure", policyProbeFake{enforce: true, cleanupFail: true}, "unknown"},
		{"identity changed", policyProbeFake{enforce: true, identityChanged: true}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := RunNetworkPolicyProbe(context.Background(), 1, &tc.fake)
			if r.State != tc.state || !tc.fake.cleaned {
				t.Fatalf("report=%+v cleaned=%v", r, tc.fake.cleaned)
			}
		})
	}
}

func TestCanceledPolicyProbeCannotPass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &policyProbeFake{enforce: true}
	report := RunNetworkPolicyProbe(ctx, 1, fake)
	if report.State == "passed" || !fake.cleaned {
		t.Fatalf("report=%+v", report)
	}
}
