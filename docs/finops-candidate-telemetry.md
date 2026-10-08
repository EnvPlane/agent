# Candidate containerd telemetry and reviewed PVC usage exporter

No source freeze, cutover, data migration or routing change is performed here.
The reviewed private telemetry runtime is candidate-only; parent coordinates
management rollout and normal workload/ownership changes.

## Desired-state integration

`FinOpsTelemetryEnvironment(Config)` produces non-secret env desired state for
the normal project-Agent Helm reconciler, including exact HTTPS origin allowlist
and future mounted public CA path. Do not patch Pod env: reconciliation replaces
those patches. Parent must add typed persisted profile/chart schema and mount
the CA bundle to the same configured path before reconciling local artifacts.

Proposed typed chart values (parent must implement/validate; not existing chart
functionality): agent.finops.nodeInventoryEnabled,
agent.finops.cadvisorContainerdUIDEnabled, agent.finops.storageUsedMetric,
agent.finops.prometheus.endpoint/allowedOrigins/caSecretRef/tlsServerName.
Persist these values in the remote project installation profile and render
Deployment env using the provider; no manual Pod env patch or default-image reset.

Update: typed RemoteCluster.finops and Agent Helm settings are now implemented.
Use agent.finops.prometheusEndpoint/allowedOrigins/caSecret/caKey/tlsServerName
(flat Helm keys), not the earlier proposed nested prometheus keys.

- ENVPLANE_FINOPS_NODE_INVENTORY_ENABLED: false default; explicit get/list nodes
  for the specific project Agent when enabled. No nodes/proxy or host grants.
- ENVPLANE_FINOPS_CADVISOR_CONTAINERD_UID_ENABLED: false default; enable only on
  the reviewed candidate source. Exact immutable Pod UID cgroup path matching;
  root/node counters, wrong UID, contradictory namespace and ambiguous series
  are rejected. Never bind node NIC traffic by Pod/application names.
- ENVPLANE_FINOPS_PROMETHEUS_ENDPOINT / ALLOWED_ORIGINS / CA_FILE /
  TLS_SERVER_NAME: exact HTTPS origin, valid CA, no redirects or autodiscovery.
- ENVPLANE_FINOPS_STORAGE_USED_METRIC: fixed allowlist, default
  kubelet_volume_stats_used_bytes; reviewed fallback
  envplane_pvc_directory_allocated_bytes. Not node_filesystem_* or writable
  container-layer bytes. Provider invoice/prices remain unknown.

## Measured storage semantics

Canonical sixth dimension `storage.used`: gib_hours, measured; optional sample
`usedBytes` is the actual integer gauge at the end boundary. Quantity is a sampled
time-integrated estimate, not requested/provisioned capacity or a provider bill.
Kubelet volume gauges require exact namespace/claim/PVC UID labels. Name-only
series cannot prove the generation. Gauge values exceeding PVC status capacity
or ambiguous/missing endpoints are rejected. Unknown remains unavailable.

Read-only candidate metadata preflight on 2026-10-08 found no PVCs at that moment,
storage classes envplane-local-path (envplane.io/local-path) and standard
(k8s.io/minikube-hostpath), and no CSI driver objects. This is not a proof of
volumeStats availability or measured mysql data; repeat after migration.

## Fallback, only after separate runtime/deployment review

NewPinnedPVCUsageSampler accepts at most five explicit namespace/name/immutable
UID/component pins and a mandatory live identity verifier before/after each
scan. Parent must supply the five approved current candidate refs, not stale
source UIDs or chart-name guesses. Mount each approved PVC read-only at
<base>/<PVC UID>; each exporter Pod can mount only claims in its own namespace.
Use no hostPath mounts, privileged mode, exec permission or Agent deployment.
The separate exporter runtime must have GET on only its approved PVC names.

The sampler reads allocated inode-block metadata, never file contents or node
filesystem totals. Hardlinks are counted once; symlink inode blocks are counted
without following referenced targets. Denied metadata,
changed identity, scan limits or unsupported OS fail unavailable. This is a
directory allocated-block estimate and may differ from CSI quota/physical cloud
storage. Deploy as non-root with existing metadata-read permissions; do not
change dataset ownership/modes to make measurement work. No dataset writes.

NewPinnedPVCUsageHandler requires TLS and serves only GET /metrics. Configure
the reviewed server certificate/CA and NetworkPolicy allowing only the dedicated
Prometheus scraper. It never emits cached values or zero on failed verification.
Existing ordinary Agent command does not start this handler. Parent must wire a
reviewed standalone runtime before any live usage proof.

## Serialized deployment/proof recipe for parent

1. Finish authorized freeze/migration and normal app-chart rollout with durable
   explicit component labels (local test-app fix 72eb1b1). Restore copied frozen
   Flux artifacts normally; do not rely on manual labels/env or push a release.
