# EnvPlane Agent

Kubernetes cluster agent package.

Responsibilities:

- namespace watch
- deployment and pod readiness collection
- Kubernetes event collection
- FluxCD status collection
- status reporting to the control plane

## Namespace identity migration

Set `ENVPLANE_REQUIRE_ENVIRONMENT_LABEL=true` for new installations. In this
mode, the Agent reports a namespace only when it carries the control-plane
owned `envplane.io/environment-id` label. The legacy `envplane-pr-<id>` name
fallback remains available with a warning for existing installations through
2026-12-31; migrate namespaces to the label before that deprecation window
ends.
