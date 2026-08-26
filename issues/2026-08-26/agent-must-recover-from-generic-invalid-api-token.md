# Agent does not recover from a stale token reported as `invalid api token`

## Observed

After the control-plane rollout, the persisted management Agent runtime token
was rejected with `401 {"error":"invalid api token"}`. The Agent has runtime
identity recovery, but only recognises the older `auth token is not issued`
wording. It keeps retrying status and Flux reports with the invalid token, so a
healthy feature environment remains `Creating`.

## Expected

Any recognised 401 response that denotes a stale Agent runtime token must
clear only the persisted runtime token, re-register using the mounted
registration claim, persist the replacement, and resume reporting without
operator intervention.

## Acceptance criteria

- Both `auth token is not issued` and `invalid api token` responses trigger
  agent runtime identity recovery.
- The registration claim is never logged or sent to non-registration APIs.
- A restarted Agent recovers and reports a ready feature environment after a
  control-plane token invalidation.
