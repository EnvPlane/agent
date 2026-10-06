# Remove unnecessary HelmRelease list from environment Flux observation

Status: implemented; full Agent tests and targeted race test passed; published artifact retest pending.

Project Agent on 0.4.623 had exact get permission for its feature Kustomization but no HelmRelease list permission. The environment-scoped collector fetched the Kustomization successfully, then failed the entire report on a needless HelmRelease list. Kubernetes events still reached UI, while Flux remained pending.

Environment-scoped manifest deployments are health-checked by their exact Kustomization. The collector now reports that resource without querying HelmRelease. Legacy/project collection retains its existing Helm behavior. No permissions were widened.

## Codex follow-up prompt

Verify the next umbrella selects this Agent source revision. Exercise a feature Kustomization transitioning failed to ready with HelmRelease list forbidden, and confirm authenticated scoped Flux observations reach the environment UI. Do not mark Ready from job success or expand RBAC to work around the collector.
