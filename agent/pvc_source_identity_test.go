package agent

import "testing"

func TestPVCSourceIdentitySeparateFromDeployableManifest(t *testing.T) {
	if pvcSourceUID("PersistentVolumeClaim", " source-uid ") != "source-uid" {
		t.Fatal("PVC identity missing")
	}
	for _, kind := range []string{"Secret", "StatefulSet", "Deployment"} {
		if pvcSourceUID(kind, " source-uid ") != "source-uid" {
			t.Fatalf("database source identity missing for %s", kind)
		}
	}
	if pvcSourceUID("ConfigMap", "source-uid") != "" {
		t.Fatal("unrelated source identity added")
	}
	manifest := map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": map[string]any{"name": "data", "namespace": "base", "uid": "source-uid"}}
	safe := sanitizeResourceManifest("PersistentVolumeClaim", manifest, "base", "data")
	if _, found := safe["metadata"].(map[string]any)["uid"]; found {
		t.Fatal("source UID present in deployable manifest")
	}
}
