# PVC discovery policy describes an obsolete implementation blocker

## Evidence
On 0.4.577 Resource review claims PVC materialization is deferred to EP-TPL-006,
although bootstrap already renders an explicit mock PVC as new feature storage.

## Implementation prompt and fix
Explain the available explicit empty-storage strategy in the policy reason.
Keep unsupported/defaulted and strategy_required until the user selects a
strategy: never silently clone base data or treat an absent choice as approval.
The regression verifies both actionable wording and the fail-closed policy.

## Live verification
The operator approved mock only for app2/backend-data. Saving this selection
does not create or modify a base PVC. Generator hardening is tracked separately
in bootstrap/issues/2026-10-03/mock-pvc-must-not-restore-source-data.md.
