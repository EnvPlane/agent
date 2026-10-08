BEGIN READ ONLY;
SET LOCAL ROLE envplane_metering;
SET LOCAL envplane.tenant_id='default';
WITH batches AS (
 SELECT b.value batch FROM finops_metering_ledger l
 CROSS JOIN LATERAL jsonb_each(l.payload->'batches') b
 WHERE l.tenant_id='default' AND b.value->>'projectId'='app'
   AND EXISTS (SELECT 1 FROM jsonb_array_elements(b.value->'dimensions') d
      WHERE d->>'dimension'='storage.used' AND d->>'state'='complete')
 ORDER BY b.value->>'periodEnd' DESC LIMIT 2
)
SELECT batch->>'batchId' batch_id,batch->>'periodStart' period_start,batch->>'periodEnd' period_end,
 batch->>'expectedPods' expected_cpu_pods,batch->>'measuredPods' measured_cpu_pods,
 batch->>'unattributedPods' unallocated_cpu_pods,d->>'dimension' dimension,
 d->>'state' state,d->>'expectedResources' expected,d->>'observedResources' observed,
 s->>'componentId' component,s->>'resourceUid' resource_uid,s->>'usedBytes' used_bytes,
 CASE WHEN d->>'dimension' LIKE 'network.%' THEN round((s->>'quantity')::numeric*1073741824) END measured_network_bytes
FROM batches CROSS JOIN LATERAL jsonb_array_elements(batch->'dimensions') d
CROSS JOIN LATERAL jsonb_array_elements(d->'samples') s
WHERE d->>'dimension' IN ('storage.used','network.receive','network.transmit')
ORDER BY period_end,dimension,component;

WITH latest AS (
 SELECT b.value batch FROM finops_metering_ledger l
 CROSS JOIN LATERAL jsonb_each(l.payload->'batches') b
 WHERE l.tenant_id='default' AND b.value->>'projectId'='app'
   AND EXISTS (SELECT 1 FROM jsonb_array_elements(b.value->'dimensions') d
      WHERE d->>'dimension'='storage.used' AND d->>'state'='complete')
 ORDER BY b.value->>'periodEnd' DESC LIMIT 2
), windows AS (
 SELECT (batch->>'periodStart')::timestamptz period_start,
        lag((batch->>'periodEnd')::timestamptz) OVER(ORDER BY batch->>'periodEnd') previous_end
 FROM latest
)
SELECT extract(epoch FROM period_start-previous_end) uncovered_seconds
FROM windows WHERE previous_end IS NOT NULL;
ROLLBACK;
