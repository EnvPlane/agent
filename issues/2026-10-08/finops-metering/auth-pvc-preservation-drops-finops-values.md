# Auth PVC preservation deletes persisted FinOps Helm settings

Status: diagnosed live; parent owns CP fix; worker makes no core/cache/schema edits.

RemoteCluster default/bethunder-local has desired/generation/observed 4/4/4,
healthy, and persisted private Prometheus endpoint. Both Agents use the reviewed
local image digest, but Helm agent.finops is null and Deployment only contains
node inventory env. New ledger batches exist, yet storage.used and network are
source-unconfigured; qualifying complete feature windows remain zero.

Root cause: control-plane/internal/server/remote_cluster_reconciler.go,
copyRemoteAuthPersistence, agent branch replaces values["agent"] with a new
authPersistence-only map. The later helper runs after remoteFinOpsReleaseValues
and discards persisted desired FinOps configuration.

Codex parent implementation: retain existing Agent map (create only if nil),
assign only authPersistence, preserve finops and unrelated desired settings.
Add regression test for legacy auth PVC retention plus FinOps profile preservation.
Rebuild API and reconcile normally; no manual Pod env or source changes.

Acceptance: Helm values retain private endpoint/origin/CA, both runtime templates
render FinOps settings, then narrow-role default-tenant ledger shows two distinct
feature windows with positive storage.used plus network RX/TX and exact current
feature PVC UIDs. Baseline three remain unattributed, unknown prices stay unknown.
