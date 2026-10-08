# MySQL symlinks unnecessarily block read-only PVC allocation measurement

Status: local fix; candidate positive gauge proof pending; no push.

Actual candidate exporter failure category after component-label normalization:
symlink_unsupported. A MySQL directory symlink need not invalidate a physical
directory allocation gauge: metadata for the link inode can be counted without
following its target. Logical referenced datasets outside the PVC remain excluded.

Codex implementation: confined WalkDir never follows symlink directories;
DirEntry.Info counts symlink inode allocated blocks only. Keep root mount symlinks
rejected, current UID checks before/after, no contents/exec/dataset writes, denied
metadata and scan bounds fail closed. Gauge remains directory allocated blocks,
not CSI quota, logical dataset contents or provider billing.

Acceptance: external target file growth never affects gauge; positive current
MySQL PVC measurements and normal tenant-bound feature ingestion must be proven
separately. Never replace missing baseline Environment bindings with fake IDs.
