package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedPVCUsageMetadataOnlyAndGenerationGuard(t *testing.T) {
	base := t.TempDir()
	ref := PinnedPVCUsageRef{Namespace: "feature", PVCName: "data", PVCUID: "aabbccdd-1122-3344-5566-778899aabbcc", ComponentID: "mysql"}
	mount := filepath.Join(base, ref.PVCUID)
	if err := os.Mkdir(mount, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "private-database-data"), []byte(strings.Repeat("private-content", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}
	checks := 0
	verify := func(context.Context, PinnedPVCUsageRef) error { checks++; return nil }
	sampler, err := NewPinnedPVCUsageSampler(base, []PinnedPVCUsageRef{ref}, verify)
	if err != nil {
		t.Fatal(err)
	}
	readings, err := sampler.Sample(context.Background())
	if err != nil || len(readings) != 1 || readings[0].AllocatedBytes <= 0 || checks != 2 {
		t.Fatalf("readings=%+v err=%v", readings, err)
	}
	text := PVCUsagePrometheusText(readings)
	if strings.Contains(text, "private-database-data") || strings.Contains(text, "private-content") || !strings.Contains(text, "pvc_uid=") {
		t.Fatal("filesystem content/filename exposed")
	}
	checks = 0
	sampler.verify = func(context.Context, PinnedPVCUsageRef) error {
		checks++
		if checks == 2 {
			return errors.New("changed PVC UID")
		}
		return nil
	}
	if result, err := sampler.Sample(context.Background()); err == nil || result != nil {
		t.Fatal("changed mount generation published")
	}
	if _, err := NewPinnedPVCUsageSampler(base, []PinnedPVCUsageRef{ref, ref}, verify); err == nil {
		t.Fatal("duplicate approved scope")
	}
	if _, err := NewPinnedPVCUsageSampler(base, []PinnedPVCUsageRef{ref}, nil); err == nil {
		t.Fatal("unverified dataset accepted")
	}
}

func TestPinnedPVCUsageNeverFollowsSymlink(t *testing.T) {
	base := t.TempDir()
	ref := PinnedPVCUsageRef{Namespace: "feature", PVCName: "data", PVCUID: "aabbccdd-1122-3344-5566-778899aabbcc", ComponentID: "mysql"}
	mount := filepath.Join(base, ref.PVCUID)
	if err := os.Mkdir(mount, 0o700); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "outside-data"), []byte(strings.Repeat("outside", 100000)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(mount, "escape")); err != nil {
		t.Fatal(err)
	}
	sampler, err := NewPinnedPVCUsageSampler(base, []PinnedPVCUsageRef{ref}, func(context.Context, PinnedPVCUsageRef) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	readings, err := sampler.Sample(context.Background())
	if err != nil || len(readings) != 1 || readings[0].AllocatedBytes >= 700000 {
		t.Fatalf("external symlink target counted: %+v %v", readings, err)
	}
	before := readings[0].AllocatedBytes
	if err := os.WriteFile(filepath.Join(external, "outside-data"), []byte(strings.Repeat("outside", 200000)), 0600); err != nil {
		t.Fatal(err)
	}
	readings, err = sampler.Sample(context.Background())
	if err != nil || readings[0].AllocatedBytes != before {
		t.Fatal("changing external data affected confined gauge", err)
	}
}

func TestFinOpsEnvironmentProviderDoesNotReadFutureCAMount(t *testing.T) {
	env, err := FinOpsTelemetryEnvironment(Config{FinOpsPrometheusEndpoint: "https://metrics.example", FinOpsPrometheusAllowedOrigins: []string{"https://metrics.example"}, FinOpsPrometheusCAFile: "/future/public-trust/ca.crt", FinOpsCadvisorContainerdUIDEnabled: true})
	if err != nil || env["ENVPLANE_FINOPS_CADVISOR_CONTAINERD_UID_ENABLED"] != "true" || env["ENVPLANE_FINOPS_PROMETHEUS_CA_FILE"] != "/future/public-trust/ca.crt" {
		t.Fatal("offline desired-state env provider read future mounted CA", err)
	}
}
