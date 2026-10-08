package agent

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var ErrDatabaseCredentialAccessProfile = errors.New("invalid reviewed database credential access profile")

type DatabaseCredentialSecretReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// This input is metadata only. KeyRef is validated but never used as a Secret
// name or emitted into RBAC. ProtectedSecrets should include the operator's
// projected key/binding Secrets; they cannot become import GET permissions.
type DatabaseCredentialEscrowAccessProfileInput struct {
	Authorizations                []DatabaseCredentialAuthorization
	EscrowNamespace               string
	ServiceAccountNamespace       string
	ServiceAccountName            string
	KeyRef                        string
	NamespaceSecretCreateReviewed bool
	ProtectedSecrets              []DatabaseCredentialSecretReference
}

type DatabaseCredentialRBACRule struct {
	APIGroups     []string `json:"apiGroups"`
	Resources     []string `json:"resources"`
	ResourceNames []string `json:"resourceNames,omitempty"`
	Verbs         []string `json:"verbs"`
}
type DatabaseCredentialRBACMetadata struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Annotations map[string]string `json:"annotations,omitempty"`
}
type DatabaseCredentialRBACSubject struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}
type DatabaseCredentialRBACRoleRef struct {
	APIGroup string `json:"apiGroup"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}
type DatabaseCredentialRBACManifest struct {
	APIVersion string                          `json:"apiVersion"`
	Kind       string                          `json:"kind"`
	Metadata   DatabaseCredentialRBACMetadata  `json:"metadata"`
	Rules      []DatabaseCredentialRBACRule    `json:"rules,omitempty"`
	Subjects   []DatabaseCredentialRBACSubject `json:"subjects,omitempty"`
	RoleRef    *DatabaseCredentialRBACRoleRef  `json:"roleRef,omitempty"`
}
type DatabaseCredentialEscrowAccessProfile struct {
	APIVersion string                           `json:"apiVersion"`
	Kind       string                           `json:"kind"`
	Items      []DatabaseCredentialRBACManifest `json:"items"`
}

var databaseProfileDNSLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var databaseProfileDNSSubdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
var databaseProfileSecretKey = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func databaseProfileName(value string, namespace bool) bool {
	limit := 253
	pattern := databaseProfileDNSSubdomain
	if namespace {
		limit, pattern = 63, databaseProfileDNSLabel
	}
	if len(value) == 0 || len(value) > limit || !pattern.MatchString(value) {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || !databaseProfileDNSLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func databaseProfileMetadataID(value string) bool {
	if value == "" || len(value) > 512 || value != strings.TrimSpace(value) || strings.ContainsAny(value, "*?/\\") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func databaseProfileKeyRef(value string) bool {
	if value == "" || len(value) > 512 || value != strings.TrimSpace(value) || strings.ContainsAny(value, "*?") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func databaseProfileSortedNames(names map[string]bool) []string {
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// BuildDatabaseCredentialEscrowAccessProfile computes review-only namespaced
// Roles/RoleBindings. It performs no reads/writes/apply, ownership adoption,
// key lookup, Secret listing or plaintext credential handling. Namespace-wide
// CREATE is isolated and explicitly marked for administrator review because
// Kubernetes cannot restrict CREATE using resourceNames.
func BuildDatabaseCredentialEscrowAccessProfile(input DatabaseCredentialEscrowAccessProfileInput) (DatabaseCredentialEscrowAccessProfile, error) {
	invalid := func() (DatabaseCredentialEscrowAccessProfile, error) {
		return DatabaseCredentialEscrowAccessProfile{}, ErrDatabaseCredentialAccessProfile
	}
	if len(input.Authorizations) == 0 || len(input.Authorizations) > 128 || len(input.ProtectedSecrets) > 256 || !databaseProfileName(input.EscrowNamespace, true) || !databaseProfileName(input.ServiceAccountNamespace, true) || !databaseProfileName(input.ServiceAccountName, false) || input.EscrowNamespace == input.ServiceAccountNamespace || !databaseProfileKeyRef(input.KeyRef) || !input.NamespaceSecretCreateReviewed {
		return invalid()
	}
	protected := map[string]bool{}
	for _, ref := range input.ProtectedSecrets {
		if !databaseProfileName(ref.Namespace, true) || !databaseProfileName(ref.Name, false) || ref.Namespace == input.EscrowNamespace {
			return invalid()
		}
		protected[ref.Namespace+"/"+ref.Name] = true
	}
	type namespaceAccess struct {
		pvcs, imports map[string]bool
		environment   string
	}
	namespaces := map[string]*namespaceAccess{}
	escrowNames := map[string]bool{}
	seenTargets := map[string]bool{}
	pvcBindings := map[string]string{}
	first := input.Authorizations[0].Binding
	for _, auth := range input.Authorizations {
		binding := auth.Binding
		profileName, generatorKey := generatedSecretProfile(binding.Generator)
		if profileName+":"+generatorKey != binding.Generator || len(generatorKey) > 253 || !databaseProfileSecretKey.MatchString(generatorKey) {
			return invalid()
		}
		if binding.ClusterID != first.ClusterID || binding.TenantID != first.TenantID || binding.ProjectID != first.ProjectID || binding.Namespace == input.EscrowNamespace || binding.Namespace == input.ServiceAccountNamespace || !databaseProfileName(binding.Namespace, true) || !databaseProfileName(binding.SecretName, false) {
			return invalid()
		}
		for _, value := range []string{binding.ClusterID, binding.TenantID, binding.ProjectID, binding.EnvironmentID, binding.ItemID} {
			if !databaseProfileMetadataID(value) {
				return invalid()
			}
		}
		locator, err := DatabaseCredentialEscrowObjectName(binding)
		if err != nil || escrowNames[locator] || seenTargets[binding.Namespace+"/"+binding.SecretName] || protected[binding.Namespace+"/"+binding.SecretName] {
			return invalid()
		}
		escrowNames[locator], seenTargets[binding.Namespace+"/"+binding.SecretName] = true, true
		access := namespaces[binding.Namespace]
		if access == nil {
			access = &namespaceAccess{pvcs: map[string]bool{}, imports: map[string]bool{}, environment: binding.EnvironmentID}
			namespaces[binding.Namespace] = access
		}
		if access.environment != binding.EnvironmentID {
			return invalid()
		}
		for _, pvc := range binding.PVCs {
			if !databaseProfileName(pvc.Name, false) || !databaseProfileName(pvc.VolumeName, false) || !databaseProfileMetadataID(pvc.UID) {
				return invalid()
			}
			pvcKey := pvc.Namespace + "/" + pvc.Name
			if previous := pvcBindings[pvcKey]; previous != "" && previous != locator {
				return invalid()
			}
			pvcBindings[pvcKey], access.pvcs[pvc.Name] = locator, true
		}
		if auth.ImportAuthorized {
			name := auth.ImportCredentialSecretName
			if !databaseProfileName(name, false) || protected[binding.Namespace+"/"+name] {
				return invalid()
			}
			access.imports[name] = true
		} else if auth.ImportCredentialSecretName != "" {
			return invalid()
		}
	}
	profile := DatabaseCredentialEscrowAccessProfile{APIVersion: "v1", Kind: "List"}
	appendRole := func(namespace string, rules []DatabaseCredentialRBACRule, escrow bool) {
		ruleJSON, _ := json.Marshal(rules)
		identity := strings.Join([]string{first.ClusterID, first.TenantID, first.ProjectID, input.ServiceAccountNamespace, input.ServiceAccountName, namespace, string(ruleJSON)}, "\x00")
		name := "envplane-db-recovery-" + strings.TrimPrefix(digestText(identity), "sha256:")[:32]
		annotations := map[string]string{"envplane.io/administrator-review-required": "true"}
		if escrow {
			annotations["envplane.io/namespace-secret-create"] = "dedicated-namespace-approval-required"
		}
		metadata := DatabaseCredentialRBACMetadata{Name: name, Namespace: namespace, Annotations: annotations}
		profile.Items = append(profile.Items,
			DatabaseCredentialRBACManifest{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role", Metadata: metadata, Rules: rules},
			DatabaseCredentialRBACManifest{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding", Metadata: metadata, Subjects: []DatabaseCredentialRBACSubject{{Kind: "ServiceAccount", Name: input.ServiceAccountName, Namespace: input.ServiceAccountNamespace}}, RoleRef: &DatabaseCredentialRBACRoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: name}},
		)
	}
	orderedNamespaces := make([]string, 0, len(namespaces))
	for namespace := range namespaces {
		orderedNamespaces = append(orderedNamespaces, namespace)
	}
	sort.Strings(orderedNamespaces)
	for _, namespace := range orderedNamespaces {
		access := namespaces[namespace]
		rules := []DatabaseCredentialRBACRule{{APIGroups: []string{""}, Resources: []string{"persistentvolumeclaims"}, ResourceNames: databaseProfileSortedNames(access.pvcs), Verbs: []string{"get", "patch"}}}
		if len(access.imports) > 0 {
			rules = append(rules, DatabaseCredentialRBACRule{APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: databaseProfileSortedNames(access.imports), Verbs: []string{"get"}})
		}
		appendRole(namespace, rules, false)
	}
	appendRole(input.EscrowNamespace, []DatabaseCredentialRBACRule{
		{APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: databaseProfileSortedNames(escrowNames), Verbs: []string{"get"}},
		{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"create"}},
	}, true)
	return profile, nil
}
