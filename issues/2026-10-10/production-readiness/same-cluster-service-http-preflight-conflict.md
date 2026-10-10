# Same-cluster project Agent fails management preflight for valid Service HTTP

Priority: P1 zero-setup blocker. Status: locally fixed; published/runtime retest pending.

## Reproduction

Umbrella 0.4.693 in clean kind-envplane-readiness-682. Create test-app targeting management-cluster; validate customer application/GitOps repositories and save project settings. Project-owned Agent/Runner releases reconcile; Pods Running. Bootstrap step 3 stays degraded: management endpoint preflight failed: insecure_transport. Agent uses http://envplane-control-plane.envplane.svc:8080 with sameCluster mode, while configuration validation explicitly permits that Service DNS HTTP endpoint. Installed Agent source 6e7dea6d46892f39986111cdce25df2675a62f76.

## Root cause and local correction

ProbeManagementEndpoint has an unconditional HTTP rejection when AllowInsecureControlPlane is false, unlike ValidateControlPlaneEndpointWithPolicy's existing same-cluster Service DNS exception. Local patch delegates the HTTP rejection decision to the existing validator. No new endpoint mode, no blanket insecure opt-in, no TLS-verification disable, no credential change. Configured external/remote HTTP remains rejected. Transport policy acceptance does not mark TLS/runtime authentication as successful; actual health/auth probes still run.

Regression table covers .svc, .svc.cluster.local, default same-cluster mode, external/remote HTTP and suffix spoofing. Cancelled-context probes ensure passing classification never claims connectivity, runtime auth or TLS. Existing authenticated probe tests retained. Race suite passed locally; released Agent still blocks live Bootstrap.

## Codex implementation prompt

Review/release the local Agent fix; retain remote HTTPS/CA/runtime identity checks and unit/real HTTP probe regressions. Repeat customer project same-cluster startup and verify authenticated heartbeat/capabilities -> namespace scan -> Compile -> lifecycle without ENVPLANE_ALLOW_INSECURE_CONTROL_PLANE=true. Repeat remote target with normal HTTPS and fail-closed negative tests. Never manually edit heartbeat/preflight status, rotate credentials to hide transport mismatch, or claim local tests as released acceptance.
