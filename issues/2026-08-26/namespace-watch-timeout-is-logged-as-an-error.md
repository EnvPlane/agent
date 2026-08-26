# Namespace watch timeout is logged as an error after successful resync

## Observed

The Agent periodically logs `namespace watch failed` with a client context
deadline or cancellation while the following resync successfully reports
Events, Flux state and environment readiness.

## Impact

Healthy environments produce misleading error-level logs and monitoring noise.

## Required fix

Treat an expected watch context cancellation/deadline as an intentional
reconnect signal and log it at debug or info level. Preserve error logging for
unexpected transport, decoding and authorization failures.

## Verification

Run repeated resyncs for a healthy namespace and confirm successful reconnects
do not emit error-level watch failures.
