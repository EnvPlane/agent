# Explicit component label spellings disagree across metering paths

Status: fixed locally; live exporter/Agent verification pending; no push.

Chart fix 72eb1b1 uses app.kubernetes.io/component, while pinned PVC verification
and measured PVC inventory accepted only envplane.io/component. Other collectors
silently preferred one value when both labels contradicted, risking inconsistent
attribution between CPU, storage and network dimensions.

Codex implementation: use one explicit component resolver in every FinOps path.
Accept either label or both when identical; reject missing, whitespace-invalid or
contradictory values. Never derive component from claim/Pod/application names.
Exporter identity verification remains namespace/name/current UID/component only;
tenant ledger requires independent registered Environment/project binding.

Acceptance: chart-only PVC labels work; contradictory labels fail closed across
all collectors. Baseline PVC measurements can be private gauges without invented
Environment IDs; attribution awaits a separately reviewed BaseResourceBinding model.
