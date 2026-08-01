# EnvPilot Agent

Cluster-side observer for EnvPilot.

## Scope

- Agent heartbeat.
- Flux status collection.
- Kubernetes event, deployment, resource, and service graph collection.
- Reporting observed cluster state back to the control plane.

## Source Origin

This repository was split from:

- `agent`
- shared `internal/domain`

## Runtime

`apps/agent` provides the standalone `envpilot-agent` binary used by the
container image. It supports `agent` (the default), `agent-install-check`, and
`agent-connectivity-check`. The connectivity check probes only
`/api/v1/health` from the Agent image and never consumes a bootstrap token; the
generated bootstrap instruction uses it before Helm installation.
