# Cached Metrics API windows blocked by immutable metadata conflicts

Status: fixed locally; parent compatible image rollout/live verification pending.

## Reproduction and cause

Parent live verification observed one accepted batch followed by minutes without
new records. Metrics Server refreshes every 60 seconds while the Agent polls at
10 seconds. The same BatchID was decorated again with a new GPU inventory
ObservedAt value. The API correctly rejected changed immutable evidence. The
Agent treated all HTTP errors as retryable and held that rejected batch for five
minutes, blocking newer collection windows. Restarting while Metrics API still
caches an accepted window can produce the same conflict.

## Codex implementation prompt / fix

Preserve API immutability. Classify Agent delivery status without logging URLs,
bearers or provider bodies. Transport, 5xx and 429 retry the exact frozen batch;
invalid/conflicting 4xx are terminal. Maintain a bounded recent finished-ID set
and skip cached acknowledged or terminal IDs before fetching inventory or other
changing decoration. A restarted Agent may receive one terminal conflict, then
must wait for a new metrics window rather than flood/retry for five minutes.
Drop expired pending windows without fabricating historical coverage.

## Regression coverage

- 60-second cached window / 10-second polls: one decoration and one ACK send.
- Restart conflict plus 400/401/403/409/422: one failed post, next new ID works.
- Transport/429/503: same immutable payload on every retry.
- Typed HTTP status classification, private response not exposed.

No server comparison, cluster, RBAC, storage labels, prices or source collection
semantics changed. No push. Live rollout verification belongs to parent.
