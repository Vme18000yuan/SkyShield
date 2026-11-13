-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
ALTER TABLE asset_monitor_ip_location
  DROP CONSTRAINT asset_monitor_city_labels,
  DROP COLUMN country_code,
  DROP COLUMN region_name,
  DROP COLUMN city_name;
