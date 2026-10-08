# Candidate storage/network telemetry source closure

Status: candidate private telemetry runtime deployed; no push. Parent-controlled
normal component/ownership rollout and persisted profile activation still needed.

## Source problems

The old DockerCRI source had root NIC counters only and null Pod network; these
must not be relabeled into per-workload traffic. PVC request/status capacity is
not used data. Name-only kubelet volume gauges cannot prove PVC generation, and
node filesystem usage cannot replace PVC data usage. Manual component labels
are lost on Flux reconcile; parent must deploy the durable chart label fix from
normal copied/frozen artifacts, not guess ownership or patch labels again.

## Codex implementation / acceptance

Implement exact owned Pod UID attribution for approved containerd cgroup paths,
opt-in only, with rejection of root counters, contradictory identity, duplicate
interfaces, reset/gap and host-network ambiguity. Add storage.used as measured
GiB-hours plus raw integer usedBytes, using PVC UID and explicit component refs,
never capacity/price substitution. Restrict HTTPS source to exact allowed origins
and approved metrics. If CSI/local-path volume gauges are unsupported, prepare a
separately reviewed RO PVC metadata exporter for at most five approved immutable
refs, GET-only identity verification, no Agent exec/host privileges, no file
contents, symlink escape or generation changes. Keep missing sources/prices and
total infrastructure spend partial/unknown. Tests cover these boundaries.

Parent proof after migration must show exact current UID source series, positive
mysql data usage where expected, at least two acknowledged persisted windows,
and no reliance on node NIC/filesystem totals. GPU remains no_devices where
verified; no hardware or provider invoice claim is introduced.

Implementation and proposed serialized recipe: docs/finops-candidate-telemetry.md.

## Actual target evidence and remaining blockers

2026-10-08 15:24 UTC: finops-private revision 1 deployed, private TLS scrape
cAdvisor UP. Verified kubelet hostname/CA and 512 non-root Pod UID network
series. Three non-root RO PVC exporters are Running; all return 503 because the
five target PVCs have no explicit component labels. This is fail-closed, not a
zero usage reading. Baseline namespace project/environment labels are absent.

Finish through normal workload/ownership configuration; do not manually guess
baseline environment IDs. Activate the persisted RemoteCluster.finops profile
with local API/Agent artifacts, then prove two distinct authenticated ledger
windows with correct new target UIDs. Until then storage.used coverage and full
tenant network ingestion remain unconfirmed; no prices/invoice can be inferred.
