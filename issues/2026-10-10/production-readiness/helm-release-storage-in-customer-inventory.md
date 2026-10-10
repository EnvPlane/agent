# Helm release storage appears as an included application Secret

Priority: P2. Status: fixed and live scan verified on umbrella 0.4.698.

## Observed reproduction

On umbrella 0.4.696, authenticated test-app scan of test-app-base completed with 14 resources. Resource review included sh.helm.release.v1.test-app-base.v1 and v2 as checked Secret/reference entries. These are Helm release history, not customer workload dependencies. No Secret values were inspected or copied; no template was compiled.

## Correction and acceptance

Exclude Secret objects with the exact Kubernetes type helm.sh/release.v1 at Agent discovery, before sanitization/snapshot publication. Do not filter by name alone: Opaque application Secrets or registry credentials must remain visible, even with similarly named metadata. The backend-secret dependency remains discoverable. No live source Secret deletion or data migration is required.

Live acceptance on 2026-10-10: project Agent automatically reconciled to b8d0af0 / sha256:90bf2b81b653b48fb47574b0d32cc3fca668ea2e59bd399b3f8c4fd7645ab103. Normal UI rescan attempt 2 (scan-ec4af5a53cfe857b589aa149b965f352) completed with 12 resources instead of 14. Resource review contains backend-secret and no Helm release storage entries. Source application/PVC state unchanged. Compile, generated credentials and lifecycle are not covered by this acceptance.

## Codex implementation prompt

Review the local scanner exclusion, retain application Secret discovery and value redaction, and add a full scanner fixture proving Helm storage is absent while backend-secret remains. Publish the Agent correction and repeat the selected namespace scan on the candidate. Verify the live inventory excludes release history without deleting the source objects. Do not mark compile, DB credential materialization or lifecycle as passed from a scan alone.
