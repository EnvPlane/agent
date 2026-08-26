# Management Agent lacks Events RBAC in feature namespaces

## Observed

The chart-managed management Agent can observe a feature namespace but receives
`403 Forbidden` while listing Kubernetes `events` there.

## Impact

Environment diagnostics omit warning events and repeatedly log collection
errors, even when workloads and Flux resources are otherwise readable.

## Required fix

Grant the management Agent read-only `get`, `list` and `watch` access to core
`events` in the explicitly managed feature namespaces. Do not grant write
permissions or widen access outside the managed namespace set.

## Verification

Run an environment reconciliation and confirm the Agent reports events without
RBAC errors.
