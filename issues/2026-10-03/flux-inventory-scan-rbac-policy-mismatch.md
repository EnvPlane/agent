# Align resource discovery scope with managed Flux RBAC

## Local implementation and verification
Agent now consumes ENVPLANE_DISCOVERY_READ_FLUX, emitted by the chart from
rbac.discovery.readFlux. Disabled optional Flux inventory is explicitly Excluded
in completeness; required workload permission failures still block completeness.
Legacy binary default remains enabled. UI Resource review displays incomplete
coverage and excluded kinds without embedded Kubernetes response bodies.
An opt-in live scanner test through a localhost proxy impersonating app2 Agent
found all ten resources with complete=true and no extra Flux rights. No deployed
image was modified: production end-to-end rescan awaits new Agent/chart/UI builds.

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
The expired app Cloudflare callback was replaced through Settings; webhook
registration and test delivery now report Verified and survive page refresh.
This configuration recovery is not deployment acceptance of the scanner fix.
app2 backend mounts backend-data PVC at /data;
do not ignore it merely to finish review or clone base application data.

## Published source status
Agent implementation commit 88be3f8 is on main; Agent CI, brand guard and image
publication succeeded. Chart 0.2.36 and frontend coverage fixes are committed
in their repositories. Full managed Agent rescan still requires the compatible
release to be installed. Do not mark that live acceptance complete prematurely.
