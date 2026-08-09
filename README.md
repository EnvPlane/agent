# EnvPlane Agent

Cluster-side observability and reporting agent for [EnvPlane](https://envplane.dev).
It watches a target Kubernetes cluster, collects operational state, and reports
bounded observations to the EnvPlane control plane.

## Responsibilities

- Maintain connectivity and heartbeat status.
- Collect Kubernetes events, deployments, resources, service relationships, and Flux CD state.
- Discover service environments and deployment capabilities.
- Report observations without exposing raw cluster credentials or secrets.

## Runtime

The container supports `agent`, `agent-install-check`, and
`agent-connectivity-check`. The connectivity check validates the control-plane
health endpoint without consuming a bootstrap token. API-managed remote-cluster
installation and rotation are controlled by the control plane.

## Development

```bash
go test ./...
go build ./...
docker build -t envplane-agent:dev .
```

## Related components

- [Control Plane](https://github.com/EnvPlane/control-plane)
- [Contracts](https://github.com/EnvPlane/contracts)
- [Deploy](https://github.com/EnvPlane/deploy)

## Security

Do not commit kubeconfigs, bootstrap tokens, cloud credentials, or production
values. Use short-lived credentials and managed Kubernetes Secrets.

## Status

Private EnvPlane platform component under active development.
