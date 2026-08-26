# Namespace watcher does not resync while its watch stream is active

## Observed

`pr-20260826-test-app-full-114` reached healthy Flux and running workloads, but remained `Creating` in EnvPlane. The namespace watcher performs `SyncOnce` only before calling the blocking `WatchNamespaces`; it reaches its resync timer only after that watch returns.

## Expected

The watcher must continue periodic reconciliation while its Kubernetes watch stream is active, so readiness changes in Pods and Flux Kustomizations are reported without a namespace add/delete event.

## Acceptance criteria

1. Keep the namespace watch active for event responsiveness.
2. Run `SyncOnce` on the configured resync interval concurrently with the active watch.
3. Preserve retry behaviour when the watch exits or fails.
4. Add a test proving a ready status is reported after a timer-driven resync.
