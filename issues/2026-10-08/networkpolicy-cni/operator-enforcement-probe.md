# Explicit NetworkPolicy traffic probe

Status: implemented locally. Scope: single-node non-hostNetwork IPv4 TCP only.

Ordinary discovery adds networkPolicy.enforcement=unknown and performs no writes.
The operator CLI requires explicit context, generation and --authorize-test-resources.
It creates a random, owned temporary namespace, three tiny non-root Pods without
ServiceAccount tokens/PVCs, and only local probe policies. It tests baseline,
ingress deny, selective ingress allow, separate egress deny and recovery. Clients
are placed on the server node. Only wget's explicit network timeout counts as
blocking; exec/RBAC/image failures yield unknown. Cleanup runs even after errors,
checks ownership, uses a namespace UID delete precondition and waits for absence.

From the Agent checkout:

    go run ./apps/networkpolicy-probe --context CLUSTER --generation GENERATION --authorize-test-resources

Exit0: measured passed with cleanup. Exit1: failed/unknown with safe JSON. Exit2:
invalid authorization/arguments. Raw kubectl output/credentials are never printed.
No CNI changes. The output is operator evidence, not an automatic readiness update.

Live repetition on 2026-10-08: envplane ingress/egress passed; bethunder-local
ingress/egress failed; both cleanupComplete=true. Unit tests cover ignored policies,
baseline/exec failure, changed identity, cleanup failure and cancellation.

## Codex follow-up prompt

Complete authenticated, tenant-bound report transport and durable freshness/
generation checks from control-plane's onboarding-enforcement-capability-probe
ticket. Do not trust arbitrary uploaded JSON, widen Agent RBAC automatically,
run this write-capable probe during discovery, or replace a CNI implicitly.
Add cross-namespace, Service/DNS and cross-node coverage before expanding the scope.
