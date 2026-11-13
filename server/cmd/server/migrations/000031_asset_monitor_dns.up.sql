-- DNS-derived geography is a separate, expiring observation. It never
-- rewrites recorded scan IPs or asserts that a port is reachable at this IP.
CREATE TABLE asset_monitor_dns_resolution (
 host TEXT PRIMARY KEY,
 ip TEXT,
 resolved_at TIMESTAMPTZ,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 CONSTRAINT asset_monitor_dns_pair CHECK ((ip IS NULL AND resolved_at IS NULL) OR (ip IS NOT NULL AND resolved_at IS NOT NULL))
);
CREATE INDEX asset_monitor_dns_next_attempt ON asset_monitor_dns_resolution(next_attempt_at);
