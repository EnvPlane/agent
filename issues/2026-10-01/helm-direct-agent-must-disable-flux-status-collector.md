# Helm Direct Agent ignores disabled Flux status collection

## Evidence

The remote project Agent for Helm Direct project `app2` is rendered with:

```yaml
rbac:
  fluxStatus:
    enabled: false
```

Despite that, its watcher constructs the legacy Flux collector and repeatedly
tries to list Kustomizations in `flux-system`. The intentionally
least-privilege ServiceAccount is denied, producing an error every resync.

## Expected behavior

An Agent must not collect Flux status when its deployment backend does not
enable Flux status. Flux projects must retain their configured scoped or
legacy collection behavior.

## Codex implementation prompt

Add an explicit Agent configuration switch for Flux status collection. Preserve
backwards compatibility for direct binary users, wire the switch from the
Agent chart's `rbac.fluxStatus.enabled` value, and add unit/chart tests for
both enabled and disabled modes. Publish a new Agent chart after validation.
