package agent

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func databaseAccessProfileFixture(t *testing.T) DatabaseCredentialEscrowAccessProfileInput {
	t.Helper()
	_, _, _, refs, _ := newEscrowFixture(t, "mysql")
	return DatabaseCredentialEscrowAccessProfileInput{Authorizations: []DatabaseCredentialAuthorization{refs.auth}, EscrowNamespace: "protected-escrow", ServiceAccountNamespace: "agent-system", ServiceAccountName: "project-runtime", KeyRef: "external/key-v1", NamespaceSecretCreateReviewed: true, ProtectedSecrets: []DatabaseCredentialSecretReference{{Namespace: "agent-system", Name: "existing-key"}, {Namespace: "agent-system", Name: "reviewed-bindings"}}}
}

func TestDatabaseEscrowAccessProfileContainsOnlyExactCapabilities(t *testing.T) {
	input := databaseAccessProfileFixture(t)
	input.Authorizations[0].ImportAuthorized = true
	input.Authorizations[0].ImportCredentialSecretName = "verified-input"
	profile, err := BuildDatabaseCredentialEscrowAccessProfile(input)
	if err != nil {
		t.Fatal(err)
	}
	locator, err := DatabaseCredentialEscrowObjectName(input.Authorizations[0].Binding)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Kind != "List" || len(profile.Items) != 4 {
		t.Fatal("expected two exact namespaced Role pairs")
	}
	roles := map[string]DatabaseCredentialRBACManifest{}
	for _, manifest := range profile.Items {
		if manifest.APIVersion != "rbac.authorization.k8s.io/v1" || manifest.Metadata.Annotations["envplane.io/administrator-review-required"] != "true" {
			t.Fatal("missing review marker")
		}
		if manifest.Kind == "Role" {
			roles[manifest.Metadata.Namespace] = manifest
		} else {
			if manifest.Kind != "RoleBinding" || len(manifest.Subjects) != 1 || manifest.Subjects[0] != (DatabaseCredentialRBACSubject{Kind: "ServiceAccount", Name: "project-runtime", Namespace: "agent-system"}) || manifest.RoleRef == nil || manifest.RoleRef.Kind != "Role" || manifest.RoleRef.Name != manifest.Metadata.Name {
				t.Fatal("foreign or cluster-wide subject")
			}
		}
	}
	if !reflect.DeepEqual(roles["target"].Rules, []DatabaseCredentialRBACRule{
		{APIGroups: []string{""}, Resources: []string{"persistentvolumeclaims"}, ResourceNames: []string{"data"}, Verbs: []string{"get", "patch"}},
		{APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{"verified-input"}, Verbs: []string{"get"}},
	}) {
		t.Fatal("target namespace permissions widened")
	}
	if !reflect.DeepEqual(roles["protected-escrow"].Rules, []DatabaseCredentialRBACRule{
		{APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{locator}, Verbs: []string{"get"}},
		{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"create"}},
	}) {
		t.Fatal("escrow permissions widened or names diverged")
	}
	payload, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"existing-key", "reviewed-bindings", input.KeyRef, "ClusterRole", "\"list\"", "\"watch\"", "\"delete\"", "\"update\"", "\"data\":", "\"ownerReferences\""} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatal("sensitive/broad profile output")
		}
	}
}

