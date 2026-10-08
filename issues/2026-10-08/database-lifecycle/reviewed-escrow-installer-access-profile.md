# Missing installer glue: exact reviewed database escrow access profile

## Local implementation (no grants applied)

`BuildDatabaseCredentialEscrowAccessProfile` accepts typed reviewed authorizations,
dedicated escrow namespace, exact ServiceAccount namespace/name, metadata-only key
reference and optional protected key/binding Secret references. The namespace-wide
CREATE limitation must be explicitly acknowledged with
`NamespaceSecretCreateReviewed: true`. KeyRef is validated but is never emitted as
a Secret name, role, key value or permission.

The helper computes ciphertext names through the same canonical binding locator
used by recovery and returns only namespaced Role/RoleBinding JSON manifests.
It grants exact named PVC GET/PATCH, exact named input Secret GET only for explicit
ImportAuthorized entries, named encrypted escrow GET and isolated namespace Secret
CREATE. No ClusterRole, wildcard, list/watch, key read, escrow update/delete, target
credential CRUD or existing resource ownership/adoption metadata is added.

Input is bounded and must have one cluster/tenant/project. Target namespace groups
must have consistent environment ownership. Duplicate targets/locators, multiple
credential identities sharing a PVC, malformed/wildcard references, unapproved
imports and known protected key/binding names fail closed. Escrow namespace cannot
equal a target namespace or the ServiceAccount Pod namespace; known protected
Secret namespaces also cannot be the ciphertext namespace. Supply exact projected
key/binding Secret references to enable their accidental-import guard. The helper
does not query infrastructure or prove that a namespace is actually dedicated.

Target namespaces must also differ from the ServiceAccount Pod namespace. This
isolated-feature profile cannot grant Secret GET inside the projected key/binding
trust domain, even if ProtectedSecrets were omitted. Other protected key/binding
locations are guarded only when their exact references are supplied; no offline
proof or discovery of unlisted key locations is claimed. Management/in-Pod-namespace
recovery needs a separately reviewed profile, not an exception to this guard.

CLI `cmd/db-credential-recovery-profile` reads a reviewed regular JSON file bounded
to 1 MiB, rejects unknown/duplicate/case-duplicate fields/trailing JSON and emits
only Kubernetes RBAC List JSON. Invalid input yields no stdout, a sanitized error,
and no apply operation. It does not accept/read key or plaintext credential files,
consult Kubernetes, adopt resources or change data. Local tests cover exact rules,
mixed scopes, deterministic ordering, protected inputs and zero sensitive output.

## Remaining administration and Codex implementation prompt

Feed the exported helper from the normal approved installer plan, using current
exact binding metadata and protected projection names. Do not add automatic Helm
RBAC merely because escrow is enabled. Administrator must verify the escrow
namespace is ciphertext-only, existing Role/RoleBinding identity is not foreign,
target ServiceAccount is the reviewed runtime and RBAC delegation is permitted.
Review output before applying through the normal administrator workflow.

Kubernetes Secret CREATE cannot be restricted by resourceNames; PVC PATCH cannot
be field-restricted by RBAC. Named access is therefore not an authorization for
arbitrary metadata/data changes: enforce appropriate admission/broker controls
and keep UID/resourceVersion runtime checks. RBAC names alone do not bind UID or
account/data ownership. Exact-key custody, native pre-PVC initialization sequencing,
approved UID-changing restore mapping and operator account verification remain
separate responsibilities. The CLI has no apply/approval bypass mode.

This closes local profile computation, not production onboarding or live grants.
No push, publish, cluster/Secret/PVC/database change was performed.
