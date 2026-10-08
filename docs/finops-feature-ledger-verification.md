# Read-only feature ledger ACK verification

Use the accompanying SQL through an authorized operator PostgreSQL connection.
It opens a READ ONLY transaction, switches to existing envplane_metering (no
SUPERUSER/BYPASSRLS), sets default-tenant RLS context, and emits aggregates only.
No passwords/DSN, raw payload, application content, migrations or DB writes.

Qualifying evidence requires at least two distinct persisted project app windows
with complete measured storage.used, network.receive and network.transmit reports,
positive measured quantities and only e2e-ui-full-652-1007652 namespace ownership.
Storage requires both exact current feature PVC UIDs and positive usedBytes.
Baseline app-backend/app2-backend samples must remain zero in these feature reports.
Baseline private gauges do not create registered ledger ownership.

Before parent rollout, baseline query returned 58 tenant batches; qualifying
dimension windows 0. Latest app ledger period ended 2026-10-08T14:57:47Z with
network source-unconfigured and no storage.used. This is pre-rollout state, not
evidence of failure in the upgraded collector.

Do not treat a Ready Pod, saved profile, Prometheus source gauge, emitted HTTP
request or successful cache install as a persisted ACK. Verify new runtime image,
configured private endpoint, current UID binding and SQL windows after rollout.
Report actual coverage reasons; no missing dimensions or unknown prices become zero.

## Post-image-rollout observation

Both Agents now run reviewed local digest 865434c7c638d6b8e83a7eec7edb4f57d1084a5f0fecc74f5217ee9866492483.
RemoteCluster default/bethunder-local is healthy, desired/generation/observed 4/4/4,
with private endpoint persisted. Aggregate batch count advanced to 60, including
new app windows 16:30:23.186--16:31:26 and 16:31:28.766--16:32:19 UTC. Their storage.used
and network reports are source-unconfigured; qualifying complete windows are 0.

Helm safe values inspection shows agent.finops null. The CP
copyRemoteAuthPersistence Agent branch replaces the whole Agent values map after
desired FinOps values were built. Parent owns merging authPersistence into the
existing map and normal reconciliation. Do not infer a collector transport or
source error until the configured endpoint actually appears in runtime env.
