# Lost generated database credentials: authenticated encrypted escrow

## Implemented locally, not deployed

Generated DB credentials can now be committed to an external escrow **before**
the PVC initialization marker or target Secret is written. AES-256-GCM authenticates
the exact cluster/tenant/project/environment/item/generator/target/PVC UID/volume
binding and key reference. Readback validates committed ciphertext before use;
concurrent creators adopt the committed winner, never overwrite it. Target Secret
creation remains atomic POST. All five supported profiles enforce matching aliases.

The default backend uses authenticated TLS Kubernetes API calls and immutable
encrypted-only Secrets in a separately protected escrow namespace. Encryption
keys are raw 32-byte keys projected from an existing operator-managed Secret,
not created or stored in escrow. No key/password/password-hash enters command,
status, Git or log output. Object locators hash metadata only. Failure details
are sanitized and use the canonical non-retryable recovery classification.

A one-time UID/resourceVersion-conditional PVC annotation records consumption of
initialization permission. Both Secret and escrow loss after this marker cannot
reuse an old initialize authorization to generate a replacement password. Even
an empty/foreign marker fails closed. Exact PVC identity is rechecked before
marking/writing; partial multi-PVC marking is resumable only for the same binding.
There is no atomic multi-object transaction; external restoration/replacement must
remain serialized against provisioning. An orphan PV or data copied to a new UID
needs a separately approved restore mapping, not automatic scope adoption.

## Explicit operator import, not password derivation

If both the live Secret and escrow are missing, automatic recovery fails. A verified
operator backup can be supplied as a separately named Opaque Secret in the exact
approved target namespace. An operator-controlled binding must explicitly set
`importAuthorized: true` and `importCredentialSecretName` to that exact name.
The operator must first authenticate that credential against the retained database
using its normal engine login. Agent does not attest a password merely because a
Secret or dump exists. Import encrypts/read-backs the known credential, preserves
the input Secret and never changes an account, executes SQL, obtains root rights,
uses pod exec, grants roles or weakens authentication.

If no verified password backup exists, plaintext is not derivable from this
implementation. A separately approved engine-native administrative rotation and
verification is required; the resulting known credential may then be explicitly
imported. There is no automatic rotation endpoint or invented root credential.

## Parent integration and Codex implementation prompt

Wire an operator/approved planner to `DatabaseCredentialBindingResolver` using
exact already-observed Bound PVC UIDs/volume names. Fresh initialization requires
explicit authorization for empty data and prepared PVCs **before** credential
materialization; existing pre-PVC provisioning cannot opt into escrow unchanged.
Protected mounted bindings are the implemented adapter, not a replacement for
tenant-scoped approval/audit UI. CP/shared canonical files were not modified.

Provide separate protected key/escrow backups and key version retention. Authorize
only exact target PVC metadata patch/read and exact import Secret GET; encrypted
escrow requires named GET and namespace Secret CREATE. Kubernetes CREATE cannot
be constrained by resourceNames, so isolate it in a dedicated escrow namespace
with no keys or plaintext credential inputs. The chart does not grant this role,
create the namespace or project a key by default. Projected keys need no Agent
Secret API list/watch/write permission. Prevent project runtime from modifying its
operator-controlled key/binding projections. Confirm cluster encryption at rest,
backup access controls and key custody independently.

Cleanup may remove the target credential only after all target namespace PVCs are
gone **and** escrow decrypts to the existing credential. Missing/wrong key or escrow
blocks cleanup; encrypted escrow itself is never deleted by Agent.
Target credential deletion requires its observed Secret UID/resourceVersion;
concurrent replacement cannot be deleted after escrow validation.
External escrow cleanup/key rotation require independent reviewed retention/admin workflows. A
historically mismatched DB account/Secret remains a manual engine recovery case.

Acceptance is local mocked crypto/HTTP/config/lifecycle testing, not live DB
restore or proof of production key custody. No push or live changes permitted.
