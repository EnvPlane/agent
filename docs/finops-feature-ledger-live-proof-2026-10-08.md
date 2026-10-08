# Actual feature FinOps ledger proof, 2026-10-08

Result: current owned-feature storage/network ingestion confirmed in durable
PostgreSQL, not inferred from raw Prometheus gauges or HTTP submission alone.
No push. Source remains retained/paused; no source data/routing/ownership changes
were performed by this worker.

## Trusted runtime and read scope

Project app, logical cluster bethunder-local, tenant default, Environment
e2e-ui-full-652-1007652, namespace envplane-pr-e2e-ui-full-652-1007652.
Reviewed Agent inventory fix b5053bc is deployed through normal manifest-pinned
image docker.io/envplane-local/agent@sha256:ee59d98d9a83eba418b43743fd9a0550ad70f9d01d13b8d39c3ce3ca6b6704d9.
Runtime chart 0.2.38 supplies HTTPS exact origin, CA/SNI, containerd UID mapping
and the directory-allocated PVC metric. Last read remote generation 7/7 healthy.

Queries run inside BEGIN READ ONLY as envplane_metering with default-tenant RLS
context. Verified role flags SUPERUSER=false and BYPASSRLS=false. No tokens/DSN,
raw request/ledger payloads, dataset contents or credentials were printed. The
evidence scripts report aggregates and allowlisted resource identities only.

## Last two observed persisted windows

| UTC interval | Batch ID | Storage coverage | Network RX/TX coverage | RX bytes | TX bytes |
| --- | --- | --- | --- | ---: | ---: |
| 16:58:21.978--16:59:24 | 45f1a5eb842a4bd8e21bedebe9b695c2302f07fe46f6e132c8f9e1b85ad3633f | complete 2/2 | complete 3/3 each | 13049 | 30417 |
| 16:59:29.234--17:00:23 | acc53005b9dea110452fde845311520bc2a5a09201dbbf27c3f56dd9969e84df | complete 2/2 | complete 3/3 each | 9717 | 24200 |

Both windows have expected CPU Pods=4, measured=3, unallocated/unattributed=1.
The explicit metadata exporter is not silently removed from the denominator or
treated as zero spend. Both reports contain zero baseline namespace samples.

Exact storage gauges in both persisted windows:

| Component | Current PVC UID | Allocated bytes |
| --- | --- | ---: |
| backend | bcf325e3-299c-4e01-9b43-1e4123fb5bdc | 4096 |
| mysql | 4ca9499d-9a5b-4f59-8278-110b2744f404 | 218632192 |

Exact network resource identities matched current Kubernetes Pod inventory:

| Component | Current Pod UID | Earlier RX/TX bytes | Later RX/TX bytes |
| --- | --- | --- | --- |
| backend | 65f0a182-b4b7-4562-930d-4a3aa9bf4aef | 6809 / 7149 | 4561 / 4810 |
| frontend | 8d07da5e-e4af-43b4-b87b-089c4ff4779a | 6240 / 23268 | 5156 / 19390 |
| mysql | ba200576-2293-43a9-89c8-2e0a8c3d6c4f | 0 / 0 | 0 / 0 |

Zero MySQL traffic is an actual measured zero in these intervals, not missing
series. Overall feature RX/TX are positive. No node/root counter attribution.

The stricter SQL check found 5 distinct qualifying windows from
16:55:24.222 through 17:00:23 UTC, requiring all three complete measured dimensions,
positive feature quantities, exact current two PVC UIDs, exact current three Pod
UIDs, and only the owned feature namespace/environment.

## Honest gaps and limitations

Uncovered time between the last two windows is 5.234 seconds. The conservative
Metrics Server intersection windows are not claimed as continuous coverage.
One terminal HTTP 422 appeared at initial post-image startup, 16:55:50.613 UTC;
there were no subsequent delivery errors while fresh windows persisted. This is
consistent with rejecting changed decoration of an already accepted cached
window after restart; the generic receiver response does not identify the exact
validation branch, so no stronger causal assertion is made. No guard was relaxed.

Baseline three PVCs have proven private gauges but no registered Environment/
BaseResourceBinding, and remain explicitly unattributed, never assigned to this
feature. Exporter CPU remains unallocated. Prices/provider invoice remain unknown;
full tenant infrastructure spend, savings and billing completeness are not proven.
Storage is confined directory inode allocation (including symlink inode metadata
without following targets), not capacity, logical referenced datasets or invoice.

Reproduce with finops-feature-ledger-verification.sql using refreshed current
Pod UID variables, and finops-feature-last-two-windows.sql. New windows/UIDs may
advance; the table above records the actual snapshot rather than an ongoing claim.
