# remote repair recovers persisted agent auth

## Evidence

During isolated remote-cluster repair validation, the runner re-registered with
a new credential while the agent retained a persisted runtime token. The control
plane rejected that token with `agent auth token is not configured`, but the
agent did not classify the response as recoverable and therefore did not retry
registration with the mounted bootstrap credential.

## Implementation prompt

Classify the 401 missing-runtime-credential response as stale Agent runtime
identity. Clear only the persisted runtime token and re-register with the
one-time bootstrap token already mounted by the managed chart.

## Acceptance criteria

- The response triggers the existing runtime-auth recovery path.
- The persisted token is cleared before registration.
- No credential value is written to logs or chart values.
