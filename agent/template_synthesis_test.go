package agent

import (
	"testing"
	"time"

	"github.com/envplane/contracts/domain"
)

func TestSynthesizeEnvironmentTemplateIsDeterministicAndFailClosed(t *testing.T) {
	snapshots := []domain.ResourceSnapshot{
		{Kind: "Service", Namespace: "base-a", Name: "api", Manifest: map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": "api", "namespace": "base-a"}}},
		{Kind: "PersistentVolumeClaim", Namespace: "base-a", Name: "data"},
		{Kind: "ConfigMap", Namespace: "shared", Name: "platform", Labels: map[string]string{"envplane.io/shared": "true"}},
	}
	graph := BuildServiceGraph(snapshots)
	input := domain.TemplateSynthesisInput{TenantID: "tenant-a", ProjectID: "project-a", ClusterID: "cluster-a", SourceScanID: "scan-1", SourceNamespaces: []string{"base-a", "shared"}, Now: time.Unix(10, 0).UTC()}
	first, err := SynthesizeEnvironmentTemplate(input, snapshots, graph)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SynthesizeEnvironmentTemplate(input, snapshots, graph)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.Revision.Digest != second.Revision.Digest {
		t.Fatal("synthesis is not deterministic")
	}
	if first.AutonomousApply {
		t.Fatal("PVC/shared resource must block autonomous apply")
	}
	if len(first.Unresolved) == 0 {
		t.Fatal("expected unresolved decisions")
	}
	if len(first.Revision.Resources) != 1 || first.Revision.Resources[0].Kind != "Service" {
		t.Fatalf("unexpected portable resources: %#v", first.Revision.Resources)
	}
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
}
