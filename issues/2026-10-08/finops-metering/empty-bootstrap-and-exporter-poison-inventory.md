# Empty bootstrap and telemetry Pods invalidate genuine feature inventory

Status: fixed b5053bc; actual feature ledger source closure verified, no push.

After runtime source activation, accepted batch 3c2679e4... reports
no-explicit-owned-inventory for storage.used and network. Candidate feature has
three explicitly owned application Pods and two component-labelled PVCs. A
project-labelled bootstrap namespace has no Environment ID and no Pods/PVCs;
the feature namespace additionally contains a private exporter marker Pod.

Codex fix: verify resources before requiring an Environment identity, so an
empty bootstrap namespace does not invalidate feature inventory. Exclude only
explicit envplane.io/pvc-exporter infrastructure from owned application inventory,
but retain its expected/unallocated CPU coverage count. A spoofed marker must not
produce zero consumption, full coverage or complete spend. Keep invalid/missing component or UID on real owned
workloads fail-closed. Nonempty project resources without owner remain unknown.

Baseline namespaces lack project/environment binding and remain outside feature
ledger. Their actual private gauges are measured but unattributed; do not claim
tenant-wide complete consumption or create phantom Environment IDs.

Acceptance: tests cover empty bootstrap, infrastructure marker, baseline without
ownership, and invalid owned workloads. Parent deploys reviewed local Agent image
normally, then proves two current-UID feature storage/network ledger windows.

Live proof: reviewed ee59d98d... image persisted five qualifying windows; latest
two storage.used complete2/2 and RX/TX complete3/3, exact current feature UID refs,
baseline samples zero. CPU expected4/measured3/unallocated1 remains visible.
Between latest two observed windows, 5.234s is honestly uncovered. See
docs/finops-feature-ledger-live-proof-2026-10-08.md; no prices/full-spend claim.
