# Project Agent live explicit-scope scan remains unexecuted

Status: OPEN coverage prerequisite, not a reproduced product defect. Candidate campaign: umbrella 0.4.682.

Fresh race test execution passes 383 tests/subtests but skips TestResourceScannerLiveExplicitFluxScope because ENVPLANE_TEST_SCAN_PROXY_URL is absent. The fixture requires a local Kubernetes proxy impersonating the actual project Agent and an explicit ENVPLANE_TEST_SCAN_NAMESPACES allowlist. The clean candidate has no configured project Agent yet because OAuth onboarding is pending. Passing mock scope tests cannot establish actual permission sufficiency or prove that excluded Flux resources are not accessed.

Implementation/verification prompt for Codex: after clean onboarding creates a project executor, run the opt-in scanner fixture through a localhost-only proxy impersonating exactly that executor ServiceAccount, with a named disposable base namespace. Verify the actual runtime RBAC and expected snapshot/completeness results; never substitute cluster-admin reads for project-Agent permissions. Include permitted and forbidden namespace checks, the explicit Flux opt-out, project IDs other than app/app2, and sanitized evidence. Do not grant additional privileges simply to make the test pass; file a separate defect if the generated profile is insufficient.

Acceptance: live fixture executes without skip on the frozen/new candidate, authorized inventory is complete, no out-of-scope reads succeed, and the proxy is stopped afterwards. No working application or Secret data copied into test logs.
