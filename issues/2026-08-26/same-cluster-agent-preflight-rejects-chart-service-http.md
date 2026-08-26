# Same-cluster Agent preflight rejects the chart service HTTP endpoint

## Problem

The Agent chart derives a same-cluster endpoint as
`http://envplane-control-plane.<namespace>.svc:8080`. Runtime validation rejects
all HTTP URLs before checking endpoint mode, so the Agent preflight enters a
CrashLoop even though the service is intentionally cluster-local.

The live SM-09 diagnostic reported:

`ENVPLANE_CONTROL_PLANE_URL must use HTTPS unless ENVPLANE_ALLOW_INSECURE_CONTROL_PLANE=true`

## Resolution

Accept HTTP only for `sameCluster` endpoints. Keep remote endpoint validation
strictly HTTPS.

