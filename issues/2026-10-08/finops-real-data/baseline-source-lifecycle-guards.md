# Baseline metadata lifecycle and redirect guards

Status: fixed locally, regression tests; no push or live infrastructure change.

Problem: baseline Agent exact-UID checks did not reject resource-level
Environment ownership or deletionTimestamp before observation. The receiving API
already rejected these states, but producers could repeatedly prepare evidence
which could never be accepted. Metadata GET also inherited redirect handling,
and Metrics API UID/namespace (when present) were not checked against the pin.

Implementation prompt: fail closed for deleting or Environment-owned resources,
conflicting canonical component labels and supplied metrics UID/namespace mismatch;
use bounded no-redirect metadata transport. Preserve current exact UID, generation,
before/after verification and API runtime authorization. Do not require invented
Metrics API UID where the real source omits it; trusted current Pod verification
and creation/window/container checks still apply.

Regression fixtures cover Pod/PVC deletion and Environment ownership, conflicting
component labels, forbidden redirect, and supplied foreign Metrics API UID.
This improves producer consistency; it does not claim live baseline acceptance,
new resource authority, complete financial coverage or baseline budget aggregation.
