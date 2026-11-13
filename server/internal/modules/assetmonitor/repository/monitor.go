package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/netip"
	"strings"
	"time"

	"github.com/lib/pq"
	contractresults "github.com/yyhuni/lunafox/contracts/results"
	"github.com/yyhuni/lunafox/server/internal/geolocation"
	monitor "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/application"
	registry "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) Read(ctx context.Context, user int, organizations []int) (*monitor.Snapshot, error) {
	var snapshot monitor.Snapshot
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var member bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE)`, registry.DefaultTenantID, user).Scan(&member).Error; err != nil {
			return err
		}
		if !member {
			return monitor.ErrForbidden
		}
		var count int64
		if err := tx.Raw(`SELECT count(*) FROM organization WHERE id=ANY(?::int[]) AND deleted_at IS NULL`, pq.Array(organizations)).Scan(&count).Error; err != nil {
			return err
		}
		if count != int64(len(organizations)) {
			return monitor.ErrOrganizationNotFound
		}
		var payload []byte
		if err := tx.Raw(snapshotSQL, registry.DefaultTenantID, pq.Array(organizations)).Row().Scan(&payload); err != nil {
			return err
		}
		var response struct {
			monitor.Snapshot
			Pending []string `json:"pendingIPs"`
			Hosts   []string `json:"pendingHosts"`
		}
		if err := json.Unmarshal(payload, &response); err != nil {
			return err
		}
		snapshot = response.Snapshot
		for i := range snapshot.RecentAssets {
			snapshot.RecentAssets[i].DisplayType = registry.AssetDisplayType(snapshot.RecentAssets[i].AssetType, snapshot.RecentAssets[i].Label)
		}
		snapshot.PendingIPs = response.Pending
		snapshot.PendingHosts = response.Hosts
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return &snapshot, err
}

func (s *Store) ClaimHost(ctx context.Context, host string) (bool, error) {
	name, ok := contractresults.NormalizeSubdomainDNSName(host)
	if !ok || name != host {
		return false, nil
	}
	result := s.db.WithContext(ctx).Exec(`INSERT INTO asset_monitor_dns_resolution(host,next_attempt_at) VALUES(?,CURRENT_TIMESTAMP+INTERVAL '15 minutes') ON CONFLICT(host) DO UPDATE SET next_attempt_at=EXCLUDED.next_attempt_at WHERE asset_monitor_dns_resolution.next_attempt_at<=CURRENT_TIMESTAMP`, host)
	return result.RowsAffected == 1, result.Error
}

func (s *Store) SaveHostResolution(ctx context.Context, host, ip string, resolvedAt time.Time) error {
	name, ok := contractresults.NormalizeSubdomainDNSName(host)
	address, public := geolocation.NormalizePublicIP(ip)
	if !ok || name != host || !public {
		return monitor.ErrInvalidSelection
	}
	return s.db.WithContext(ctx).Exec(`INSERT INTO asset_monitor_dns_resolution(host,ip,resolved_at,next_attempt_at) VALUES(?,?,?,?) ON CONFLICT(host) DO UPDATE SET ip=EXCLUDED.ip,resolved_at=EXCLUDED.resolved_at,next_attempt_at=EXCLUDED.next_attempt_at WHERE asset_monitor_dns_resolution.resolved_at IS NULL OR asset_monitor_dns_resolution.resolved_at<=EXCLUDED.resolved_at`, host, address, resolvedAt, resolvedAt.Add(time.Hour)).Error
}

func (s *Store) ClaimIP(ctx context.Context, ip string) (bool, error) {
	normalized, ok := geolocation.NormalizePublicIP(ip)
	if !ok {
		address, err := netip.ParseAddr(strings.TrimSpace(ip))
		if err != nil {
			return false, nil
		}
		// Negative scheduling entries stop private-only inventories from starving
		// public candidates. They never receive coordinates or provider requests.
		return false, s.db.WithContext(ctx).Exec(`INSERT INTO asset_monitor_ip_location(ip,next_attempt_at) VALUES(?,CURRENT_TIMESTAMP+INTERVAL '7 days') ON CONFLICT(ip) DO UPDATE SET next_attempt_at=EXCLUDED.next_attempt_at`, address.Unmap().WithZone("").String()).Error
	}
	result := s.db.WithContext(ctx).Exec(`INSERT INTO asset_monitor_ip_location(ip,next_attempt_at) VALUES(?,CURRENT_TIMESTAMP+INTERVAL '15 minutes') ON CONFLICT(ip) DO UPDATE SET next_attempt_at=EXCLUDED.next_attempt_at WHERE asset_monitor_ip_location.next_attempt_at<=CURRENT_TIMESTAMP`, normalized)
	return result.RowsAffected == 1, result.Error
}
func (s *Store) SaveLocation(ctx context.Context, l geolocation.Location) error {
	ip, ok := geolocation.NormalizePublicIP(l.ObservedIP)
	if !ok {
		return monitor.ErrInvalidSelection
	}
	return s.db.WithContext(ctx).Exec(`INSERT INTO asset_monitor_ip_location(ip,latitude,longitude,country_code,region_name,city_name,provider_key,resolved_at,next_attempt_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(ip) DO UPDATE SET latitude=EXCLUDED.latitude,longitude=EXCLUDED.longitude,country_code=EXCLUDED.country_code,region_name=EXCLUDED.region_name,city_name=EXCLUDED.city_name,provider_key=EXCLUDED.provider_key,resolved_at=EXCLUDED.resolved_at,next_attempt_at=EXCLUDED.next_attempt_at WHERE asset_monitor_ip_location.resolved_at IS NULL OR asset_monitor_ip_location.resolved_at<=EXCLUDED.resolved_at`, ip, l.Latitude, l.Longitude, l.CountryCode, l.RegionName, l.CityName, l.ProviderKey, l.ResolvedAt, l.ResolvedAt.Add(7*24*time.Hour)).Error
}

const snapshotSQL = `WITH
 p AS (SELECT ?::uuid tenant,?::int[] org_ids),
 orgs AS (SELECT o.id,o.name FROM organization o,p WHERE o.id=ANY(p.org_ids) AND o.deleted_at IS NULL),
 org_targets AS (SELECT DISTINCT o.id org_id,t.id target_id FROM orgs o JOIN organization_target ot ON ot.organization_id=o.id JOIN target t ON t.id=ot.target_id AND t.deleted_at IS NULL),
 targets AS (SELECT DISTINCT target_id FROM org_targets),
 direct_links AS (
  SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN subdomain a ON a.target_id=ot.target_id JOIN asset_subdomain_binding b ON b.subdomain_id=a.id
  UNION SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN subdomain a ON a.target_id=ot.target_id JOIN asset_subdomain_variant_binding b ON b.subdomain_id=a.id
  UNION SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN website a ON a.target_id=ot.target_id JOIN asset_website_binding b ON b.website_id=a.id
  UNION SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN endpoint a ON a.target_id=ot.target_id JOIN asset_endpoint_binding b ON b.endpoint_id=a.id
  UNION SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN endpoint a ON a.target_id=ot.target_id JOIN asset_endpoint_variant_binding b ON b.endpoint_id=a.id
  UNION SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN host_port_mapping a ON a.target_id=ot.target_id JOIN asset_host_port_binding b ON b.host_port_id=a.id
  UNION SELECT ot.org_id,o.asset_id FROM org_targets ot JOIN asset_observation o ON o.target_id=ot.target_id JOIN p ON o.tenant_id=p.tenant
  UNION SELECT orgs.id,o.asset_id FROM orgs JOIN asset_observation o ON o.organization_id=orgs.id JOIN p ON o.tenant_id=p.tenant LEFT JOIN target t ON t.id=o.target_id WHERE o.original_target_id IS NULL OR t.deleted_at IS NULL AND t.id IS NOT NULL
 ),
 links AS (
  SELECT org_id,asset_id FROM direct_links
  UNION SELECT l.org_id,r.root_asset_id FROM direct_links l JOIN asset_root_domain_source r ON r.source_asset_id=l.asset_id JOIN p ON p.tenant=r.tenant_id
  UNION SELECT ot.org_id,r.root_asset_id FROM org_targets ot JOIN asset_root_domain_target r ON r.target_id=ot.target_id JOIN p ON p.tenant=r.tenant_id
 ),
 assets AS (SELECT e.id,e.tenant_id,e.asset_type,e.label,e.first_seen_at,e.last_seen_at FROM asset_entity e,p WHERE e.tenant_id=p.tenant AND EXISTS(SELECT 1 FROM links l WHERE l.asset_id=e.id)),
 inventory_org_sources AS (
  SELECT l.org_id,s.source_asset_id,s.category,s.label,s.first_seen_at FROM asset_inventory_source s JOIN p ON s.tenant_id=p.tenant JOIN direct_links l ON l.asset_id=s.source_asset_id WHERE s.source_target_id IS NULL AND s.label IS NOT NULL
  UNION ALL
  SELECT ot.org_id,s.source_asset_id,s.category,s.label,s.first_seen_at FROM asset_inventory_source s JOIN p ON s.tenant_id=p.tenant JOIN org_targets ot ON ot.target_id=s.source_target_id WHERE s.source_target_id IS NOT NULL AND s.label IS NOT NULL
 ),
 inventory_sources AS (SELECT DISTINCT source_asset_id,category,label,first_seen_at FROM inventory_org_sources),
 inventory AS (SELECT category,label,min(first_seen_at) first_seen_at FROM inventory_sources GROUP BY category,label),
 inventory_hosts AS (
  SELECT category,label,hostname FROM (SELECT category,label,CASE WHEN category='subdomain' THEN label ELSE regexp_replace(label, ':[0-9]+$', '') END hostname FROM inventory WHERE category IN ('subdomain','service')) x
  WHERE hostname ~ '^[a-z0-9][a-z0-9.-]*[a-z0-9]$' AND position('.' IN hostname)>0 AND hostname !~ '^[0-9.]+$'
 ),
 observations AS (SELECT o.* FROM asset_observation o JOIN assets e ON e.id=o.asset_id AND e.tenant_id=o.tenant_id LEFT JOIN target t ON t.id=o.target_id,p WHERE (o.organization_id=ANY(p.org_ids) OR o.target_id IN(SELECT target_id FROM targets)) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)),
 base_asset_ips AS (
  SELECT DISTINCT b.asset_id,host(h.ip) ip FROM host_port_mapping h JOIN targets t ON t.target_id=h.target_id JOIN asset_host_port_binding b ON b.host_port_id=h.id JOIN assets e ON e.id=b.asset_id
  UNION SELECT e.id,e.label FROM assets e WHERE e.asset_type='ip'
  UNION SELECT o.asset_id,COALESCE(NULLIF(o.payload->>'ip',''),NULLIF(o.payload->>'serviceIp',''),o.payload->'extra'->>'ip') FROM observations o WHERE COALESCE(NULLIF(o.payload->>'ip',''),NULLIF(o.payload->>'serviceIp',''),o.payload->'extra'->>'ip','')<>''
  UNION SELECT b.asset_id,host(h.ip) FROM website w JOIN targets t ON t.target_id=w.target_id JOIN asset_website_binding b ON b.website_id=w.id JOIN host_port_mapping h ON h.target_id=w.target_id AND lower(h.host)=lower(w.host)
  UNION SELECT b.asset_id,host(h.ip) FROM endpoint w JOIN targets t ON t.target_id=w.target_id JOIN asset_endpoint_binding b ON b.endpoint_id=w.id JOIN host_port_mapping h ON h.target_id=w.target_id AND lower(h.host)=lower(w.host)
  UNION SELECT b.asset_id,host(h.ip) FROM subdomain w JOIN targets t ON t.target_id=w.target_id JOIN asset_subdomain_binding b ON b.subdomain_id=w.id JOIN host_port_mapping h ON h.target_id=w.target_id AND lower(h.host)=lower(w.dns_name)
 ),
 asset_ips AS (
  SELECT asset_id,ip FROM base_asset_ips
  UNION SELECT r.root_asset_id,i.ip FROM base_asset_ips i JOIN asset_root_domain_source r ON r.source_asset_id=i.asset_id JOIN p ON p.tenant=r.tenant_id JOIN assets e ON e.id=r.root_asset_id
 ),
 country_sources AS (
  SELECT o.asset_id,o.payload,o.observed_at,o.id FROM observations o
  UNION ALL SELECT r.root_asset_id,o.payload,o.observed_at,o.id FROM observations o JOIN asset_root_domain_source r ON r.source_asset_id=o.asset_id AND r.tenant_id=o.tenant_id JOIN assets e ON e.id=r.root_asset_id
 ),
 country_metadata AS (SELECT DISTINCT ON(o.asset_id) o.asset_id,upper(o.payload->'extra'->>'country') code,left(concat_ws(' · ',NULLIF(o.payload->'extra'->>'countryName',''),NULLIF(o.payload->'extra'->>'region',''),NULLIF(o.payload->'extra'->>'city','')),160) label FROM country_sources o WHERE upper(o.payload->'extra'->>'country') ~ '^[A-Z]{2}$' AND upper(o.payload->'extra'->>'country') NOT IN('XX','ZZ') ORDER BY o.asset_id,o.observed_at DESC,o.id DESC),
 ip_locations AS (SELECT DISTINCT ON(i.asset_id) i.asset_id,l.latitude,l.longitude,l.country_code,l.region_name,l.city_name,l.resolved_at FROM asset_ips i JOIN assets e ON e.id=i.asset_id JOIN asset_monitor_ip_location l ON l.ip=i.ip WHERE l.resolved_at IS NOT NULL ORDER BY i.asset_id,l.resolved_at DESC,i.ip),
 located AS (SELECT e.id asset_id,i.latitude,i.longitude,COALESCE(NULLIF(i.country_code,''),c.code,'') code,CASE WHEN i.asset_id IS NOT NULL THEN concat_ws(' · ',NULLIF(i.region_name,''),NULLIF(i.city_name,'')) ELSE COALESCE(c.label,'') END label,COALESCE(i.region_name,'') region_name,COALESCE(i.city_name,'') city_name,CASE WHEN i.asset_id IS NOT NULL THEN 'ip' ELSE 'country' END location_precision,COALESCE(i.resolved_at<CURRENT_TIMESTAMP-INTERVAL '7 days',FALSE) expired FROM assets e LEFT JOIN ip_locations i ON i.asset_id=e.id LEFT JOIN country_metadata c ON c.asset_id=e.id WHERE i.asset_id IS NOT NULL OR c.asset_id IS NOT NULL),
 inventory_location_candidates AS (
  SELECT s.category,s.label,l.latitude,l.longitude,l.code,l.label location_label,l.region_name,l.city_name,l.location_precision,l.expired,CASE WHEN l.location_precision='ip' THEN 0 ELSE 2 END priority,s.source_asset_id FROM inventory_sources s JOIN located l ON l.asset_id=s.source_asset_id
  UNION ALL
  SELECT h.category,h.label,l.latitude,l.longitude,l.country_code,concat_ws(' · ',NULLIF(l.region_name,''),NULLIF(l.city_name,'')),l.region_name,l.city_name,'dns'::text,(l.resolved_at<CURRENT_TIMESTAMP-INTERVAL '7 days' OR d.resolved_at<CURRENT_TIMESTAMP-INTERVAL '1 hour'),1,0::bigint FROM inventory_hosts h JOIN asset_monitor_dns_resolution d ON d.host=h.hostname JOIN asset_monitor_ip_location l ON l.ip=d.ip WHERE l.resolved_at IS NOT NULL
 ),
 inventory_located AS (SELECT DISTINCT ON(category,label) category,label,latitude,longitude,code,location_label,region_name,city_name,location_precision,expired FROM inventory_location_candidates ORDER BY category,label,priority,expired,source_asset_id),
 map_positions AS (SELECT CASE WHEN location_precision IN ('ip','dns') THEN round(latitude::numeric,4) END latitude,CASE WHEN location_precision IN ('ip','dns') THEN round(longitude::numeric,4) END longitude,code,location_label label,region_name,city_name,location_precision,expired FROM inventory_located),
 groups AS (SELECT CASE WHEN location_precision IN ('ip','dns') THEN location_precision||':'||md5(jsonb_build_array(latitude,longitude,expired,code,region_name,city_name)::text) ELSE 'country:'||code END key,latitude,longitude,code,max(label) label,region_name,city_name,count(*) count,location_precision,expired FROM map_positions GROUP BY location_precision,code,latitude,longitude,region_name,city_name,expired),
 risk AS (SELECT v.* FROM vulnerability v JOIN targets t ON t.target_id=v.target_id),
 scan_state AS (SELECT s.* FROM scan s JOIN targets t ON t.target_id=s.target_id WHERE s.deleted_at IS NULL),
 days AS (SELECT generate_series((CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date-6,(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date,'1 day')::date AS metric_day),
 pending AS (SELECT DISTINCT i.ip FROM (SELECT ip FROM asset_ips UNION SELECT d.ip FROM inventory_hosts h JOIN asset_monitor_dns_resolution d ON d.host=h.hostname WHERE d.ip IS NOT NULL) i LEFT JOIN asset_monitor_ip_location l ON l.ip=i.ip WHERE l.ip IS NULL OR l.next_attempt_at<=CURRENT_TIMESTAMP ORDER BY i.ip LIMIT 128),
 pending_hosts AS (SELECT DISTINCT h.hostname FROM inventory_hosts h LEFT JOIN inventory_located l ON l.category=h.category AND l.label=h.label LEFT JOIN asset_monitor_dns_resolution d ON d.host=h.hostname WHERE (l.category IS NULL OR l.location_precision IN ('dns','country')) AND (d.host IS NULL OR d.next_attempt_at<=CURRENT_TIMESTAMP) ORDER BY h.hostname LIMIT 128)
SELECT jsonb_build_object(
 'generatedAt',CURRENT_TIMESTAMP,
 'organizations',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',o.id,'displayName',o.name,'targetCount',(SELECT count(*) FROM org_targets ot WHERE ot.org_id=o.id),'assetCount',(SELECT count(DISTINCT (s.category,s.label)) FROM inventory_org_sources s WHERE s.org_id=o.id)) ORDER BY o.id) FROM orgs o),'[]'::jsonb),
 'summary',jsonb_build_object('totalAssets',(SELECT count(*) FROM inventory),'targets',(SELECT count(*) FROM targets),'ipAssets',(SELECT count(*) FROM inventory WHERE category='ip'),'risks',(SELECT count(*) FROM risk),'newAssets24h',(SELECT count(*) FROM inventory WHERE first_seen_at>=CURRENT_TIMESTAMP-INTERVAL '24 hours'),'newRisks24h',(SELECT count(*) FROM risk WHERE created_at>=CURRENT_TIMESTAMP-INTERVAL '24 hours'),'runningScans',(SELECT count(*) FROM scan_state WHERE status IN('running','initiated')),'pendingScans',(SELECT count(*) FROM scan_state WHERE status='pending'),'locatedAssets',(SELECT count(*) FROM inventory_located),'unlocatedAssets',(SELECT count(*) FROM inventory)-(SELECT count(*) FROM inventory_located)),
 'assetTypes',COALESCE((SELECT jsonb_agg(jsonb_build_object('key',asset_type,'count',n) ORDER BY n DESC,asset_type) FROM(SELECT asset_type,count(*) n FROM assets GROUP BY asset_type)x),'[]'::jsonb),
 'assetCategories',COALESCE((SELECT jsonb_agg(jsonb_build_object('key',category,'count',n) ORDER BY n DESC,category) FROM(SELECT CASE WHEN asset_type IN ('service','endpoint') AND (label ILIKE 'http://%' OR label ILIKE 'https://%') THEN 'web' ELSE asset_type END AS category,count(*) n FROM assets GROUP BY 1)x),'[]'::jsonb),
 'inventoryCategories',COALESCE((SELECT jsonb_agg(jsonb_build_object('key',category,'count',n) ORDER BY n DESC,category) FROM(SELECT category,count(*) n FROM inventory GROUP BY category)x),'[]'::jsonb),
 'severities',COALESCE((SELECT jsonb_agg(jsonb_build_object('key',severity,'count',n)) FROM(SELECT severity,count(*) n FROM risk GROUP BY severity)x),'[]'::jsonb),
 'ports',COALESCE((SELECT jsonb_agg(jsonb_build_object('key',port::text,'count',n) ORDER BY n DESC,port) FROM(SELECT substring(label FROM ':([0-9]+)$')::integer port,count(*) n FROM inventory WHERE category='service' GROUP BY 1 ORDER BY n DESC,port LIMIT 5)x),'[]'::jsonb),
 'technologies',COALESCE((SELECT jsonb_agg(jsonb_build_object('key',tech,'count',n) ORDER BY n DESC,tech) FROM(SELECT tt.value AS tech,count(DISTINCT w.url) n FROM website w JOIN targets t ON t.target_id=w.target_id CROSS JOIN LATERAL unnest(w.tech) AS tt(value) WHERE tt.value<>'' GROUP BY tt.value ORDER BY n DESC,tt.value LIMIT 8)x),'[]'::jsonb),
 'trend',(SELECT jsonb_agg(jsonb_build_object('date',to_char(d.metric_day,'YYYY-MM-DD'),'assets',(SELECT count(*) FROM inventory e WHERE (e.first_seen_at AT TIME ZONE 'Asia/Shanghai')::date=d.metric_day),'risks',(SELECT count(*) FROM risk v WHERE (v.created_at AT TIME ZONE 'Asia/Shanghai')::date=d.metric_day)) ORDER BY d.metric_day) FROM days d),
 'recentAssets',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',category||':'||label,'label',label,'assetType',category,'firstSeenAt',first_seen_at) ORDER BY first_seen_at DESC,category,label) FROM(SELECT category,label,first_seen_at FROM inventory ORDER BY first_seen_at DESC,category,label LIMIT 50)x),'[]'::jsonb),
 'locations',COALESCE((SELECT jsonb_agg(jsonb_build_object('key',key,'latitude',latitude,'longitude',longitude,'countryCode',code,'regionName',region_name,'cityName',city_name,'label',label,'count',count,'precision',location_precision,'expired',expired) ORDER BY count DESC,key) FROM groups),'[]'::jsonb),
 'pendingIPs',COALESCE((SELECT jsonb_agg(ip) FROM pending),'[]'::jsonb),
 'pendingHosts',COALESCE((SELECT jsonb_agg(hostname) FROM pending_hosts),'[]'::jsonb)
)`
