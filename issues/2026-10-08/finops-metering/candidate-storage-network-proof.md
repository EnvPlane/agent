# Candidate storage/network telemetry source closure

Status: local implementation; actual closure awaits parent-controlled migration
and reviewed source/exporter deployment. No push or operator changes here.

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
