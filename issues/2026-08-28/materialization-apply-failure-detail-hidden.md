# Materialization apply failure detail was hidden

Agent command diagnostics emitted only the normalized command error code after
a Secret materialization execution failure. Kubernetes source/apply errors
were therefore collapsed to `backend_unavailable` without the bounded API
status needed to distinguish authorization, validation, and transport faults.

## Resolution

Emit a warning for failed execution containing only command and plan
identifiers, the redacted stable error code, and the existing bounded error.
Secret values and payloads are never included in these errors.
