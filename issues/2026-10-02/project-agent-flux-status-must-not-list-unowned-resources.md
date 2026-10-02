# Project Agent must not list unowned Flux status resources

## Implementation

Fixed locally: exact-name and environment-scoped collectors already exist, but
startup scope setters recreated a collector after `SetFluxStatusEnabled(false)`.
Both scope setters now preserve disabled observation. Regression coverage uses
the production startup order with empty/named sources and both environment scope
modes, asserting no Flux queries or reports. No RBAC permissions were broadened.
Live verification requires publishing and deploying the updated Agent image.

## Evidence

During the live `app2` Bootstrap rescan on 2026-10-02, the project Agent
successfully reported ten resources but emitted repeated warnings such as:

```
cannot list resource "kustomizations" in API group
"kustomize.toolkit.fluxcd.io" in namespace "flux-system"
```

The Agent was correctly limited to project-owned Flux objects. Kubernetes RBAC
cannot constrain `list` by resource name, so granting it would expose unrelated
Flux workloads.

## Required implementation

Change Flux status collection for project-scoped Agents to fetch known,
project-owned Kustomization and HelmRelease names directly, or skip Flux status
collection when no project-owned source reference exists. Do not add broad
`list` permission in `flux-system`.

## Acceptance checks

- A project-scoped Agent has no Flux `list` permission in `flux-system`.
- Status collection for a known project Flux object succeeds with exact `get`.
- Environments without a project-owned Flux source do not emit recurring RBAC
  warnings.
