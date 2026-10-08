package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPVCComponentBindingSupportsChartLabelsWithoutNamespaceOwnerGuess(t *testing.T) {
	const uid = "2f520e00-52f4-412f-be97-a4570ce45e97"
	for _, conflict := range []bool{false, true} {
		labels := map[string]string{"app.kubernetes.io/component": "backend"}
		if conflict {
			labels["envplane.io/component"] = "mysql"
		}
		h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pvc := map[string]any{
				"metadata": map[string]any{"name": "arbitrary-claim", "uid": uid, "creationTimestamp": time.Now().Add(-time.Hour), "labels": labels},
				"spec":     map[string]any{"volumeName": "bound-volume"},
				"status":   map[string]any{"phase": "Bound", "capacity": map[string]string{"storage": "1Gi"}},
			}
			switch r.URL.Path {
			case "/api/v1/namespaces":
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "feature", "labels": map[string]string{"envplane.io/project-id": "p", environmentIDLabel: "e"}}}}})
			case "/api/v1/namespaces/feature/persistentvolumeclaims":
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{pvc}})
			case "/api/v1/namespaces/feature/persistentvolumeclaims/arbitrary-claim":
				_ = json.NewEncoder(w).Encode(pvc)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		s := &KubernetesNamespaceSource{apiURL: h.URL, client: h.Client()}
		// Exporter identity needs no namespace/environment label: only reviewed
		// claim UID and consistent explicit component metadata.
		err := s.VerifyPinnedPVCUsageRef(context.Background(), PinnedPVCUsageRef{Namespace: "feature", PVCName: "arbitrary-claim", PVCUID: uid, ComponentID: "backend"})
		if (err != nil) != conflict {
			t.Fatalf("verifier conflict=%v err=%v", conflict, err)
		}
		owned, err := s.FinOpsOwnedPVCInventory(context.Background(), "p", time.Now().Add(-time.Minute))
		if conflict && err == nil {
			t.Fatal("conflicting inventory accepted")
		}
		if !conflict && (err != nil || len(owned) != 1 || owned[0].ComponentID != "backend") {
			t.Fatalf("chart labels not supported: %+v %v", owned, err)
		}
		h.Close()
	}
}
