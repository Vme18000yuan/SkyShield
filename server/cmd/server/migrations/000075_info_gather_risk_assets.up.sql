BEGIN;
-- Associate a platform finding with the precise asset when ownership is known.
ALTER TABLE vulnerability ADD COLUMN asset_id BIGINT REFERENCES asset_entity(id) ON DELETE SET NULL;
CREATE INDEX idx_vuln_asset_id ON vulnerability(asset_id) WHERE asset_id IS NOT NULL;

-- Backfill only unambiguous exact matches for previously imported info-gath findings.
WITH matches AS (
    SELECT v.id AS vulnerability_id, MIN(b.asset_id) AS asset_id
    FROM vulnerability v
    JOIN endpoint e ON e.target_id=v.target_id AND e.url=v.url
    JOIN asset_endpoint_binding b ON b.endpoint_id=e.id
    WHERE v.source='info-gath'
    GROUP BY v.id
    HAVING COUNT(DISTINCT b.asset_id)=1
)
UPDATE vulnerability v SET asset_id=matches.asset_id
FROM matches WHERE v.id=matches.vulnerability_id AND v.asset_id IS NULL;

WITH matches AS (
    SELECT v.id AS vulnerability_id, MIN(b.asset_id) AS asset_id
    FROM vulnerability v
    JOIN website w ON w.target_id=v.target_id AND w.url=v.url
    JOIN asset_website_binding b ON b.website_id=w.id
    WHERE v.source='info-gath' AND v.asset_id IS NULL
    GROUP BY v.id
    HAVING COUNT(DISTINCT b.asset_id)=1
)
UPDATE vulnerability v SET asset_id=matches.asset_id
FROM matches WHERE v.id=matches.vulnerability_id AND v.asset_id IS NULL;

-- Keep the evidence row, but remove a duplicate observation from the pending
-- queue only if its corresponding platform risk still exists.
UPDATE ai_info_gather_record o
SET status='ignored',target_id=r.target_id,reason='已归集为对应的漏洞或风险',updated_at=clock_timestamp()
FROM ai_info_gather_record r
JOIN vulnerability v ON v.target_id=r.target_id AND v.url=r.payload->>'url'
    AND v.severity='info'
    AND v.vuln_type=CASE r.payload->>'type'
        WHEN 'ANONYMOUS_DATA' THEN '匿名数据响应（待复核）'
        WHEN 'REVIEW_TOKEN_DIFFERENCE' THEN '令牌响应差异（待复核）'
        ELSE '' END
WHERE o.run_id=r.run_id AND o.record_id=r.record_id
    AND o.kind='observation' AND o.status='pending'
    AND r.kind='vulnerability' AND r.status='imported'
    AND o.payload->>'category'=r.payload->>'type'
    AND o.payload->>'value'=r.payload->>'url';
COMMIT;
