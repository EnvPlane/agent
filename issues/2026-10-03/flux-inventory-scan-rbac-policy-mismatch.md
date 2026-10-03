# Align resource discovery scope with managed Flux RBAC

## Reproduction
On umbrella 0.4.573, app2's completed scan reports ten resources but completeness
is false. Both app2-backend and app2-frontend report forbidden LIST requests for
GitRepository, HelmRelease and Kustomization. Managed chart values explicitly
set rbac.discovery.readFlux=false. The project Agent has only exact-name access
to project source objects in flux-system.

## Impact
Deployment readiness remains blocked by incomplete desired-state inventory.
Resource review can be completed without exposing the actual scan permission
failures on that page; the readiness summary only says inventory is incomplete.

## Implementation prompt
Align scanner scope, chart RBAC and completeness requirements. Declare whether
Flux CRs are in the selected inventory scope. Do not silently convert forbidden
required reads into a complete inventory. Preserve exact-name access in shared
flux-system; do not grant namespace-wide LIST there. If Flux discovery is
required in explicitly selected base namespaces, request/display that additional
scope before granting it. Otherwise explicitly report out-of-scope kinds.
Expose per-namespace completeness failures and an actionable recovery step in UI.
Test readFlux=false, exact source GET, selected-base Flux discovery, and denied
required kinds. Repeat a live rescan and compile-readiness check.

## Related configuration blockers
app webhook still uses an expired previous Cloudflare callback; re-registration
and delivery proof are needed. app2 backend mounts backend-data PVC at /data;
do not ignore it merely to finish review or clone base application data.
