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

### Runtime token persistence

The Agent persists its runtime bearer token to
`ENVPLANE_AGENT_AUTH_TOKEN_FILE` after registration so it can restart without
consuming the one-time bootstrap token. The token is stored as plaintext with
file mode `0600` in a directory with mode `0700`. This is an accepted
operational risk, not encryption at rest: an actor able to read the Pod volume,
a PersistentVolume snapshot, or the worker-node filesystem can recover the
token.

Deploy the token path on a dedicated non-shared volume, do not include it in
backups or diagnostic bundles, and restrict privileged Pod and node access.
Control-plane operators should issue short-lived runtime tokens and support
prompt revocation and re-registration to limit exposure after a suspected
volume compromise. Encryption of the token file would require a separately
protected key and is intentionally outside the current Agent credential model.

## Status

Private EnvPlane platform component under active development.
