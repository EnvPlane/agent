# Secret materialization loop keeps a stale runtime token

## Symptom

After the control plane reissues a same-cluster agent identity, heartbeat
recovery obtains a fresh runtime token, but the Secret materialization polling
loop continues sending the token captured at process startup. The endpoint
returns `401 invalid api token` and encrypted-clone commands cannot be claimed.

## Root cause

`RunSecretMaterializationCommands` received a value copy of `Config` and used
`cfg.AgentAuthToken` for fetch and result requests. Heartbeat recovery updated
the shared reporter, not that stale copy.

## Fix

Make the reporter runtime token synchronized and have materialization fetch and
result reporting read the current token from the reporter. Add a regression
test covering a token rotation after the startup configuration was captured.

## Verification

`go test ./agent ./apps/agent` passes.
