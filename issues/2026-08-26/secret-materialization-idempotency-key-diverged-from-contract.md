# Secret materialization result key diverged from the shared contract

## Problem

The Agent used a private hash of tenant, plan ID, plan digest, and item ID for
server-side apply and reported results. The shared contract instead binds the
key to tenant, project, environment, template digest, target namespace, item,
and operation. This would make an end-to-end materialization result fail
contract validation once the control plane validates each item.

## Resolution

Use `domain.SecretMaterializationIdempotencyKey` for apply and result records,
and assert the exact shared key in the Agent runtime transport test.
