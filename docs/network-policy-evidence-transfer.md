# Authenticated NetworkPolicy evidence transfer

The operator probe can submit measured evidence directly; it never imports an
old JSON report. First the authenticated Agent obtains a one-time server lease,
then performs the probe/cleanup, then submits its result over verified HTTPS.
Offline mode remains available and does not update API readiness.

## Prerequisites

- Deploy compatible contracts, control-plane and Agent builds before using this
  mode. Publish contracts and update every direct consumer before release CI.
- Use the existing registered Agent runtime token, bound to this exact project,
  Agent and remote cluster. Do not use a bootstrap, admin, Stripe or issuer key.
- The management endpoint must have a certificate trusted by the host. HTTP,
  insecure TLS and redirects are rejected. No insecure-development exception.
- Grant only explicitly approved diagnostic permissions for temporary namespaces,
  Pods, NetworkPolicies and exec. Ordinary discovery never creates probe resources.
- PostgreSQL is required for multi-replica API deployments. JSON stores are
  single-process development storage, not a shared-file distributed lock.

## Run

From the Agent repository:

```sh
go run ./apps/networkpolicy-probe \
  --context YOUR_TARGET_CONTEXT \
  --authorize-test-resources \
  --control-plane-url https://YOUR_MANAGEMENT_ENDPOINT \
  --agent-token-file /absolute/private/runtime-agent-token \
  --project-id YOUR_PROJECT_ID \
  --cluster-id YOUR_REMOTE_CLUSTER_ID \
  --agent-id YOUR_REGISTERED_AGENT_ID
```

The server chooses the current target configuration generation; do not pass an
arbitrary generation when submitting. Secrets stay in the file/header, not argv
values, logs or output. The CLI prints only the safe report. Exit0 means passed
and stored; exit1 means failed/unknown measurement stored; exit2 means invalid
arguments or transfer failed. Never interpret a local file as persisted evidence.

## Protocol and durability

1. POST `/api/v1/agents/network-policy/probes/challenge`: runtime bearer only,
   project/cluster/Agent identity, observed target namespace UID. Server derives
   tenant and generation; one active ten-minute lease per binding.
2. Run a new single-node Pod IPv4 TCP probe with positive/negative controls and
   ownership-safe cleanup. No claims about UDP, IPv6, host networking or all nodes.
3. POST `/api/v1/agents/network-policy/probes/result`: same bearer, nonce/lease ID
   and typed result. Nonce hash, authentication binding and report are checked;
   consumption plus persistence is one row-locked transaction. Raw nonce/bearer
   and kubectl output are never stored. Replays return409, storage failure503.
4. GET `/api/v1/projects/{id}/network-policy-probe`: ordinary tenant/project
   authorization; returns only report, server receive time and derived freshness.
   Fifteen-minute stale evidence, changed generation/Agent credentials or expired
   runtime authentication yields state=unknown and fresh=false, not passed.

The hidden Bootstrap state is not returned through generic Bootstrap reads and
cannot be modified by public Bootstrap PATCH. Issuing a new lease invalidates
the prior current observation until the next result is stored. If the challenge
response is lost, an unchanged active binding cannot issue another lease until
expiry; this bounded failure does not make isolation green. An acknowledgement
lost after commit is still durable; replays cannot replace that observation.

## Trust and recovery boundaries

Reports originate from an authenticated Agent principal. Initial cluster UID is
Agent-observed and pinned within a configuration generation, not independent
hardware/CNI attestation. A compromised Agent credential can lie; rotate it and
repeat the probe. These results do not broaden runtime RBAC, install CNI, or
alter working application policies. Cleanup failure remains unknown.

Rollback by stopping submissions; previous consumers ignore additive endpoints.
Do not manually edit nonce hashes or synthesize passed reports. If CNI is not
enforcing, follow the administrator-approved network migration ticket and rerun
the diagnostic after repair.
