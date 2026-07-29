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
container image. It supports `agent` (the default) and `agent-install-check`,
which matches the Helm chart contract.
