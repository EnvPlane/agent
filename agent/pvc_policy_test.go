package agent

import (
	"strings"
	"testing"

	"github.com/envplane/contracts/domain"
)

func TestPVCPolicyRequiresExplicitEmptyStorageChoice(t *testing.T) {
	graph := BuildServiceGraph([]domain.ResourceSnapshot{{Kind: "PersistentVolumeClaim", Namespace: "base", Name: "data"}})
	if len(graph.Policies) != 1 || graph.Policies[0].Strategy != domain.ResourcePolicyUnsupported || !graph.Policies[0].Required {
		t.Fatalf("PVC policy must remain fail-closed: %#v", graph.Policies)
	}
	if !strings.Contains(graph.Policies[0].Reason, "empty feature storage") || strings.Contains(graph.Policies[0].Reason, "EP-TPL-006") {
		t.Fatalf("policy must explain supported recovery: %s", graph.Policies[0].Reason)
	}
	if graph.Validation == nil || graph.Validation.Valid {
		t.Fatal("PVC without explicit strategy must still block compilation")
	}
}
