# Agent configuration migration (EP-BRAND-003)

Agent runtime configuration accepts canonical `ENVPLANE_*` names and legacy
`ENVPLANE_*` names during the migration window. The canonical name is derived
by replacing the prefix; when both are set, `ENVPLANE_*` wins. Legacy use emits
only a variable-name warning. Values, registration tokens and runtime tokens
are never included in diagnostics.

Examples include `ENVPLANE_CONTROL_PLANE_URL`, `ENVPLANE_CLUSTER_ID`,
`ENVPLANE_AGENT_ID`, `ENVPLANE_AGENT_AUTH_TOKEN_FILE`,
`ENVPLANE_WATCH_NAMESPACES`, `ENVPLANE_DISCOVERY_READ_SECRETS`, and all
connectivity retry settings.

The following remain intentionally stable and are not renamed in place:

- registration/runtime token protocol and control-plane API paths;
- persisted auth-token file format and permissions;
- Kubernetes selectors and `envplane.io/*` labels/annotations used to find
  existing workloads;
- project namespace/resource naming (`envplane-pr-*`) and filesystem defaults.

New Helm values and generated manifests should emit `ENVPLANE_*`. Existing
manifests continue to work through the legacy fallback. Operators can roll
back by removing canonical variables; no token rotation or file migration is
triggered by this alias layer.
