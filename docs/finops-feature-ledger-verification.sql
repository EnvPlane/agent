-- Aggregate evidence only. Never dump raw payload/credentials/application data.
-- Invoke with psql -v ON_ERROR_STOP=1 -f - as an authorized operator.
BEGIN READ ONLY;
SET LOCAL ROLE envplane_metering;
SET LOCAL envplane.tenant_id = 'default';
SELECT current_user AS evidence_role,
       rolsuper AS superuser, rolbypassrls AS bypass_rls
FROM pg_roles WHERE rolname = current_user;

SELECT count(*) AS default_tenant_batches
FROM finops_metering_ledger l
CROSS JOIN LATERAL jsonb_each(l.payload->'batches') b
WHERE l.tenant_id = 'default';

WITH batches AS (
 SELECT b.value AS batch
 FROM finops_metering_ledger l
 CROSS JOIN LATERAL jsonb_each(l.payload->'batches') b
 WHERE l.tenant_id='default' AND b.value->>'projectId'='app'
 ORDER BY b.value->>'periodEnd' DESC LIMIT 12
), reports AS (
 SELECT batch, d.value AS report
 FROM batches CROSS JOIN LATERAL
 jsonb_array_elements(COALESCE(NULLIF(batch->'dimensions','null'::jsonb),'[]'::jsonb)) d
 WHERE d.value->>'dimension' IN ('storage.used','network.transmit','network.receive')
)
SELECT batch->>'batchId' AS batch_id, batch->>'clusterId' AS cluster_id,
 batch->>'periodStart' AS period_start, batch->>'periodEnd' AS period_end,
 report->>'dimension' AS dimension, report->>'state' AS coverage_state,
 report->>'reason' AS coverage_reason,
 report->>'expectedResources' AS expected,
 report->>'observedResources' AS observed,
 (SELECT count(*) FROM jsonb_array_elements(report->'samples') s
   WHERE s->>'environmentId'='e2e-ui-full-652-1007652'
     AND s->>'namespace'='envplane-pr-e2e-ui-full-652-1007652') AS feature_samples,
 (SELECT count(*) FROM jsonb_array_elements(report->'samples') s
   WHERE s->>'namespace' IN ('app-backend','app2-backend')) AS baseline_samples,
 (SELECT sum((s->>'quantity')::numeric) FROM jsonb_array_elements(report->'samples') s
   WHERE s->>'environmentId'='e2e-ui-full-652-1007652') AS feature_quantity
FROM reports ORDER BY period_end DESC,dimension;

-- Closure requires two distinct persisted windows with all three dimensions,
-- correct feature ownership, positive quantities and exact storage PVC UIDs.
WITH batches AS (
 SELECT b.value AS batch FROM finops_metering_ledger l
 CROSS JOIN LATERAL jsonb_each(l.payload->'batches') b
 WHERE l.tenant_id='default' AND b.value->>'projectId'='app'
), qualified_reports AS (
 SELECT batch, d.value AS report
 FROM batches CROSS JOIN LATERAL
 jsonb_array_elements(COALESCE(NULLIF(batch->'dimensions','null'::jsonb),'[]'::jsonb)) d
 WHERE d.value->>'dimension' IN ('storage.used','network.transmit','network.receive')
   AND d.value->>'state'='complete' AND d.value->>'measurementKind'='measured'
   AND jsonb_array_length(d.value->'samples')>0
   AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(d.value->'samples') s
     WHERE s->>'environmentId' IS DISTINCT FROM 'e2e-ui-full-652-1007652'
        OR s->>'namespace' IS DISTINCT FROM 'envplane-pr-e2e-ui-full-652-1007652')
   AND (SELECT sum((s->>'quantity')::numeric)
        FROM jsonb_array_elements(d.value->'samples') s)>0
   AND (d.value->>'dimension'<>'storage.used' OR (
     jsonb_array_length(d.value->'samples')=2
     AND (SELECT count(DISTINCT s->>'resourceUid') FROM jsonb_array_elements(d.value->'samples') s
       WHERE s->>'resourceUid' IN ('bcf325e3-299c-4e01-9b43-1e4123fb5bdc','4ca9499d-9a5b-4f59-8278-110b2744f404')
         AND (s->>'usedBytes')::numeric>0)=2))
), qualifying_windows AS (
 SELECT batch->>'clusterId' cluster_id,batch->>'periodStart' period_start,batch->>'periodEnd' period_end
 FROM qualified_reports GROUP BY 1,2,3
 HAVING count(DISTINCT report->>'dimension')=3
)
SELECT count(*) AS qualifying_feature_windows,
       count(*)>=2 AS two_window_source_ingestion_verified,
       min(period_start) AS first_start,max(period_end) AS last_end
FROM qualifying_windows;
ROLLBACK;
