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
RemoteCluster reconciliation runs that same check as an init-container from the
target Pod before registration. For API-managed remote targets, operators must
not install this chart or pass tokens manually: the management control plane
selects the signed chart/image compatibility set and mounts a one-time Secret.
See the [remote-cluster guide](https://github.com/envpilot/deploy/blob/main/docs/remote-clusters.md).
