# Materialization item result can emit non-contract error codes

The Kubernetes materializer uses executor-local codes such as
`foreign_secret` and `unsafe_secret_type`. Passing those values directly into
the shared result DTO can produce a wire result outside the canonical error
code vocabulary.

## Resolution

Map executor-local item failures to the shared redacted error codes before
reporting the command result.

## Status

Resolved with the agent Secret materialization command transport release.
