package agent

import (
	"github.com/envplane/contracts/domain"
	"testing"
)

func TestNamespaceCleanupReportsBothFinalizerLocations(t *testing.T) {
	namespace := Namespace{Metadata: NamespaceMetadata{Name: "preview", Labels: map[string]string{environmentIDLabel: "preview"}, Finalizers: []string{"example.com/cleanup"}}, Status: NamespaceStatus{Phase: "Terminating"}}
	namespace.Spec.Finalizers = []string{"kubernetes"}
	report, ok, _ := buildNamespaceStatusReport("MODIFIED", namespace, true)
	if !ok || report.Status != domain.StatusTerminating || report.NamespaceCleanup == nil || len(report.NamespaceCleanup.Finalizers) != 2 {
		t.Fatalf("missing finalizer evidence: %#v", report)
	}
	namespace.Status.Phase = "Active"
	report, _, _ = buildNamespaceStatusReport("MODIFIED", namespace, true)
	if report.NamespaceCleanup != nil {
		t.Fatal("active namespace reported cleanup blockers")
	}
}
