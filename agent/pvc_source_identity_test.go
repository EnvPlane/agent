package agent

import "testing"

func TestPVCSourceIdentitySeparateFromDeployableManifest(t *testing.T) {
	if pvcSourceUID("PersistentVolumeClaim", " source-uid ") != "source-uid" {
		t.Fatal("PVC identity missing")
	}
	if pvcSourceUID("Secret", "source-uid") != "" {
		t.Fatal("unrelated identity added")
	}
	manifest := map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": map[string]any{"name": "data", "namespace": "base", "uid": "source-uid"}}
	safe := sanitizeResourceManifest("PersistentVolumeClaim", manifest, "base", "data")
	if _, found := safe["metadata"].(map[string]any)["uid"]; found {
		t.Fatal("source UID present in deployable manifest")
	}
}
