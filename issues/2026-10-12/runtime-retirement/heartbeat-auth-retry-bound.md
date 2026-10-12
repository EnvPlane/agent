# Rejected heartbeat identity causes indefinite high-rate polling

Status: fixed locally; published Agent/deployed acceptance pending.

## Evidence and implementation

Readiness observations on umbrella 0.4.721 identified retired Agent identities
polling a replacement management installation every 30 seconds. The API correctly
rejects them. The runtime now applies typed 401/403/404 exponential backoff from
one minute to ten minutes; successful recovery resets it. Failed heartbeats no
longer dispatch resource scans. No identity adoption or additional permissions
were introduced. Complete local Agent race tests pass.

## Codex follow-up prompt

Repeat live rejection/recovery with an isolated Agent after component publication.
Show authenticated degraded readiness and operator-reviewed reconnect versus
retire choices. Preserve original base workloads/PVCs. Never silently reissue a
credential or delete live runtimes; only exact explicitly reviewed targets may be
retired. Coordinate with deploy's stale-agents-after-management-replacement ticket.
