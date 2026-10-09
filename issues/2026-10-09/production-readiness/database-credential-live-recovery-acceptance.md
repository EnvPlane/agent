# Database credential recovery needs live data-integrity acceptance

Status: OPEN coverage prerequisite, not a reproduced recovery bug. Candidate campaign: umbrella 0.4.682.

Pass 4: 121 race-enabled test/subtest events passed with no skips for database credentials/escrow and recovery-profile CLI. The fixture covers PostgreSQL, MySQL, MariaDB, MongoDB and Redis credential transport, binding, concurrent winner recovery, commit-before-write, missing key, escrow loss and replaced-PVC rejection. It does not connect to running database engines or prove existing records survive recovery.

Verification prompt for Codex: after project onboarding, use isolated disposable database environments for every commercially supported engine. Seed identifiable records, verify application login, remove only the explicitly authorized generated Secret while retaining PVC/data, then invoke the normal recovery path. Confirm the restored credential actually authenticates and records/checksums persist after Pod restart. Repeat with unavailable escrow key and replaced PVC: fail closed without regeneration, rotation or destructive reinitialization. Check actual project Agent RBAC rather than administrator access. Do not delete production Secrets or print credentials. Unsupported engines must be excluded explicitly from the support matrix.

Acceptance: actual engine login, preserved data and product recovery status are evidenced on the frozen candidate; no hand-written replacement passwords or administrator bypass. Full live recovery remains NOT RUN in this campaign.