func TestDatabaseEscrowAccessProfileIsDeterministicAcrossNamespaces(t *testing.T) {
	input := databaseAccessProfileFixture(t)
	second := input.Authorizations[0]
	second.Binding.PVCs = append([]DatabasePVCIdentity(nil), second.Binding.PVCs...)
	second.Binding.EnvironmentID, second.Binding.Namespace, second.Binding.PVCs[0].Namespace = "another-env", "another-target", "another-target"
	second.Binding.PVCs[0].UID = "second-uid"
	input.Authorizations = append(input.Authorizations, second)
	first, err := BuildDatabaseCredentialEscrowAccessProfile(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Authorizations[0], input.Authorizations[1] = input.Authorizations[1], input.Authorizations[0]
	secondProfile, err := BuildDatabaseCredentialEscrowAccessProfile(input)
	if err != nil || !reflect.DeepEqual(first, secondProfile) || len(first.Items) != 6 {
		t.Fatal("namespace sorting/scoping unstable")
	}
	for _, manifest := range first.Items {
		if manifest.Kind != "Role" || manifest.Metadata.Namespace == input.EscrowNamespace {
			continue
		}
		for _, rule := range manifest.Rules {
			if !reflect.DeepEqual(rule.Resources, []string{"persistentvolumeclaims"}) {
				t.Fatal("unapproved import GET granted")
			}
		}
	}
}

func TestDatabaseEscrowAccessProfileFailsClosed(t *testing.T) {
	for _, mode := range []string{"no acknowledgement", "empty key ref", "empty bindings", "mixed cluster", "mixed tenant", "mixed project", "duplicate target", "mixed namespace ownership", "foreign PVC namespace", "wildcard PVC", "invalid uid", "wildcard SA", "escrow is target", "escrow is agent namespace", "malformed namespace", "unapproved import", "protected import", "protected target", "unscoped import", "missing import", "same PVC different locator"} {
		t.Run(mode, func(t *testing.T) {
			input := databaseAccessProfileFixture(t)
			auth := &input.Authorizations[0]
			switch mode {
			case "no acknowledgement":
				input.NamespaceSecretCreateReviewed = false
			case "empty key ref":
				input.KeyRef = ""
			case "empty bindings":
				input.Authorizations = nil
			case "mixed cluster", "mixed tenant", "mixed project", "duplicate target", "mixed namespace ownership", "same PVC different locator":
				second := *auth
				second.Binding.PVCs = append([]DatabasePVCIdentity(nil), auth.Binding.PVCs...)
				if mode == "mixed cluster" {
					second.Binding.ClusterID = "other"
				}
				if mode == "mixed tenant" {
					second.Binding.TenantID = "other"
				}
				if mode == "mixed project" {
					second.Binding.ProjectID = "other"
				}
				if mode == "mixed namespace ownership" {
					second.Binding.EnvironmentID = "other"
					second.Binding.SecretName = "other"
				}
				if mode == "same PVC different locator" {
					second.Binding.ItemID = "other"
					second.Binding.SecretName = "other"
				}
				input.Authorizations = append(input.Authorizations, second)
			case "foreign PVC namespace":
				auth.Binding.PVCs[0].Namespace = "other"
			case "wildcard PVC":
				auth.Binding.PVCs[0].Name = "*"
			case "invalid uid":
				auth.Binding.PVCs[0].UID = "*"
			case "wildcard SA":
				input.ServiceAccountName = "*"
			case "escrow is target":
				input.EscrowNamespace = auth.Binding.Namespace
			case "escrow is agent namespace":
				input.EscrowNamespace = input.ServiceAccountNamespace
			case "malformed namespace":
				auth.Binding.Namespace = "target/foreign"
			case "unapproved import":
				auth.ImportCredentialSecretName = "unreviewed"
			case "protected import", "protected target":
				input.ProtectedSecrets = append(input.ProtectedSecrets, DatabaseCredentialSecretReference{Namespace: "target", Name: "protected-key"})
				if mode == "protected target" {
					auth.Binding.SecretName = "protected-key"
				} else {
					auth.ImportAuthorized = true
					auth.ImportCredentialSecretName = "protected-key"
				}
			case "unscoped import":
				auth.ImportAuthorized = true
				auth.ImportCredentialSecretName = "foreign/name"
			case "missing import":
				auth.ImportAuthorized = true
			}
			profile, err := BuildDatabaseCredentialEscrowAccessProfile(input)
			if !errors.Is(err, ErrDatabaseCredentialAccessProfile) || len(profile.Items) != 0 {
				t.Fatal("invalid profile produced permissions")
			}
		})
	}
}

func TestDatabaseEscrowAccessProfileCannotReadAgentProjectionTrustDomain(t *testing.T) {
	for _, projected := range []string{"existing-key", "reviewed-bindings"} {
		t.Run(projected, func(t *testing.T) {
			input := databaseAccessProfileFixture(t)
			input.ProtectedSecrets = nil // Omission must not widen Pod-namespace access.
			auth := &input.Authorizations[0]
			auth.Binding.Namespace = input.ServiceAccountNamespace
			auth.Binding.PVCs[0].Namespace = input.ServiceAccountNamespace
			auth.ImportAuthorized = true
			auth.ImportCredentialSecretName = projected
			profile, err := BuildDatabaseCredentialEscrowAccessProfile(input)
			if !errors.Is(err, ErrDatabaseCredentialAccessProfile) || len(profile.Items) != 0 {
				t.Fatal("projection trust domain Secret GET granted")
			}
		})
	}
}
