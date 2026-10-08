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
