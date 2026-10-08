# Generated credential replay overwrites retained database password

## Root cause and local prevention

`SecretMaterializer.executeItem` generated new random data on every replay before
calling SSA. Kubernetes ownership/idempotency checked the plan but did not preserve
password bytes. Database initialization variables do not reset accounts already
stored on PVCs. This can split an application Secret from the database account.

Local code now reuses an owned generated Secret without writes, validates supported
engine aliases and uses a stable tenant/project/environment/item/name/generator
identity digest. It never stores a password or password hash in a plan/status.
Legacy records require their original digest. New credentials use atomic POST.
Missing DB credentials with any target PVC, unavailable inventory, or mismatched
aliases fail closed. Generated DB credentials cannot be cleaned up while PVCs
remain. Recovery reports canonical non-retryable validation failure, not a transient
backend outage. Source credentials, auth settings, database accounts and PVCs are
never changed by the recovery guard.

## Verification scope

Mocked create/replay/recreate/restore and cleanup cases cover PostgreSQL, MySQL,
MariaDB, MongoDB and Redis. HTTP fixtures verify namespace scope, atomic creation,
conflicts, pagination, malformed/null inventory and denied permissions. No live
engine restore or account rotation was performed. Runner/bootstrap trace found no
second credential generator to patch; typed generator profiles execute on Agent.

## Remaining lifecycle work and Codex implementation prompt

Coordinate an immutable exact database/PVC/credential identity contract, serialized
restore/provisioning gates and approved encrypted credential escrow/restore mapping.
Do not infer association from claim names. Namespace-wide guarding is intentionally
conservative and cannot see orphan retained PVs or externally restored data arriving
after inventory. Kubernetes has no atomic Secret-create/PVC-inventory transaction.
Support approved engine-specific rotation through a separately audited account
reconciliation protocol; never silently generate a replacement password, weaken
authentication, discard data or log/export Secret values. Existing historical DB
password mismatch still requires explicit administrator recovery. A different
plan's cleanup may not adopt/delete an original-plan Secret without an approved
ownership migration. Verify engine behavior on isolated databases before claiming
the generic restore/rotation lifecycle complete. Configure cluster encryption at
rest separately; this patch does not establish a new encryption/escrow service.

No push or infrastructure changes authorized in this iteration.
