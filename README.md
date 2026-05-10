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

## Runtime Note

In the monorepo, the agent runtime entrypoint is currently embedded in `apps/api/main.go` behind the `agent` command. This repository contains the agent package and should get a dedicated `cmd/envpilot-agent` binary as the next extraction step.