2. Read current five PVC UIDs/components and Pod UIDs on the target generation.
3. Verify candidate cAdvisor actually provides per-Pod counters; root-only or
   null network is an actual source gap, not zero. Dedicated scraper may use
   separately reviewed fine-grained nodes/metrics, never Agent nodes/proxy.
4. Probe actual kubelet_volume_stats_used_bytes with generation-safe PVC mapping.
   If unsupported, separately review/pin/read-only deploy the bounded exporter.
5. Render normal desired-state chart env via the typed provider and immutable
   local Agent artifacts, serialize normal Helm reconcile, then observe at least
   two distinct acknowledged Metrics Server windows and persisted actual samples.
6. For mysql claims prove positive actual measurements against migrated data
   (expected roughly 200 MiB each is a check, not an invented metric). Keep
   storage.used, capacity, GPU no_devices and unknown prices separate in UI.

Do not claim complete storage/network until those operator proofs exist. OpenAPI
and frontend must include storage.used and usedBytes before generating images.

## Actual candidate runtime proof: 2026-10-08 15:24 UTC

The approved finops-private Helm release revision 1 is deployed only on
bethunder-policy-candidate, namespace envplane-telemetry. Three standalone TLS
exporters mount five current immutable PVCs read-only in app-backend,
app2-backend and envplane-pr-e2e-ui-full-652-1007652. UID 999/65532; no fsGroup,
hostPath or exec. Exact PVC GET is allowed; PVC list and Secret GET are denied.
Dedicated scraper: exact node nodes/metrics GET allowed, nodes/proxy denied.

Kubelet serving hostname bethunder-policy-candidate verifies against its pinned
public certificate (Verify return code 0). No insecure TLS flag. Private
Prometheus has TLS, NetworkPolicies, 2-hour/256MB bounded local TSDB; no public
Service/Ingress. cAdvisor scrape is UP, including 512 non-root transmit series
with real Pod UID cgroups and namespace/pod labels. This proves source
availability, NOT authenticated tenant attribution or total cost coverage.

All three PVC scrape targets return HTTP 503: five approved PVCs lack explicit
envplane.io/component labels. Baseline namespaces lack environment/project
bindings. Normal chart/ownership rollout must restore those bindings; never
guess labels manually. Exporters do not emit zero or scan when verification
fails. Measured storage and two persisted dimension ACK windows remain unproved.

Local images, no push: envplane-local/agent:pvc-exporter-ec702cd and
envplane-local/api:finops-profile-16b1200. Parent owns management API rollout,
canonical RemoteCluster schema wiring and normal Save during cutover.

Required finops profile:

```yaml
node_inventory_enabled: true
cadvisor_containerd_uid_enabled: true
prometheus_endpoint: https://finops-prometheus.envplane-telemetry.svc:9090
allowed_origins: [https://finops-prometheus.envplane-telemetry.svc:9090]
storage_used_metric: envplane_pvc_directory_allocated_bytes
tls:
  ca_secret_ref: {name: finops-exporter-ca, key: ca.crt}
```

Public CA Secret envplane-system/finops-exporter-ca exists on candidate only.
For project Agents in other namespaces provision an authorized same-namespace
public CA reference. Opt-in nodes get/list and metrics.k8s.io pod list must be
included in the checked installer profile; no nodes/proxy or exec grants.

## Component normalization and live feature gauges, 15:44 UTC

Agent 907a07c resolves envplane.io/component and app.kubernetes.io/component
consistently: either explicit value is accepted; contradictory labels are
unavailable, with no claim-name guess. Agent fbeb80e counts allocated symlink
inode blocks without following targets; external target growth is excluded.

Private runtime revision 4, image envplane-local/agent:pvc-allocated-fbeb80e:
feature scrape UP. Current backend-data UID bcf325e3-299c-4e01-9b43-1e4123fb5bdc
reports 4096 bytes; mysql-data UID 4ca9499d-9a5b-4f59-8278-110b2744f404 reports
218632192 bytes. These are actual confined directory allocation gauges, not
requested capacity, referenced external data or invoice amounts.

Three baseline PVC gauges still await normal component metadata rollout; their
namespaces have no registered Environment bindings. Do not attribute them to the
feature or invent Environment IDs. Future BaseResourceBinding is separate work.

Bootstrap 6c8160a excludes PVC exporter-marker Pods from application allow
policies and preserves namespace-wide deny-all. Actual feature Flux policy
still had podSelector:{} when reviewed; parent must persist its exclusion before
claiming API-only exporter isolation (NetworkPolicy grants union).

Agent complete tests, focused race tests and lint passed; Bootstrap complete
package tests and lint passed. Normal typed-profile Agent rollout and two tenant
ledger ACK windows remain parent-controlled and are not claimed by source proof.
