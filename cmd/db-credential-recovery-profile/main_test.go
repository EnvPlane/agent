package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clusteragent "github.com/envplane/agent/agent"
)

func reviewedCLIFixture(t *testing.T) ([]string, string) {
	t.Helper()
	auth := clusteragent.DatabaseCredentialAuthorization{Binding: clusteragent.DatabaseCredentialBinding{ClusterID: "cluster", TenantID: "tenant", ProjectID: "project", EnvironmentID: "env", ItemID: "db", Generator: "mysql-password-v1:DB_PASSWORD", Namespace: "feature-target", SecretName: "backend-secret", PVCs: []clusteragent.DatabasePVCIdentity{{Namespace: "feature-target", Name: "database-data", UID: "pvc-uid", VolumeName: "pv-data"}}}, ImportAuthorized: true, ImportCredentialSecretName: "verified-input"}
	payload, err := json.Marshal([]clusteragent.DatabaseCredentialAuthorization{auth})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "reviewed-bindings.json")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	return []string{"--reviewed-bindings", path, "--escrow-namespace", "protected-escrow", "--service-account-namespace", "agent-system", "--service-account", "project-runtime", "--key-ref", "external/key-version-metadata", "--acknowledge-namespace-secret-create", "--protected-secret", "agent-system/existing-key", "--protected-secret", "agent-system/reviewed-bindings"}, path
}

func TestRecoveryProfileCLIEmitsOnlyRBACAndNoKeyMaterial(t *testing.T) {
	args, _ := reviewedCLIFixture(t)
	var stdout bytes.Buffer
	if err := run(args, &stdout); err != nil {
		t.Fatal(err)
	}
	var profile clusteragent.DatabaseCredentialEscrowAccessProfile
	if json.Unmarshal(stdout.Bytes(), &profile) != nil || profile.Kind != "List" || len(profile.Items) != 4 {
		t.Fatal("invalid RBAC JSON")
	}
	for _, item := range profile.Items {
		if item.Kind != "Role" && item.Kind != "RoleBinding" {
			t.Fatal("non-RBAC resource emitted")
		}
	}
	for _, forbidden := range []string{"external/key-version-metadata", "existing-key", "reviewed-bindings", "ClusterRole", "\"data\":", "\"list\"", "\"watch\"", "\"delete\"", "\"update\""} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatal("unexpected key/sensitive/broad output")
		}
	}
}

func TestRecoveryProfileCLIRejectsUnreviewedOrSensitiveInputWithoutOutput(t *testing.T) {
	for _, mode := range []string{"plaintext key", "password field", "duplicate field", "case duplicate field", "trailing JSON", "oversized", "unknown flag", "apply flag", "no create acknowledgement", "no key ref", "escrow overlaps Pod namespace", "target overlaps Pod namespace", "directory"} {
		t.Run(mode, func(t *testing.T) {
			args, path := reviewedCLIFixture(t)
			sentinel := "plaintext-sentinel-never-emit"
			switch mode {
			case "target overlaps Pod namespace":
				payload, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				payload = []byte(strings.ReplaceAll(string(payload), "feature-target", "agent-system"))
				if err := os.WriteFile(path, payload, 0600); err != nil {
					t.Fatal(err)
				}
			case "duplicate field", "case duplicate field":
				payload, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				key := "importAuthorized"
				if mode == "case duplicate field" {
					key = "ImportAuthorized"
				}
				payload = []byte(strings.Replace(string(payload), `"importAuthorized":true`, `"importAuthorized":false,"`+key+`":true`, 1))
				if err := os.WriteFile(path, payload, 0600); err != nil {
					t.Fatal(err)
				}
			case "plaintext key":
				if err := os.WriteFile(path, []byte(sentinel), 0600); err != nil {
					t.Fatal(err)
				}
			case "password field":
				if err := os.WriteFile(path, []byte(`[{"password":"`+sentinel+`"}]`), 0600); err != nil {
					t.Fatal(err)
				}
			case "trailing JSON":
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.WriteString(`{}`)
				_ = file.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, bytes.Repeat([]byte{' '}, (1<<20)+1), 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown flag":
				args = append(args, "--api-key", sentinel)
			case "apply flag":
				args = append(args, "--apply")
			case "no create acknowledgement":
				args = append(args, "--acknowledge-namespace-secret-create=false")
			case "no key ref":
				args = append(args, "--key-ref", "")
			case "escrow overlaps Pod namespace":
				args = append(args, "--escrow-namespace", "agent-system")
			case "directory":
				args = append(args, "--reviewed-bindings", filepath.Dir(path))
			}
			var stdout bytes.Buffer
			err := run(args, &stdout)
			if err == nil || stdout.Len() != 0 || strings.Contains(err.Error(), sentinel) {
				t.Fatal("invalid input emitted information/permissions")
			}
		})
	}
}
