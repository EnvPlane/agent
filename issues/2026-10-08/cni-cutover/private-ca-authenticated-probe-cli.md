# Operator NetworkPolicy CLI must support reviewed private management TLS

The existing CLI always supplied a nil HTTP client to authenticated transport.
That trusts only host system roots; the approved private management CA cannot
be configured, although runtime Agent HTTPS supports a public CA Secret.

CLI now accepts the reviewed CA file and TLS server name through the existing
secure HTTP client factory. HTTPS, certificate verification, redirect refusal,
identity/generation challenge binding and server replay checks remain. Token
input must be an owner-private regular file; symlinks/group/world-readable files
are rejected. No token or challenge nonce appears in arguments/logs/report.

## Codex implementation prompt

Run the operator diagnostic with existing candidate node admin authority and
the actual current scoped project Agent token. Use current candidate cluster UID
and server-issued generation/nonce. Submit over the trusted private gateway CA;
do not extract browser sessions or forge Agent tokens. Verify API readiness
receives bounded fresh evidence, temporary namespace cleanup, positive controls
and deny/allow recovery. This is single-node Pod IPv4 TCP evidence, not all CNI
topologies/UDP/multi-node certification.
